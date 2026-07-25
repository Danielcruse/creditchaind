package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// applyPlatformFee is the single place every completed P2P Trading
// Center settlement (direct P2P offer accept/release, Hub Rate trade,
// Buy Request fulfillment) routes its final payout through - splits
// amount into (amount - fee) to the real recipient and
// core.PlatformFeeAmount(amount) to the configured platform fee
// address, both drawn from the same already-escrowed/authorized
// balance so total value moved never exceeds amount. If no fee
// address is configured (PLATFORM_FEE_ADDRESS unset), the full amount
// goes to the recipient and nothing is skimmed - fails closed, same
// disclosed-gap convention as ADMIN_TOKEN/NETWORK_SECRET elsewhere in
// this codebase, rather than silently failing to nothing or panicking.
func applyPlatformFee(store *core.TokenStore, from, to string, amount uint64) bool {
	feeAddr := os.Getenv("PLATFORM_FEE_ADDRESS")
	fee := core.PlatformFeeAmount(amount)
	if feeAddr == "" || fee == 0 {
		return store.Transfer(from, to, amount)
	}
	if !store.Transfer(from, to, amount-fee) {
		return false
	}
	store.Transfer(from, feeAddr, fee)
	return true
}

// skimPlatformFee pulls the fee slice of amount back out of address
// AFTER address has already received amount in full - used where a
// trade settles in two legs that must both succeed before either is
// truly final (see settleHubTradeOnChain), so the fee can only safely
// be taken once there's no remaining refund path that would otherwise
// need to account for it. No-op if no fee address is configured.
func skimPlatformFee(store *core.TokenStore, address string, amount uint64) {
	feeAddr := os.Getenv("PLATFORM_FEE_ADDRESS")
	fee := core.PlatformFeeAmount(amount)
	if feeAddr == "" || fee == 0 {
		return
	}
	store.Transfer(address, feeAddr, fee)
}

// appliedTx guards against applying the same transaction twice - a
// transaction can legitimately be seen more than once (this node
// produced it locally AND received it again via gossip, or a peer
// resends a block we've already adopted).
var appliedTx = struct {
	mu   sync.Mutex
	seen map[string]bool
}{seen: make(map[string]bool)}

// appliedBlock guards against applying an entire block twice - e.g. a
// full-chain resync (syncChainFromPeer) walks every block again, and
// this node's own gossip can echo a block it already adopted.
var appliedBlock = struct {
	mu   sync.Mutex
	seen map[string]bool
}{seen: make(map[string]bool)}

// slashDedup guards against the same offense being punished twice -
// every node running the consensus loop independently notices the
// same missed round or double-sign and each submits its own report,
// but only the first one confirmed in a block should actually count.
var slashDedup = struct {
	mu   sync.Mutex
	seen map[string]bool
}{seen: make(map[string]bool)}

func slashAlreadyApplied(key string) bool {
	slashDedup.mu.Lock()
	defer slashDedup.mu.Unlock()

	if slashDedup.seen[key] {
		return true
	}
	slashDedup.seen[key] = true
	return false
}

// slashApplied/markSlashApplied split slashAlreadyApplied's combined
// test-and-set into separate peek and set steps - needed by the
// missed-block M-of-N corroboration path (missed_block_confirmation.go),
// which must check "already fully applied" repeatedly while
// accumulating reporters WITHOUT marking it applied until the real
// threshold is actually crossed. double_sign keeps using
// slashAlreadyApplied unchanged, since it applies on the first
// (independently cryptographically verified) report and never needs
// this two-step split.
func slashApplied(key string) bool {
	slashDedup.mu.Lock()
	defer slashDedup.mu.Unlock()
	return slashDedup.seen[key]
}

func markSlashApplied(key string) bool {
	slashDedup.mu.Lock()
	defer slashDedup.mu.Unlock()

	if slashDedup.seen[key] {
		return false
	}
	slashDedup.seen[key] = true
	return true
}

// snapshotAppliedTxIDs/restoreAppliedTxIDs (and the Block/Slash
// equivalents below) persist the dedup guards above across restarts.
// See persistence.go's persistedState doc comment for why this
// matters: without it, a restart-then-full-resync would replay and
// re-apply everything this node already applied in a previous
// process lifetime.
func snapshotAppliedTxIDs() []string {
	appliedTx.mu.Lock()
	defer appliedTx.mu.Unlock()

	ids := make([]string, 0, len(appliedTx.seen))
	for id := range appliedTx.seen {
		ids = append(ids, id)
	}
	return ids
}

func restoreAppliedTxIDs(ids []string) {
	appliedTx.mu.Lock()
	defer appliedTx.mu.Unlock()

	for _, id := range ids {
		appliedTx.seen[id] = true
	}
}

func snapshotAppliedBlockHashes() []string {
	appliedBlock.mu.Lock()
	defer appliedBlock.mu.Unlock()

	hashes := make([]string, 0, len(appliedBlock.seen))
	for h := range appliedBlock.seen {
		hashes = append(hashes, h)
	}
	return hashes
}

func restoreAppliedBlockHashes(hashes []string) {
	appliedBlock.mu.Lock()
	defer appliedBlock.mu.Unlock()

	for _, h := range hashes {
		appliedBlock.seen[h] = true
	}
}

func snapshotSlashDedup() []string {
	slashDedup.mu.Lock()
	defer slashDedup.mu.Unlock()

	keys := make([]string, 0, len(slashDedup.seen))
	for k := range slashDedup.seen {
		keys = append(keys, k)
	}
	return keys
}

func restoreSlashDedup(keys []string) {
	slashDedup.mu.Lock()
	defer slashDedup.mu.Unlock()

	for _, k := range keys {
		slashDedup.seen[k] = true
	}
}

// nonAddressValidators are placeholder "producer" strings that are
// not real wallet addresses, so applyBlockReward must not mint to them.
var nonAddressValidators = map[string]bool{
	"":                      true,
	"creditchain":           true, // genesis
	"creditchain-validator": true, // legacy /mine placeholder
}

// ApplyBlock is the single place every economic state change actually
// happens - both the transactions it carries AND the fixed reward for
// producing it. It runs identically whether this node produced the
// block itself or received it from a peer, which is what lets
// independent nodes converge on the same wallet/loan/staking state
// instead of only agreeing on the list of blocks. Rewarding the
// producer HERE (not inline where the block is mined) is what makes
// that reward replay identically everywhere the block goes.
func ApplyBlock(block core.Block) {
	appliedBlock.mu.Lock()
	if block.Hash != "" && appliedBlock.seen[block.Hash] {
		appliedBlock.mu.Unlock()
		return
	}
	if block.Hash != "" {
		appliedBlock.seen[block.Hash] = true
	}
	appliedBlock.mu.Unlock()

	for _, tx := range block.Transactions {
		applyTxOnce(tx)
	}

	applyBlockReward(block)

	// Driven by the block's own timestamp, not this node's wall
	// clock, so every node that applies this same block reaches the
	// identical set of matured unbonds - same determinism guarantee
	// as everything else in this function.
	for _, entry := range stakingStore.ProcessMaturedUnbonds(block.Timestamp) {
		creditMaturedUnbond(entry, block.Timestamp)
	}

	// Same block-timestamp-driven determinism, for matured validator
	// undelegations - see core.DelegationStore.ProcessMaturedUndelegations.
	for _, entry := range delegationStore.ProcessMaturedUndelegations(block.Timestamp) {
		tokenStore.Transfer(validatorDelegationEscrowAddress, entry.Delegator, entry.Amount)
	}

	// Same block-timestamp-driven determinism as everything else here
	// - "unlike Bitcoin, it can't be lost forever" (see
	// core/dormancy.go): wallets quiet long enough get swept and
	// redistributed to whoever's still active.
	processDormantWallets(block.Timestamp)

	// Same block-timestamp-driven determinism - peer-funded loans past
	// their due date get defaulted identically on every node.
	processLoanDefaults(block.Timestamp)

	// Same block-timestamp-driven determinism - OETH-collateralized
	// peer-funded loans whose collateral value has fallen too far
	// (regardless of due date) get liquidated identically on every
	// node. Runs after processLoanDefaults so a loan that's both
	// overdue AND under-collateralized is only ever handled once (the
	// due-date sweep already flipped its status away from FUNDED).
	processCollateralLiquidations(block.Timestamp)

	// Same block-timestamp-driven determinism - Final Redemption
	// windows (see advanceLoanLifecycle) that expired without full
	// repayment transfer Mining Power identically on every node. Runs
	// after the two sweeps above so a loan that just entered
	// AwaitingFinalRedemption this same block never gets swept here
	// too (its RiskStageDeadline is always in the future).
	processFinalRedemptions(block.Timestamp)

	// Same block-timestamp-driven determinism - P2P trade chats that
	// have stalled (either party's payment window ran out, or one
	// party acted and the other went silent for an hour) resolve
	// identically on every node.
	processStalledP2PSettlements(block.Timestamp)

	// Continuous Reputation Credit mining - see its own doc comment
	// (api/reputation_mining.go) for the epoch-gating rationale.
	processReputationMining(block)

	// Same block-timestamp-driven determinism as every sweep above -
	// proposals whose 7-day voting window has passed close identically
	// on every node, with identical PASSED/FAILED outcomes and (on
	// PASSED) identical REPT payouts/protocol-parameter effects.
	processProposalFinalization(block.Timestamp)

	// Keeps the governance feed "constantly active" even when no real
	// user has proposed anything lately - see processAutoProposal's
	// own doc comment.
	processAutoProposal(block)
}

// processDormantWallets sweeps every wallet that's gone dormant (see
// core.Wallet.IsDormant) and splits what it reclaims equally across
// every wallet that ISN'T dormant right now - "qualifying" simply
// means still active. This is the "unlike Bitcoin, it can't be lost
// forever" rule: staying active is both what keeps your own funds
// yours, and what makes you eligible for a share of anyone else's
// that went unclaimed.
func processDormantWallets(asOf int64) {
	var dormantAddrs []string
	var recipients []string

	for address, w := range walletStore.Wallets {
		if w.IsDormant(asOf) {
			dormantAddrs = append(dormantAddrs, address)
		}
		// Deliberately a separate check, not an else-if paired with
		// !IsDormant() - see QualifiesForAirdrop's doc comment for why
		// those two aren't interchangeable (an already-reclaimed wallet
		// reports IsDormant()==false forever, but that's not proof of
		// current activity).
		if w.QualifiesForAirdrop(asOf) {
			recipients = append(recipients, address)
		}
	}

	if len(dormantAddrs) == 0 || len(recipients) == 0 {
		return
	}
	// Map iteration order is randomized in Go - sorting here means
	// every node processes the exact same sequence, even though the
	// actual math below doesn't depend on order (equal split by count,
	// remainder always left behind rather than assigned to whichever
	// recipient happened to be visited last).
	sort.Strings(dormantAddrs)
	sort.Strings(recipients)

	for _, address := range dormantAddrs {
		reclaimDormantWallet(address, recipients, asOf)
	}
}

// reclaimDormantWallet actually sweeps one dormant wallet's
// reclaimable balance and splits it equally across recipients.
//
// Only CCT, REPT, and staked CCT/ETHTEST/USDCTEST are reclaimed - all
// four live at (or are ledger-keyed by) the wallet's own raw address.
// HELD (not staked) ETHTEST/USDCTEST balances live at a sub-address
// derived from the wallet's real public key - a key this server never
// stores for any wallet except the one currently making a request
// (see api/wallet_create.go's disclosed gap) - so those can't be swept
// automatically. This is the exact same limitation the wallet-recovery
// migration already discloses (see core/recovery.go), just with no one
// present to optionally supply the missing key the way a recovering
// user can.
func reclaimDormantWallet(address string, recipients []string, asOf int64) {
	n := uint64(len(recipients))

	sweepEqually := func(store *core.TokenStore) {
		total := store.BalanceOf(address)
		if total == 0 {
			return
		}
		share := total / n
		if share == 0 {
			return
		}
		for _, recipient := range recipients {
			store.Transfer(address, recipient, share)
		}
		// Any remainder from integer division is deliberately left
		// behind in the now-reclaimed wallet rather than assigned to
		// an arbitrary recipient - harmless dust, and avoids an
		// order-dependent tiebreak.
	}
	sweepEqually(tokenStore)
	sweepEqually(reptStore)

	sweepStakedEqually := func(symbol string) {
		total := stakingStore.StakeOf(address, symbol)
		if total == 0 {
			return
		}
		share := total / n

		// Escrowed tokens stay in their symbol's shared escrow (see
		// stakeEscrowAddress) - only the ledger entry recording who
		// they belong to needs to move, same as migrateWalletAssets
		// already does for wallet-recovery migration.
		stakingStore.Stakes[address][symbol] = 0

		// oETH is a receipt balance for staked ETHTEST (see
		// applyStake/creditMaturedUnbond) - moves alongside the stake
		// ledger entry itself, so a reclaimed wallet doesn't keep
		// phantom oETH pointing at stake it no longer owns, and
		// recipients' oETH stays true to the stake they now hold.
		if symbol == "ETHTEST" {
			w := walletStore.Get(address)
			oethTotal := w.OETHBalance
			w.OETHBalance = 0
			oethShare := oethTotal / n
			if oethShare > 0 {
				for _, recipient := range recipients {
					walletStore.Get(recipient).OETHBalance += oethShare
				}
			}
		}

		if share == 0 {
			return
		}
		for _, recipient := range recipients {
			if stakingStore.Stakes[recipient] == nil {
				stakingStore.Stakes[recipient] = make(map[string]uint64)
			}
			stakingStore.Stakes[recipient][symbol] += share
		}
	}
	sweepStakedEqually("CCT")
	sweepStakedEqually("ETHTEST")
	sweepStakedEqually("USDCTEST")

	stakingStore.SyncWallet(walletStore.Get(address), address)
	for _, recipient := range recipients {
		stakingStore.SyncWallet(walletStore.Get(recipient), recipient)
	}

	walletStore.Get(address).ReclaimedAt = asOf
}

// blockProductionReputationInterval/Growth is the "honest validators
// climb over time, not just get paid in Rewards" rule - +1 reputation
// every 10 blocks produced. This is the ONLY way a slashed validator's
// reputation can recover, since slashing only ever decreases it (see
// core/slashing.go) - without this, a validator that gets slashed once
// early on stays near the reputation floor forever, no matter how many
// honest blocks it produces afterward.
const (
	blockProductionReputationGrowth   = 1
	blockProductionReputationInterval = 10
)

// applyBlockReward pays out ProduceBlockReward for a produced block -
// 100% to the validator if nobody has delegated to them (identical to
// behavior before delegation pools existed), or split between the
// validator's own commission cut and a pro-rata share to every
// delegator (by their delegated amount / the pool's total) once they
// have a real pool behind them. See core.ValidatorStatus.CommissionBps
// and core.DelegationStore's doc comments for the full model.
// mintRept is the single call site every REPT tier-reward mint goes
// through - centralizes the "Minted REPT" notification alongside the
// actual mint instead of relying on every one of this file's several
// mint sites to remember it individually.
func mintRept(address string, amount uint64) {
	reptStore.Mint(address, amount)
	notifyUser(address, "REPT_MINTED", fmt.Sprintf("You minted %d REPT", amount), "")
}

func applyBlockReward(block core.Block) {
	if nonAddressValidators[block.Validator] {
		return
	}

	wallet := walletStore.Get(block.Validator)

	// Sweep this block's collected CCT transfer fees (see
	// cctTransferFeePoolAddress) straight to the validator that
	// produced it - a real value transfer from senders to the
	// validator, not a mint, and paid 100% to the validator directly
	// rather than split with delegators the way ProduceBlockReward is,
	// since it's compensation for including the transaction, not a
	// staking yield.
	if feeCollected := tokenStore.BalanceOf(cctTransferFeePoolAddress()); feeCollected > 0 {
		if tokenStore.Transfer(cctTransferFeePoolAddress(), block.Validator, feeCollected) {
			wallet.Rewards += feeCollected
		}
	}

	totalDelegated := delegationStore.TotalDelegatedTo(block.Validator)

	// PoSR's controlled-inflation decaying reward for the era this
	// block falls in (see blockRewardForTimestamp) - replaces the old
	// flat ProduceBlockReward constant, which now only serves as the
	// Year-1 baseline the decay schedule is built from. Every mint
	// below routes through mintCCTCapped, so this whole payout also
	// respects MaxTotalCCTSupply - once the cap is reached, minted
	// amounts (and therefore Rewards credited) correctly shrink toward
	// zero rather than the ledger silently exceeding the cap.
	reward := blockRewardForTimestamp(block.Timestamp)

	if totalDelegated == 0 {
		minted := mintCCTCapped(block.Validator, reward)
		wallet.Rewards += minted
	} else {
		status, _ := validatorStore.Get(block.Validator)
		var commissionBps uint64
		if status != nil {
			commissionBps = uint64(status.CommissionBps)
		}

		commission := reward * commissionBps / 10000
		poolShare := reward - commission

		mintedCommission := mintCCTCapped(block.Validator, commission)
		wallet.Rewards += mintedCommission

		// Map iteration order is randomized in Go - every node applying
		// this same block must reach an identical result, so delegators
		// are visited in a fixed (sorted) order, same convention as
		// processDormantWallets uses for exactly this reason. The actual
		// payout math doesn't depend on order, only which entries pick
		// up the paid-out remainder.
		delegators := make([]string, 0, len(delegationStore.Delegations[block.Validator]))
		for delegator := range delegationStore.Delegations[block.Validator] {
			delegators = append(delegators, delegator)
		}
		sort.Strings(delegators)

		var distributed uint64
		for _, delegator := range delegators {
			amount := delegationStore.Delegations[block.Validator][delegator]
			share := poolShare * amount / totalDelegated
			if share == 0 {
				continue
			}
			dWallet := walletStore.Get(delegator)
			mintedShare := mintCCTCapped(delegator, share)
			dWallet.Rewards += mintedShare
			distributed += share
		}

		// Integer-division remainder from the pro-rata split goes to the
		// validator rather than being lost or arbitrarily assigned to
		// whichever delegator sorts first - same "remainder stays
		// behind" rule reclaimDormantWallet already follows.
		if remainder := poolShare - distributed; remainder > 0 {
			mintedRemainder := mintCCTCapped(block.Validator, remainder)
			wallet.Rewards += mintedRemainder
		}
	}

	// BlocksProduced must also be counted here, not where the block is
	// mined - otherwise only the node that actually produced it locally
	// increments the counter, and every peer that just received it via
	// gossip/sync silently undercounts the same validator.
	blocksProduced := validatorStore.RecordBlock(block.Validator)

	// blocksProduced == 0 means RecordBlock found no registered
	// validator for this address (e.g. an old block from before
	// validator registration existed) - guard against that being
	// misread as "just hit a multiple of 10."
	if blocksProduced > 0 && blocksProduced%blockProductionReputationInterval == 0 {
		if reward := core.IncreaseWalletReputation(wallet, blockProductionReputationGrowth); reward > 0 {
			mintRept(block.Validator, reward)
		}
	}
}

func applyTxOnce(tx core.Transaction) {
	appliedTx.mu.Lock()
	if tx.ID != "" && appliedTx.seen[tx.ID] {
		appliedTx.mu.Unlock()
		return
	}
	if tx.ID != "" {
		appliedTx.seen[tx.ID] = true
	}
	appliedTx.mu.Unlock()

	if !verifyTxAuth(tx) {
		fmt.Println("rejected transaction with invalid/mismatched authentication:", tx.ID, tx.Type)
		return
	}

	// A migrated-away address is permanently frozen (see
	// core.RecoveryStore's doc comment) - even if its original private
	// key somehow later resurfaces, it can never transact again, since
	// its balances have already moved to the address that recovered it.
	if tx.AuthAddress != "" && recoveryStore.IsMigrated(tx.AuthAddress) {
		fmt.Println("rejected transaction from a migrated-away address:", tx.AuthAddress)
		return
	}

	// Any signed action proves this wallet's key is still live -
	// resets the dormancy clock (see core/dormancy.go). Uses the
	// transaction's own timestamp, not wall-clock, for the same
	// cross-node determinism every other block-driven state change in
	// this file relies on.
	if tx.AuthAddress != "" {
		walletStore.Get(tx.AuthAddress).LastActivityAt = tx.Timestamp
	}

	ApplyTransaction(tx)
}

// verifyTxAuth independently re-confirms a transaction really was
// authorized by the address it claims, before ApplyTransaction ever
// touches real state - every node runs this, not just the one that
// originally received the HTTP request, so a block producer can't
// just insert an arbitrary transaction into a block they legitimately
// produce and sign and have every other node blindly apply it.
//
// This is two checks, not one: core.VerifyTransactionAuth confirms
// SOME valid signature is attached (AuthAddress really did sign
// AuthPayload), but that alone isn't enough - it doesn't prove this
// Transaction's own From/To/Amount fields actually match what was
// signed. Without also checking that, a malicious producer could take
// a real, validly-signed envelope (their own, or one merely observed
// in the mempool/gossip stream) and change the Amount/To on the
// Transaction it gets wrapped in - the embedded signature would still
// "verify" (it was never checked against those fields), letting the
// producer inflate an amount or redirect a recipient while still
// looking authenticated. So every type below also confirms the
// authenticated actor and (where the apply* function trusts it
// directly, rather than re-deriving it from existing state like a
// loan record) the Amount match what was actually signed.
func verifyTxAuth(tx core.Transaction) bool {
	if err := core.VerifyTransactionAuth(tx); err != nil {
		return false
	}

	switch tx.Type {

	case core.TxLockCollateral:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed lockCollateralData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxLoanAddCollateral:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed loanAddCollateralData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxLoanRedeemShortfall:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed loanRedeemShortfallData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxStakeETH, core.TxUnstakeETH:
		if tx.AuthAddress != tx.From {
			return false
		}
		// Symbol is bound between what was signed and what the
		// transaction claims - same reasoning as TxExchange below -
		// without it a malicious producer could take a genuinely-signed
		// "stake 50 ETHTEST" and rewrite Data's symbol to redirect it
		// into staking CCT/USDCTEST instead. Data defaults to ETHTEST
		// rather than requiring an exact match against AuthPayload's raw
		// bytes, since transactions recorded before Symbol existed
		// always left Data empty - a fresh node replaying the historical
		// chain from genesis must still accept those exactly as they
		// were originally confirmed.
		var signed stakeData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if signed.Symbol == "" {
			signed.Symbol = "ETHTEST"
		}

		claimed := stakeData{Symbol: "ETHTEST"}
		if tx.Data != "" {
			if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
				return false
			}
		}

		if signed.Symbol != claimed.Symbol {
			return false
		}
		return signed.Amount == tx.Amount

	case core.TxExchange:
		if tx.AuthAddress != tx.From {
			return false
		}
		// Symbol is bound between what was signed and what the
		// transaction claims, same reasoning as every other
		// multi-field payload - without it, a malicious producer could
		// take a genuinely-signed "send 50 CCT" and rewrite Data's
		// symbol to redirect it into an ETHTEST/USDCTEST send instead.
		// Data defaults to CCT rather than requiring an exact match
		// against AuthPayload's raw bytes, since transactions recorded
		// before Symbol existed always left Data empty - a fresh node
		// replaying the historical chain from genesis must still
		// accept those exactly as they were originally confirmed.
		var signed tokenTransferData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if signed.Symbol == "" {
			signed.Symbol = "CCT"
		}

		claimed := tokenTransferData{Symbol: "CCT"}
		if tx.Data != "" {
			if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
				return false
			}
		}

		if signed.Symbol != claimed.Symbol {
			return false
		}
		return signed.To == tx.To && signed.Amount == tx.Amount

	case core.TxBridgeWithdraw:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed bridgeWithdrawData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		var claimed bridgeWithdrawData
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxBorrow:
		// The borrower is the authenticated actor - stored in To,
		// since From is left empty to represent the lending pool (see
		// api/borrow.go).
		if tx.AuthAddress != tx.To {
			return false
		}
		var payload struct{ Amount uint64 }
		if err := json.Unmarshal(tx.AuthPayload, &payload); err != nil {
			return false
		}
		return payload.Amount == tx.Amount

	case core.TxLend, core.TxRepay, core.TxGovernance, core.TxFaucetClaim, core.TxRatingSubmit,
		core.TxValidatorApplicationCreate, core.TxValidatorApplicationConfirm, core.TxYeneClaim:
		// No Amount is trusted directly from these transactions - the
		// apply* functions for these either re-derive the amount from
		// existing trusted state (the loan record itself for
		// Lend/Repay), carry none at all (Governance), mint a fixed
		// server-side constant regardless of anything the client sent
		// (FaucetClaim), mint a fixed 1 CCT gated on a first-ever good
		// rating (RatingSubmit), or mint a server-derived pseudo-random
		// amount gated on a cooldown (YeneClaim) - so only the actor's
		// identity needs binding here.
		return tx.AuthAddress == tx.From

	case core.TxValidatorRegister:
		if tx.AuthAddress != tx.From {
			return false
		}
		// The client must explicitly sign WHICH node it's delegating
		// to (see api/validators.go's HandleRegisterValidator) - a
		// malicious producer could otherwise take someone else's real
		// registration transaction and rewrite Data to redirect their
		// delegation to an attacker-controlled node. Both the payload
		// the wallet actually signed and the Data the transaction
		// claims must decode to the identical delegation.
		var signed, claimed validatorRegisterData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed.NodeID == claimed.NodeID && signed.NodePublicKey == claimed.NodePublicKey

	case core.TxValidatorDelegate:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed validatorDelegateData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed && signed.Amount == tx.Amount

	case core.TxValidatorUndelegate:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed validatorUndelegateData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed && signed.Amount == tx.Amount

	case core.TxValidatorSetCommission:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed validatorSetCommissionData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxTokenRegister:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed tokenRegisterData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxPoolAddLiquidity:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed poolAddLiquidityData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxPoolRemoveLiquidity:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed poolRemoveLiquidityData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxPoolSwap:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed poolSwapData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxSwapViaCCT:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed swapViaCCTData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxP2POfferCreate:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed p2pOfferCreateData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxP2POfferAccept, core.TxP2POfferCancel:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed p2pOfferActionData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxLoanOfferCreate:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed loanOfferCreateData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxLoanOfferDraw:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed loanOfferDrawData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxLoanOfferClose:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed loanOfferCloseData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxLoanRequestCreate:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed loanRequestCreateData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxLoanRequestCancel:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed loanRequestCancelData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	// TxLoanRequestFulfill is authored by the LENDER at negotiation-
	// confirm time (see HandleConfirmNegotiation's RequestID branch) -
	// unlike TxLoanOfferDraw, there's no pre-signed borrower envelope
	// to reuse, since the borrower's consent was already given, signed,
	// and block-confirmed when they created the LoanRequest itself. The
	// signed payload is just "I confirm this negotiation" -
	// applyLoanRequestFulfill looks up the real economic terms
	// (RequestID, RatePercent, Amount, Symbol, Lender, Borrower) from
	// the Negotiation record itself, which cannot be forged by the
	// confirming party since only the lender named on it can execute it.
	case core.TxLoanRequestFulfill:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed negotiationActionData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxModeratorRegister:
		// No other field to bind - registration carries no destination
		// or amount that a malicious producer could redirect, so
		// confirming the actor's own identity signed this is sufficient
		// (same reasoning as TxLend/TxRepay/TxGovernance above).
		return tx.AuthAddress == tx.From

	case core.TxModeratorConfirm:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed moderationConfirmData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxP2PExternalAccept:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed p2pExternalAcceptData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxP2PSellerConfirmPaid:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed p2pSellerConfirmData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxP2PSellerDispute:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed p2pSellerDisputeData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxP2PBuyerMarkPaid:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed p2pBuyerMarkPaidData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxP2PBuyerConfirmPaid:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed p2pBuyerConfirmData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxP2PModeratorResolve:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed p2pModeratorResolveData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxKYCSubmit:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed kycSubmitData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxCompanyApply:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed companyApplyData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxCompanyReview:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed companyReviewData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxCompanyReassignModerator:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed companyReassignData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxCompanyUpdateContact:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed companyUpdateContactData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxBuyRequestCreate:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed buyRequestCreateData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxBuyRequestFulfill, core.TxBuyRequestCancel:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed buyRequestActionData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxSetRecoveryEmail:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed recoveryEmailRequestData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxRecoverWallet:
		// The NEW address is the authenticated actor here - it's the
		// one signing, and the one that will receive the migrated
		// balance. It deliberately does NOT need to match the OLD
		// address embedded in the payload (that's the entire point of
		// a recovery flow - the old key is unavailable).
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed recoverWalletData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxHDWalletMigrate:
		// Unlike TxRecoverWallet, the OLD address is the authenticated
		// actor here - it's still-controlled, still-signing key is
		// exactly what makes this a migration rather than a recovery
		// (see core/transactions.go's doc comment).
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed hdWalletMigrateData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxHubRateSet:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed hubRateSetData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxHubTradeRequest:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed hubTradeRequestData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxHubRulesSet:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed hubRulesSetData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		// hubRulesSetData contains a map (PaymentWindowMinutes), which
		// isn't comparable with == - reflect.DeepEqual is the map-safe
		// equivalent of the same "signed payload must match applied
		// payload" check every other case here does.
		return reflect.DeepEqual(signed, claimed)

	case core.TxHubWorkerAdd:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed hubWorkerAddData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxHubWorkerRemove:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed hubWorkerRemoveData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxHubAutomationSubscribe:
		return tx.AuthAddress == tx.From

	case core.TxHubWorkerSetAutomation:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed hubWorkerSetAutomationData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxValidatorApplicationSupport:
		// Unlike Create/Confirm above, this payload's Address field
		// picks WHICH application receives the supporter's real
		// governance weight - worth binding so a tampering relay
		// can't redirect a legitimate supporter's weight to a
		// different application than they actually signed for.
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed validatorApplicationSupportData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxHubStorefrontSet:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed hubStorefrontSetData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxOrderPlace:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed orderPlaceData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxOrderCancel:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed orderCancelData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxSlashReport:
		// Node-authored, not wallet-authored - see
		// api/node_identity.go's selfSignedEnvelope. Data and
		// AuthPayload are set to the exact same report bytes at
		// submission time (see consensus_loop.go's reportMissedBlock /
		// peer.go's reportDoubleSign), so requiring them to still
		// match is a simple, general way to bind the report's actual
		// content to what the reporting node's identity signed,
		// without needing bespoke per-field comparison logic.
		return tx.Data == string(tx.AuthPayload)

	case core.TxTokenMint:
		// See verifyAdminTxAuth's doc comment: accepts either a real
		// signature from the configured admin wallet (independently
		// reverifiable by every node) or the legacy node-authored
		// shape kept only for the migration window.
		return verifyAdminTxAuth(tx)

	case core.TxBridgeDeposit:
		// Node-authored, not wallet-authored - same reasoning as
		// TxSlashReport above (api/eth_bridge.go's
		// pollAndReportBridgeDeposit is this report's only submitter).
		return tx.Data == string(tx.AuthPayload)

	case core.TxBridgeBtcDeposit:
		// Node-authored, not wallet-authored - same reasoning as
		// TxBridgeDeposit above (api/btc_bridge.go's
		// pollAndReportBtcDeposit is this report's only submitter).
		return tx.Data == string(tx.AuthPayload)

	case core.TxTreasuryPurchaseConfirm, core.TxTreasurySetSalePrice, core.TxTreasurySetPaused, core.TxTreasurySetAddress, core.TxAnnouncementCreate:
		// Admin-only - see verifyAdminTxAuth.
		return verifyAdminTxAuth(tx)

	case core.TxGenesisPoolAllocation, core.TxGenesisPoolWithdraw:
		// Admin-only - see verifyAdminTxAuth.
		return verifyAdminTxAuth(tx)

	case core.TxAdminTransfer:
		// Admin-only - see verifyAdminTxAuth. Deliberately does NOT
		// additionally check tx.From against any signature, since the
		// entire point of this transaction type is moving value out of
		// an address whose own key is unreachable.
		return verifyAdminTxAuth(tx)

	case core.TxAdminMultisigPropose, core.TxAdminMultisigApprove:
		// Ordinary wallet-signed - see verifyAdminMultisigTxAuth
		// (api/admin_multisig.go). Unlike every other admin-only case
		// above, this is NOT node-authored/self-signed and does NOT
		// funnel through verifyAdminTxAuth's single-address check -
		// any of the network's configured multisig signers may
		// propose or approve.
		return verifyAdminMultisigTxAuth(tx)

	case core.TxTreasuryPurchaseRequest:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed treasuryPurchaseRequestData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	default:
		return false
	}
}

// ApplyTransaction performs the actual state mutation for a
// confirmed transaction. Every mutating handler used to do this
// inline at request time; now they only validate and queue a
// self-describing Transaction, and this is where it actually takes
// effect - exactly once, at block-confirmation time.
func ApplyTransaction(tx core.Transaction) {
	switch tx.Type {

	case core.TxStakeETH:
		applyStake(tx)

	case core.TxBorrow:
		applyBorrow(tx)

	case core.TxRepay:
		applyRepay(tx)

	case core.TxLend:
		applyFundLoan(tx)

	case core.TxGovernance:
		applyGovernance(tx)

	case core.TxRatingSubmit:
		applyRatingSubmit(tx)

	case core.TxExchange:
		applyTransfer(tx)

	case core.TxValidatorRegister:
		applyValidatorRegister(tx)

	case core.TxValidatorDelegate:
		applyValidatorDelegate(tx)

	case core.TxValidatorUndelegate:
		applyValidatorUndelegate(tx)

	case core.TxValidatorSetCommission:
		applyValidatorSetCommission(tx)

	case core.TxLockCollateral:
		applyLockCollateral(tx)

	case core.TxLoanAddCollateral:
		applyLoanAddCollateral(tx)

	case core.TxLoanRedeemShortfall:
		applyLoanRedeemShortfall(tx)

	case core.TxOrderPlace:
		applyOrderPlace(tx)

	case core.TxOrderCancel:
		applyOrderCancel(tx)

	case core.TxSlashReport:
		applySlashReport(tx)

	case core.TxBridgeDeposit:
		applyBridgeDeposit(tx)

	case core.TxBridgeBtcDeposit:
		applyBridgeBtcDeposit(tx)

	case core.TxBridgeWithdraw:
		applyBridgeWithdraw(tx)

	case core.TxUnstakeETH:
		applyUnstake(tx)

	case core.TxTokenRegister:
		applyTokenRegister(tx)

	case core.TxPoolAddLiquidity:
		applyPoolAddLiquidity(tx)

	case core.TxPoolRemoveLiquidity:
		applyPoolRemoveLiquidity(tx)

	case core.TxPoolSwap:
		applyPoolSwap(tx)

	case core.TxSwapViaCCT:
		applySwapViaCCT(tx)

	case core.TxP2POfferCreate:
		applyP2POfferCreate(tx)

	case core.TxP2POfferAccept:
		applyP2POfferAccept(tx)

	case core.TxP2POfferCancel:
		applyP2POfferCancel(tx)

	case core.TxFaucetClaim:
		applyFaucetClaim(tx)

	case core.TxYeneClaim:
		applyYeneClaim(tx)

	case core.TxLoanOfferCreate:
		applyLoanOfferCreate(tx)

	case core.TxLoanOfferDraw:
		applyLoanOfferDraw(tx)

	case core.TxLoanOfferClose:
		applyLoanOfferClose(tx)

	case core.TxLoanRequestCreate:
		applyLoanRequestCreate(tx)

	case core.TxLoanRequestCancel:
		applyLoanRequestCancel(tx)

	case core.TxLoanRequestFulfill:
		applyLoanRequestFulfill(tx)

	case core.TxModeratorRegister:
		applyModeratorRegister(tx)

	case core.TxModeratorConfirm:
		applyModeratorConfirm(tx)

	case core.TxP2PExternalAccept:
		applyP2PExternalAccept(tx)

	case core.TxP2PSellerConfirmPaid:
		applyP2PSellerConfirmPaid(tx)

	case core.TxP2PSellerDispute:
		applyP2PSellerDispute(tx)

	case core.TxP2PBuyerMarkPaid:
		applyP2PBuyerMarkPaid(tx)

	case core.TxP2PBuyerConfirmPaid:
		applyP2PBuyerConfirmPaid(tx)

	case core.TxP2PModeratorResolve:
		applyP2PModeratorResolve(tx)

	case core.TxKYCSubmit:
		applyKYCSubmit(tx)

	case core.TxCompanyApply:
		applyCompanyApply(tx)

	case core.TxCompanyReview:
		applyCompanyReview(tx)

	case core.TxCompanyReassignModerator:
		applyCompanyReassignModerator(tx)

	case core.TxCompanyUpdateContact:
		applyCompanyUpdateContact(tx)

	case core.TxBuyRequestCreate:
		applyBuyRequestCreate(tx)

	case core.TxBuyRequestFulfill:
		applyBuyRequestFulfill(tx)

	case core.TxBuyRequestCancel:
		applyBuyRequestCancel(tx)

	case core.TxSetRecoveryEmail:
		applySetRecoveryEmail(tx)

	case core.TxRecoverWallet:
		applyRecoverWallet(tx)

	case core.TxHDWalletMigrate:
		applyHDWalletMigrate(tx)

	case core.TxHubRateSet:
		applyHubRateSet(tx)

	case core.TxHubTradeRequest:
		applyHubTradeRequest(tx)

	case core.TxHubRulesSet:
		applyHubRulesSet(tx)

	case core.TxHubWorkerAdd:
		applyHubWorkerAdd(tx)

	case core.TxHubWorkerRemove:
		applyHubWorkerRemove(tx)

	case core.TxHubAutomationSubscribe:
		applyHubAutomationSubscribe(tx)

	case core.TxHubWorkerSetAutomation:
		applyHubWorkerSetAutomation(tx)

	case core.TxValidatorApplicationCreate:
		applyValidatorApplicationCreate(tx)

	case core.TxValidatorApplicationSupport:
		applyValidatorApplicationSupport(tx)

	case core.TxValidatorApplicationConfirm:
		applyValidatorApplicationConfirm(tx)

	case core.TxHubStorefrontSet:
		applyHubStorefrontSet(tx)

	case core.TxTokenMint:
		applyTokenMint(tx)

	case core.TxTreasuryPurchaseRequest:
		applyTreasuryPurchaseRequest(tx)

	case core.TxTreasuryPurchaseConfirm:
		applyTreasuryPurchaseConfirm(tx)

	case core.TxTreasurySetSalePrice:
		applyTreasurySetSalePrice(tx)

	case core.TxTreasurySetPaused:
		applyTreasurySetPaused(tx)

	case core.TxTreasurySetAddress:
		applyTreasurySetAddress(tx)

	case core.TxGenesisPoolAllocation:
		applyGenesisPoolAllocation(tx)

	case core.TxGenesisPoolWithdraw:
		applyGenesisPoolWithdraw(tx)

	case core.TxAdminTransfer:
		applyAdminTransfer(tx)

	case core.TxAnnouncementCreate:
		applyAnnouncementCreate(tx)

	case core.TxAdminMultisigPropose:
		applyAdminMultisigPropose(tx)

	case core.TxAdminMultisigApprove:
		applyAdminMultisigApprove(tx)
	}
}

// applyTokenMint replicates an admin-authorized testnet mint to every
// node identically - see HandleTokenMint's doc comment for why this
// can no longer just mutate a token store directly in the HTTP
// handler.
func applyTokenMint(tx core.Transaction) {
	var data tokenMintData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.Amount == 0 {
		return
	}

	if data.Symbol == "" || data.Symbol == "CCT" {
		mintCCTCapped(data.Address, data.Amount)
		return
	}

	store, err := resolveTokenStore(data.Symbol)
	if err != nil {
		return
	}
	store.Mint(data.Address, data.Amount)
}

// applyTreasuryPurchaseRequest records a buyer's intent to buy CCT
// from the Foundation Treasury - no funds move yet, see
// applyTreasuryPurchaseConfirm for the actual transfer.
func applyTreasuryPurchaseRequest(tx core.Transaction) {
	var data treasuryPurchaseRequestData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.CCTAmount == 0 {
		return
	}
	// Re-checked here, not just at submission time - state may have
	// changed between the two.
	if treasuryStore.Treasury.Paused {
		return
	}

	treasuryStore.CreatePurchase(&core.PurchaseRequest{
		ID:             tx.ID,
		BuyerAddress:   tx.From,
		BuyerPublicKey: tx.AuthPublicKey,
		CCTAmount:      data.CCTAmount,
		PriceAtRequest: treasuryStore.Treasury.SalePriceCentsPerCCT,
		Status:         core.PurchaseAwaitingPayment,
		CreatedAt:      tx.Timestamp,
	})
}

type treasuryPurchaseConfirmData struct {
	PurchaseID string `json:"purchaseId"`
}

// applyTreasuryPurchaseConfirm is the one Treasury transaction that
// actually moves value - a pure ledger transfer from the Foundation's
// address to the buyer, applied identically by every node once this
// admin attestation is confirmed in a block (see HandleTokenMint's
// doc comment for why this can't just mutate tokenStore directly in
// the HTTP handler). A failed transfer (insufficient treasury
// balance) is a silent no-op, the same convention TokenStore.Transfer
// already uses everywhere else - the purchase stays AWAITING_PAYMENT
// rather than erroring, a known rough edge for this phase (the admin
// should check /treasury/info before confirming).
func applyTreasuryPurchaseConfirm(tx core.Transaction) {
	var data treasuryPurchaseConfirmData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	purchase, ok := treasuryStore.GetPurchase(data.PurchaseID)
	if !ok || purchase.Status != core.PurchaseAwaitingPayment {
		return
	}

	if !tokenStore.Transfer(treasuryStore.Treasury.FoundationAddress, purchase.BuyerAddress, purchase.CCTAmount) {
		return
	}

	if confirmed, ok := treasuryStore.ConfirmPurchase(purchase.ID); ok {
		confirmed.ConfirmedAt = tx.Timestamp
	}
}

type treasurySetSalePriceData struct {
	SalePriceCentsPerCCT uint64 `json:"salePriceCentsPerCCT"`
}

func applyTreasurySetSalePrice(tx core.Transaction) {
	var data treasurySetSalePriceData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	treasuryStore.SetSalePrice(data.SalePriceCentsPerCCT)
}

type treasurySetPausedData struct {
	Paused bool `json:"paused"`
}

func applyTreasurySetPaused(tx core.Transaction) {
	var data treasurySetPausedData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	treasuryStore.SetPaused(data.Paused)
}

type treasurySetAddressData struct {
	FoundationAddress string `json:"foundationAddress"`
}

func applyTreasurySetAddress(tx core.Transaction) {
	var data treasurySetAddressData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.FoundationAddress == "" {
		return
	}
	treasuryStore.SetFoundationAddress(data.FoundationAddress)
}

// poolAddress is a synthetic, non-signable "wallet" that holds
// whatever CCT/token balance is currently sitting in a given symbol's
// liquidity pool - the same trick applyBorrow already uses (an empty
// From representing "the lending pool") extended to a per-symbol
// address so each pool's escrowed balance is visibly its own, never
// commingled with another pool's.
func poolAddress(symbol string) string {
	return "POOL:" + symbol
}

func applyTokenRegister(tx core.Transaction) {
	var data tokenRegisterData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	// Stake may have moved (e.g. unstaked) between submission and
	// confirmation - re-checked here the same way hasCollateral is
	// re-checked in applyBorrow, rather than trusting the
	// submission-time check alone. Deliberately ETHTEST-specific (not
	// "any staked token") - this gate predates CCT/USDCTEST staking and
	// broadening it is a separate product decision this change doesn't
	// make on its own.
	if stakingStore.StakeOf(tx.From, "ETHTEST") == 0 {
		return
	}
	if !core.ValidateTokenSymbol(data.Symbol) || !core.ValidateTokenName(data.Name) {
		return
	}
	if data.InitialSupply == 0 || data.InitialSupply > core.MaxCustomTokenSupply {
		return
	}
	if data.Standard == "" {
		data.Standard = "C1"
	}
	if !tokenStandardRegistry.IsCreatable(data.Standard) {
		return
	}

	// The ETHTEST fee may also have moved between submission and
	// confirmation - re-checked and only actually deducted here, never
	// trusted from submission time alone.
	ethTestSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, "ETHTEST")
	if !ethTestStore.Transfer(ethTestSide, tokenCreationFeeAddress(), TokenCreationFeeETHTest) {
		return
	}

	// Register itself is a no-op if the symbol was already taken by
	// another registration that landed first - re-checked here since
	// two registrations for the same symbol could both have looked
	// valid at their own submission time. The fee is still spent even
	// in that case (paid to enter the race, not a refundable deposit) -
	// consistent with every other apply* function never refunding a
	// race loser's fee/gas-equivalent cost.
	tokenRegistry.Register(data.Symbol, data.Name, tx.From, data.InitialSupply, data.Standard, data.PegReference)
}

func applyPoolAddLiquidity(tx core.Transaction) {
	var data poolAddLiquidityData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	store, pool, err := lookupCustomToken(data.Symbol)
	if err != nil {
		return
	}

	if data.CCTAmount == 0 || data.TokenAmount == 0 {
		return
	}
	if pool.TotalShares > 0 && !ratioMatches(data.CCTAmount, data.TokenAmount, pool.CCTReserve, pool.TokenReserve) {
		return
	}

	// The CCT side always moves on the CCT address; the token side
	// moves on whichever address ACTUALLY holds that symbol's balance -
	// the wallet's own CCT address for a custom/registered token, or
	// the derived sub-address for ETHTEST/USDCTEST (see
	// resolveBalanceAddress). LP shares stay tracked under the CCT
	// address either way - a share is a receipt on the pool membership,
	// not itself an ETHTEST/USDCTEST balance.
	tokenSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, data.Symbol)

	escrow := poolAddress(data.Symbol)

	if !tokenStore.Transfer(tx.From, escrow, data.CCTAmount) {
		return
	}
	if !store.Transfer(tokenSide, escrow, data.TokenAmount) {
		// Refund the CCT side - this deposit isn't going through.
		tokenStore.Transfer(escrow, tx.From, data.CCTAmount)
		return
	}

	pool.AddLiquidity(tx.From, data.CCTAmount, data.TokenAmount)
	recordPoolPrice(data.Symbol, pool, tx.Timestamp)
}

func applyPoolRemoveLiquidity(tx core.Transaction) {
	var data poolRemoveLiquidityData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	store, pool, err := lookupCustomToken(data.Symbol)
	if err != nil {
		return
	}

	cctOut, tokenOut, ok := pool.RemoveLiquidity(tx.From, data.Shares)
	if !ok {
		return
	}

	tokenSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, data.Symbol)

	escrow := poolAddress(data.Symbol)
	tokenStore.Transfer(escrow, tx.From, cctOut)
	store.Transfer(escrow, tokenSide, tokenOut)
	recordPoolPrice(data.Symbol, pool, tx.Timestamp)
}

// recordPoolPrice appends a price-history sample for symbol from
// pool's current reserves - called after every successful reserve
// mutation (swap, add/remove liquidity) so the chart reflects real
// trading activity regardless of which action moved the price.
func recordPoolPrice(symbol string, pool *core.LiquidityPool, timestamp int64) {
	priceHistoryStore.Record(symbol, pool, timestamp)
}

func applyPoolSwap(tx core.Transaction) {
	var data poolSwapData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	store, pool, err := lookupCustomToken(data.Symbol)
	if err != nil {
		return
	}
	escrow := poolAddress(data.Symbol)
	tokenSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, data.Symbol)

	switch data.Direction {
	case "cct_to_token":
		if !tokenStore.Transfer(tx.From, escrow, data.AmountIn) {
			return
		}
		amountOut, ok := pool.SwapCCTForToken(data.AmountIn)
		if !ok {
			tokenStore.Transfer(escrow, tx.From, data.AmountIn)
			return
		}
		store.Transfer(escrow, tokenSide, amountOut)
		recordPoolPrice(data.Symbol, pool, tx.Timestamp)

	case "token_to_cct":
		if !store.Transfer(tokenSide, escrow, data.AmountIn) {
			return
		}
		amountOut, ok := pool.SwapTokenForCCT(data.AmountIn)
		if !ok {
			store.Transfer(escrow, tokenSide, data.AmountIn)
			return
		}
		tokenStore.Transfer(escrow, tx.From, amountOut)
		recordPoolPrice(data.Symbol, pool, tx.Timestamp)
	}
}

// applySwapViaCCT is the universal swap - any resolvable token to any
// other, always routed through CCT. A single hop if either side
// already IS CCT (identical to applyPoolSwap's two directions, just
// dispatched from one generic entry point), two hops otherwise.
func applySwapViaCCT(tx core.Transaction) {
	var data swapViaCCTData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	if data.AmountIn == 0 || data.FromSymbol == data.ToSymbol {
		return
	}

	switch {
	case data.FromSymbol == "CCT":
		applySwapCCTToToken(tx.From, tx.AuthPublicKey, data.ToSymbol, data.AmountIn, tx.Timestamp)

	case data.ToSymbol == "CCT":
		applySwapTokenToCCT(tx.From, tx.AuthPublicKey, data.FromSymbol, data.AmountIn, tx.Timestamp)

	default:
		applySwapTokenToToken(tx.From, tx.AuthPublicKey, data.FromSymbol, data.ToSymbol, data.AmountIn, tx.Timestamp)
	}
}

func applySwapCCTToToken(from, publicKey, toSymbol string, amountIn uint64, timestamp int64) {
	toStore, toPool, err := lookupCustomToken(toSymbol)
	if err != nil {
		return
	}
	toSide := resolveBalanceAddress(from, publicKey, toSymbol)
	escrow := poolAddress(toSymbol)

	if !tokenStore.Transfer(from, escrow, amountIn) {
		return
	}
	amountOut, ok := toPool.SwapCCTForToken(amountIn)
	if !ok {
		tokenStore.Transfer(escrow, from, amountIn)
		return
	}
	toStore.Transfer(escrow, toSide, amountOut)
	recordPoolPrice(toSymbol, toPool, timestamp)
}

func applySwapTokenToCCT(from, publicKey, fromSymbol string, amountIn uint64, timestamp int64) {
	fromStore, fromPool, err := lookupCustomToken(fromSymbol)
	if err != nil {
		return
	}
	fromSide := resolveBalanceAddress(from, publicKey, fromSymbol)
	escrow := poolAddress(fromSymbol)

	if !fromStore.Transfer(fromSide, escrow, amountIn) {
		return
	}
	amountOut, ok := fromPool.SwapTokenForCCT(amountIn)
	if !ok {
		fromStore.Transfer(escrow, fromSide, amountIn)
		return
	}
	tokenStore.Transfer(escrow, from, amountOut)
	recordPoolPrice(fromSymbol, fromPool, timestamp)
}

// applySwapTokenToToken routes fromSymbol -> CCT -> toSymbol through
// both pools' escrow accounts. Both legs are QUOTED first (pure
// calculation, no state mutation) so the whole swap either goes
// through or is rejected outright - never left half-applied with the
// user's input gone but no output credited because the second leg
// turned out to lack liquidity.
func applySwapTokenToToken(from, publicKey, fromSymbol, toSymbol string, amountIn uint64, timestamp int64) {
	fromStore, fromPool, err := lookupCustomToken(fromSymbol)
	if err != nil {
		return
	}
	toStore, toPool, err := lookupCustomToken(toSymbol)
	if err != nil {
		return
	}

	quotedCCT, ok := fromPool.QuoteTokenForCCT(amountIn)
	if !ok {
		return
	}
	if _, ok := toPool.QuoteCCTForToken(quotedCCT); !ok {
		return
	}

	fromSide := resolveBalanceAddress(from, publicKey, fromSymbol)
	toSide := resolveBalanceAddress(from, publicKey, toSymbol)

	fromEscrow := poolAddress(fromSymbol)
	toEscrow := poolAddress(toSymbol)

	if !fromStore.Transfer(fromSide, fromEscrow, amountIn) {
		return
	}

	intermediateCCT, _ := fromPool.SwapTokenForCCT(amountIn)
	tokenStore.Transfer(fromEscrow, toEscrow, intermediateCCT)
	finalAmountOut, _ := toPool.SwapCCTForToken(intermediateCCT)
	toStore.Transfer(toEscrow, toSide, finalAmountOut)
	recordPoolPrice(fromSymbol, fromPool, timestamp)
	recordPoolPrice(toSymbol, toPool, timestamp)
}

// ReputationDeltaP2PTrade is the reputation reward BOTH parties earn
// when a P2P offer is successfully accepted - a completed trade is a
// mutual accomplishment (unlike a loan, where only the borrower
// "achieves" repayment), so both sides get it. Set 40% above the
// closest existing precedent for a genuinely completed exchange
// (ReputationDeltaLoanRepaidOnTime, 10 - see core/credit_engine.go) -
// a disclosed judgment call, not a locked spec value.
const ReputationDeltaP2PTrade = 14 // 10 * 1.4, rounded down

// ReputationDeltaP2PFraud is the penalty applied to whichever side of
// an EXTERNAL P2P trade a moderator's dispute resolution finds to
// have acted in bad faith (falsely claimed payment, or falsely denied
// receiving it). Same magnitude class as core.ReputationDeltaLoanDefault
// (30) - both represent a moderator/system-confirmed bad-faith act,
// not a minor slip.
const ReputationDeltaP2PFraud = 30

func applyP2POfferCreate(tx core.Transaction) {
	var data p2pOfferCreateData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	if data.OfferAmount == 0 || data.WantAmount == 0 || data.OfferSymbol == data.WantSymbol {
		return
	}

	offerStore, err := resolveTokenStore(data.OfferSymbol)
	if err != nil {
		return
	}

	mode := core.PaymentMode(data.PaymentMode)
	if mode == "" {
		mode = core.PaymentModeOnChain
	}
	if mode != core.PaymentModeOnChain && mode != core.PaymentModeExternal {
		return
	}
	// EXTERNAL's "want" side is never a real token (see
	// HandleCreateP2POffer's doc comment) - only re-validated as one
	// for ONCHAIN, same re-check-at-apply-time reasoning as every
	// other fast-fail check in this file.
	if mode == core.PaymentModeOnChain {
		if _, err := resolveTokenStore(data.WantSymbol); err != nil {
			return
		}
	} else if len(data.WantSymbol) == 0 || len(data.WantSymbol) > 20 {
		return
	}

	// Verification may have changed (e.g. rejected) between submission
	// and confirmation - re-checked here, not just at submission time.
	// Every offer (any mode, any token) requires a verified Trading Hub
	// - see HandleCreateP2POffer's doc comment.
	if !companyStore.IsVerified(tx.From) {
		return
	}

	// The transaction's own ID is already unique (see
	// core.newTxID) - reusing it directly as the offer ID avoids
	// needing a second ID scheme.
	offerID := tx.ID
	escrow := p2pOfferEscrowAddress(offerID)

	offerSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, data.OfferSymbol)

	if !offerStore.Transfer(offerSide, escrow, data.OfferAmount) {
		return
	}

	p2pOfferStore.Create(&core.P2POffer{
		ID:               offerID,
		Creator:          tx.From,
		CreatorPublicKey: tx.AuthPublicKey,
		OfferSymbol:      data.OfferSymbol,
		OfferAmount:      data.OfferAmount,
		WantSymbol:       data.WantSymbol,
		WantAmount:       data.WantAmount,
		PaymentMode:      mode,
		Status:           core.OfferOpen,
		CreatedAt:        tx.Timestamp,
		// Assigned here (block-confirmation time), not in the HTTP
		// handler, so every node applying this same transaction reaches
		// the identical round-robin assignment (see
		// core.ModeratorStore.AssignNext's doc comment).
		ModeratorAddress: moderatorStore.AssignNext(),
		ModerationStatus: core.ModerationPending,
	})
}

func applyP2POfferAccept(tx core.Transaction) {
	var data p2pOfferActionData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	offer, ok := p2pOfferStore.Get(data.OfferID)
	if !ok || offer.Status != core.OfferOpen || offer.Creator == tx.From {
		return
	}

	offerStore, err := resolveTokenStore(offer.OfferSymbol)
	if err != nil {
		return
	}
	wantStore, err := resolveTokenStore(offer.WantSymbol)
	if err != nil {
		return
	}

	// Each side's balance moves on whichever address actually holds
	// that symbol - the acceptor's/creator's own CCT address for a
	// custom/registered token, or their derived ETHTEST/USDCTEST
	// sub-address (see resolveBalanceAddress). The creator's sub-address
	// is re-derived from CreatorPublicKey, captured back when they
	// created the offer - this transaction only authenticates the
	// acceptor, not the creator.
	acceptorWantSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, offer.WantSymbol)
	creatorWantSide := resolveBalanceAddress(offer.Creator, offer.CreatorPublicKey, offer.WantSymbol)
	acceptorOfferSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, offer.OfferSymbol)

	// Move the acceptor's side to the creator first, in FULL - if they
	// don't actually have it, nothing else happens and the offer stays
	// open for someone else to accept. The platform fee is deliberately
	// NOT taken here: this move is still reversible (see the refund
	// below), and skimming a fee before knowing the trade will actually
	// go through would make that refund short the acceptor by the fee
	// amount for a trade that never completed.
	if !wantStore.Transfer(acceptorWantSide, creatorWantSide, offer.WantAmount) {
		return
	}

	if _, ok := p2pOfferStore.Accept(offer.ID, tx.From); !ok {
		// Someone else's accept for this same offer landed first
		// (e.g. two accept transactions for the same offer in the
		// same block) - refund the acceptor's side in full immediately
		// rather than leaving it stuck with the creator for nothing.
		wantStore.Transfer(creatorWantSide, acceptorWantSide, offer.WantAmount)
		return
	}

	// From here the trade is final - now the platform fee is taken,
	// retroactively on the leg that already landed (skimmed out of
	// what the creator just received) and on the escrow release below.
	skimPlatformFee(wantStore, creatorWantSide, offer.WantAmount)
	applyPlatformFee(offerStore, p2pOfferEscrowAddress(offer.ID), acceptorOfferSide, offer.OfferAmount)

	if reward := core.IncreaseWalletReputation(walletStore.Get(tx.From), ReputationDeltaP2PTrade); reward > 0 {
		mintRept(tx.From, reward)
	}
	if reward := core.IncreaseWalletReputation(walletStore.Get(offer.Creator), ReputationDeltaP2PTrade); reward > 0 {
		mintRept(offer.Creator, reward)
	}
	// Both parties earn the same amount for a P2P trade actually
	// completing - recorded against the acceptor (tx.From) here since
	// that's this transaction's own authenticated actor; the creator's
	// identical gain is described in the explorer's summary text
	// instead of a second event record (see buildTransactionSummary).
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: tx.From, Delta: int64(ReputationDeltaP2PTrade),
		Reason: "P2P trade completed", Timestamp: tx.Timestamp,
	})
}

func applyP2POfferCancel(tx core.Transaction) {
	var data p2pOfferActionData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	offer, ok := p2pOfferStore.Get(data.OfferID)
	if !ok || offer.Creator != tx.From {
		return
	}

	if _, ok := p2pOfferStore.Cancel(offer.ID); !ok {
		return
	}

	offerStore, err := resolveTokenStore(offer.OfferSymbol)
	if err != nil {
		return
	}

	creatorOfferSide := resolveBalanceAddress(offer.Creator, offer.CreatorPublicKey, offer.OfferSymbol)
	offerStore.Transfer(p2pOfferEscrowAddress(offer.ID), creatorOfferSide, offer.OfferAmount)
}

func applyP2PExternalAccept(tx core.Transaction) {
	var data p2pExternalAcceptData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	offer, ok := p2pOfferStore.Get(data.OfferID)
	if !ok || offer.Creator == tx.From {
		return
	}

	p2pOfferStore.BeginExternalSettlement(offer.ID, tx.From, tx.AuthPublicKey, tx.Timestamp)
}

func applyP2PSellerConfirmPaid(tx core.Transaction) {
	var data p2pSellerConfirmData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	offer, ok := p2pOfferStore.Get(data.OfferID)
	if !ok {
		return
	}

	// Re-verified here, not trusted from submission time: tx.From is
	// either the seller's own address, or an authorized automation
	// worker acting within its hub-set cap (see
	// api/hub_automation.go's doc comment) - core.P2POfferStore's
	// ConfirmSellerPaid always expects the real Creator address, so a
	// worker's confirmation is recorded as the hub's own action, with
	// tx.From (the worker's real signing identity) preserved only in
	// the transaction record itself for audit purposes.
	actingAsHub := tx.From
	if tx.From != offer.Creator {
		if !hubWorkerStore.CanAutoConfirm(offer.Creator, tx.From, offer.OfferAmount) {
			return
		}
		actingAsHub = offer.Creator
	}

	if !p2pOfferStore.ConfirmSellerPaid(data.OfferID, actingAsHub, tx.Timestamp) {
		return
	}

	// ConfirmSellerPaid now closes the trade itself (see its doc
	// comment) - release escrow to the buyer immediately, same fund
	// movement the buyer's own agree-confirmation used to trigger.
	releaseEscrowToBuyer(offer, offer.AcceptedBy, offer.AcceptedByPublicKey)
}

func applyP2PSellerDispute(tx core.Transaction) {
	var data p2pSellerDisputeData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	p2pOfferStore.DisputeAwaitingPayment(data.OfferID, tx.From)
}

func applyP2PBuyerMarkPaid(tx core.Transaction) {
	var data p2pBuyerMarkPaidData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	p2pOfferStore.MarkBuyerPaid(data.OfferID, tx.From, tx.Timestamp)
}

// releaseEscrowToBuyer moves an EXTERNAL offer's locked OfferAmount to
// the buyer - shared by the buyer's own agree-confirmation and the
// moderator's dispute resolution, since both end the same way once
// the trade is decided in the buyer's favor.
func releaseEscrowToBuyer(offer *core.P2POffer, buyerAddress, buyerPublicKey string) {
	offerStore, err := resolveTokenStore(offer.OfferSymbol)
	if err != nil {
		return
	}

	buyerSide := resolveBalanceAddress(buyerAddress, buyerPublicKey, offer.OfferSymbol)
	applyPlatformFee(offerStore, p2pOfferEscrowAddress(offer.ID), buyerSide, offer.OfferAmount)
}

func applyP2PBuyerConfirmPaid(tx core.Transaction) {
	var data p2pBuyerConfirmData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	offer, ok := p2pOfferStore.ConfirmBuyer(data.OfferID, tx.From, data.Agree)
	if !ok {
		return
	}

	if data.Agree {
		releaseEscrowToBuyer(offer, tx.From, tx.AuthPublicKey)
	}
	// A disagreement (data.Agree == false) moves no funds - it just
	// opens the dispute for the assigned moderator to resolve (see
	// applyP2PModeratorResolve).
}

func applyP2PModeratorResolve(tx core.Transaction) {
	var data p2pModeratorResolveData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	offer, ok := p2pOfferStore.ResolveDispute(data.OfferID, tx.From, data.Release)
	if !ok {
		return
	}

	if data.Release {
		// Moderator sided with the buyer - the buyer really paid and
		// the seller wrongly disputed/failed to confirm. Penalize the
		// seller (Creator), the party found to be acting in bad faith.
		releaseEscrowToBuyer(offer, offer.AcceptedBy, offer.AcceptedByPublicKey)
		core.DecreaseWalletReputation(walletStore.Get(offer.Creator), ReputationDeltaP2PFraud)
		reputationEventStore.Record(&core.ReputationEvent{
			TxID: tx.ID, Target: offer.Creator, Delta: -int64(ReputationDeltaP2PFraud),
			Reason: "Moderator found this trade claim to be false", Timestamp: tx.Timestamp,
		})
		return
	}

	// Moderator sided with the seller - the buyer's payment claim was
	// false. Penalize the buyer (AcceptedBy).
	refundEscrowToSeller(offer)
	core.DecreaseWalletReputation(walletStore.Get(offer.AcceptedBy), ReputationDeltaP2PFraud)
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: offer.AcceptedBy, Delta: -int64(ReputationDeltaP2PFraud),
		Reason: "Moderator found this trade claim to be false", Timestamp: tx.Timestamp,
	})
}

// refundEscrowToSeller moves an EXTERNAL offer's locked OfferAmount
// back to the seller (Creator) - shared by the moderator's
// side-with-the-seller dispute resolution and by
// processStalledP2PSettlements' EXPIRED case, since both end the
// trade the same way: the buyer never (successfully) completed
// off-app payment, so the escrow goes back to whoever put it up.
func refundEscrowToSeller(offer *core.P2POffer) {
	offerStore, err := resolveTokenStore(offer.OfferSymbol)
	if err != nil {
		return
	}
	sellerSide := resolveBalanceAddress(offer.Creator, offer.CreatorPublicKey, offer.OfferSymbol)
	offerStore.Transfer(p2pOfferEscrowAddress(offer.ID), sellerSide, offer.OfferAmount)
}

// p2pOfferHubAndDirection figures out which side of an EXTERNAL trade
// (if either) is the verified Trading Hub, and from which direction -
// mirrors website/js/p2p.js's offerHubAndDirection exactly, since both
// need to agree on which hub's HubRules.PaymentWindowFor applies to a
// given offer. Every general P2POffer's Creator is required to be a
// verified hub at creation time (see applyP2POfferCreate), but the hub
// rate/escrow flow (applyHubTradeRequest) can put the hub on either
// side depending on SELL_TO_HUB vs BUY_FROM_HUB - so both are checked.
func p2pOfferHubAndDirection(o *core.P2POffer) (hub, direction string) {
	if companyStore.IsVerified(o.Creator) {
		return o.Creator, "BUY_FROM_HUB"
	}
	if companyStore.IsVerified(o.AcceptedBy) {
		return o.AcceptedBy, "SELL_TO_HUB"
	}
	return "", ""
}

// processStalledP2PSettlements is the trade-chat countdown timer's
// actual enforcement - driven by the block's own timestamp (not this
// node's wall clock), same determinism guarantee as processLoanDefaults
// right below it, so every node resolves the exact same set of
// trades the exact same way. Two outcomes (see
// core.P2POfferStore.SweepStalledSettlements for the three stall
// conditions that produce them):
//
//   - EXPIRED (ReleaseToBuyer=false): neither party acted at all
//     within the hub's payment window - escrow refunded to the
//     seller, nothing was ever paid so there's nothing for the buyer
//     to receive.
//   - AUTO_RELEASED (ReleaseToBuyer=true): one party acted (buyer
//     marked paid, or seller confirmed) and the other went silent for
//     a full hour - escrow released to the buyer, same fund movement
//     as an explicit ConfirmBuyer(agree=true).
func processStalledP2PSettlements(asOf int64) {
	stalled := p2pOfferStore.SweepStalledSettlements(asOf, func(o *core.P2POffer) int {
		hub, direction := p2pOfferHubAndDirection(o)
		if hub == "" {
			return core.DefaultPaymentWindowMinutes
		}
		rules, _ := hubRulesStore.Get(hub)
		return rules.PaymentWindowFor(direction)
	})

	for _, s := range stalled {
		offer := s.Offer
		if s.ReleaseToBuyer {
			releaseEscrowToBuyer(offer, offer.AcceptedBy, offer.AcceptedByPublicKey)

			notifyUser(offer.Creator, "P2P_TRADE_AUTO_RELEASED", fmt.Sprintf("Trade %s auto-closed after an hour with no response - your escrowed %d %s was released to the buyer", offer.ID, offer.OfferAmount, offer.OfferSymbol), offer.ID)
			if offer.AcceptedBy != "" {
				notifyUser(offer.AcceptedBy, "P2P_TRADE_AUTO_RELEASED", fmt.Sprintf("Trade %s auto-closed after an hour with no response from the seller - you received the escrowed %d %s", offer.ID, offer.OfferAmount, offer.OfferSymbol), offer.ID)
			}
			continue
		}

		refundEscrowToSeller(offer)

		notifyUser(offer.Creator, "P2P_TRADE_EXPIRED", fmt.Sprintf("Trade %s expired - the buyer didn't complete payment in time, your escrowed %d %s was returned to you", offer.ID, offer.OfferAmount, offer.OfferSymbol), offer.ID)
		if offer.AcceptedBy != "" {
			notifyUser(offer.AcceptedBy, "P2P_TRADE_EXPIRED", fmt.Sprintf("Trade %s expired - the payment window ran out, no funds were exchanged", offer.ID), offer.ID)
		}
	}
}

// applyLockCollateral moves data.Amount out of the caller's STAKED CCT
// position or oETH receipt balance into collateralStore - re-checked
// here against current balances, not trusted from submission time,
// since either could have moved (e.g. an unstake, or another lock)
// between submission and confirmation.
func applyLockCollateral(tx core.Transaction) {
	var data lockCollateralData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.Amount == 0 {
		return
	}

	switch data.Symbol {
	case "CCT":
		if stakingStore.Stakes[tx.From]["CCT"] < data.Amount {
			return
		}
		stakingStore.Stakes[tx.From]["CCT"] -= data.Amount

	case "OETH":
		w := walletStore.Get(tx.From)
		if w.OETHBalance < data.Amount {
			return
		}
		w.OETHBalance -= data.Amount

	default:
		return
	}

	collateralStore.Add(tx.From, data.Symbol, data.Amount)
}

// applyLoanAddCollateral moves data.Amount out of the borrower's
// already-locked collateral pool (collateralStore) into a specific,
// existing FUNDED loan's CollateralAmount - re-validated here against
// current state, not trusted from submission time, since the loan's
// status or the caller's locked balance could have moved (repayment,
// liquidation, another lock/unlock) between submission and
// confirmation. Mirrors reserveCollateralForLoan's earmarking
// mechanism against a brand-new loan, just against an existing one.
func applyLoanAddCollateral(tx core.Transaction) {
	var data loanAddCollateralData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.Amount == 0 {
		return
	}

	loan, ok := loanStore.Get(data.LoanID)
	if !ok || loan.Borrower != tx.From || loan.Status != core.LoanFunded || loan.CollateralSymbol == "" {
		return
	}

	if !collateralStore.Release(tx.From, loan.CollateralSymbol, data.Amount) {
		return
	}

	loan.CollateralAmount += data.Amount
}

// applyLoanRedeemShortfall pays off exactly loan.LiquidatedShortfall
// during the Final Redemption window, mirroring settleRepayment's
// sub-address resolution but transferring the FIXED frozen shortfall,
// never repayAmountFor(loan) (collateral is already gone by this
// stage, so that would double-pay the lender - see
// HandleLoanRedeemShortfall's doc comment).
func applyLoanRedeemShortfall(tx core.Transaction) {
	var data loanRedeemShortfallData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	loan, ok := loanStore.Get(data.LoanID)
	if !ok || loan.RiskStage != core.LoanRiskAwaitingFinalRedemption || tx.AuthAddress != loan.Borrower {
		return
	}

	symbol := loanSymbol(loan)
	store, err := resolveTokenStore(symbol)
	if err != nil {
		return
	}
	borrowerSide := resolveBalanceAddress(loan.Borrower, tx.AuthPublicKey, symbol)
	lenderSide := resolveBalanceAddress(loan.Lender, loan.LenderPublicKey, symbol)
	if !store.Transfer(borrowerSide, lenderSide, loan.LiquidatedShortfall) {
		return
	}

	loan.Status = core.LoanRepaid
	loan.RepaidAt = tx.Timestamp
	loan.RiskStage = core.LoanRiskHealthy
	loan.RiskStageEnteredAt = 0
	loan.RiskStageDeadline = 0

	// Deliberately NOT the usual +10 on-time-repayment reputation bonus
	// creditEngine.RepayLoan grants - this loan already went through
	// Warning/MarginCall/GracePeriod/Liquidation, so it isn't an
	// on-time repayment, and awarding the same bonus a clean early
	// repayment gets would be exploitable. "Mining Power stays
	// unchanged" (per the Default Protection spec) means net-neutral,
	// not rewarded - just record the repayment for history without the
	// reputation increase.
	creditEngine.Profiles.Get(loan.Borrower).RecordRepayment(loan.LiquidatedShortfall)

	shortfall := loan.LiquidatedShortfall
	notifyUser(loan.Borrower, "LOAN_REDEEMED", fmt.Sprintf("You repaid the outstanding %d %s shortfall in time - your Mining Power is unaffected.", shortfall, symbol), loan.ID)
	notifyUser(loan.Lender, "LOAN_REDEEMED", fmt.Sprintf("A borrower repaid their %d %s shortfall within the final redemption window - no Mining Power changed hands.", shortfall, symbol), loan.ID)
}

func applyUnstake(tx core.Transaction) {
	// Queues the unbond rather than instantly removing stake - see
	// core/staking_logic.go's UnbondingEntry doc. Maturity is derived
	// from the TRANSACTION's own timestamp (fixed, identical on every
	// node), not "now" - re-checked at apply time too, since the
	// available-to-unbond balance may have moved between submission
	// and confirmation (e.g. another pending unstake ahead of this
	// one in the same block).
	symbol := stakeSymbolFromData(tx.Data)

	// Captured now, not re-derived when the unbond matures - see
	// UnbondingEntry's doc comment on CreditAddress.
	creditAddress := resolveBalanceAddress(tx.From, tx.AuthPublicKey, symbol)

	maturesAt := tx.Timestamp + core.UnbondingPeriodSeconds
	stakingStore.BeginUnbond(tx.From, symbol, tx.Amount, maturesAt, creditAddress)
}

// creditMaturedUnbond actually pays out an unbonding entry once
// core.StakingStore.ProcessMaturedUnbonds reports it as matured - core
// only owns the Stakes ledger and has no access to the token stores
// that hold real CCT/ETHTEST/USDCTEST balances (those live here in
// api), so this is the other half of unstaking a real balance actually
// coming back, not just vanishing from the ledger with nothing
// credited anywhere.
func creditMaturedUnbond(entry core.UnbondingEntry, timestamp int64) {
	symbol := entry.Symbol
	if symbol == "" {
		symbol = "ETHTEST" // entries persisted before Symbol existed
	}
	creditAddress := entry.CreditAddress
	if creditAddress == "" {
		creditAddress = entry.Address // see persistence.go's migration note
	}

	// ETHTEST is the only symbol with an oETH receipt, and redemption
	// works differently for it - see creditMaturedETHTestViaSwap. CCT
	// and USDCTEST stakes have no receipt token and are still paid out
	// as a plain 1:1 transfer from escrow.
	if symbol == "ETHTEST" {
		// Burn the oETH receipt now that the position is being
		// redeemed - see applyStake's minting side. Keyed by
		// entry.Address (the wallet's own raw address, where
		// OETHBalance lives), not creditAddress (the derived
		// sub-address the real ETHTEST itself lands at). Burned
		// unconditionally, regardless of how the swap below turns
		// out, since ProcessMaturedUnbonds has already removed the
		// stake-ledger entry - this position is gone either way.
		w := walletStore.Get(entry.Address)
		if w.OETHBalance >= entry.Amount {
			w.OETHBalance -= entry.Amount
		} else {
			w.OETHBalance = 0
		}
		creditMaturedETHTestViaSwap(entry.Amount, creditAddress, timestamp)
		return
	}

	store, err := stakeTokenStore(symbol)
	if err != nil {
		return
	}
	store.Transfer(stakeEscrowAddress(symbol), creditAddress, entry.Amount)
}

// creditMaturedETHTestViaSwap redeems a matured ETHTEST unbond by
// actually routing the position through the real ETHTEST -> CCT ->
// ETHTEST round trip on cctEthTestPool - the oETH position being "sold"
// back through CCT, the center engine every swap in this codebase
// already passes through (see applySwapTokenToToken, whose quote-both-
// legs-before-touching-any-balance pattern this mirrors exactly, just
// with the same pool/token on both ends of the round trip). This means
// what a staker gets back reflects real pool pricing - two 0.3% swap
// fees plus any slippage - rather than a guaranteed 1:1 return.
//
// Falls back to crediting the original staked amount directly from the
// stake escrow only if the pool can't quote the round trip at all (e.g.
// drained by unrelated activity between staking and maturity) - the
// same never-silently-lose-funds convention every other swap path here
// already follows on quote failure.
func creditMaturedETHTestViaSwap(amount uint64, creditAddress string, timestamp int64) {
	escrow := stakeEscrowAddress("ETHTEST")
	poolEscrow := poolAddress("ETHTEST")

	quotedCCT, ok := cctEthTestPool.QuoteTokenForCCT(amount)
	if ok {
		if _, ok2 := cctEthTestPool.QuoteCCTForToken(quotedCCT); ok2 {
			if ethTestStore.Transfer(escrow, poolEscrow, amount) {
				intermediateCCT, _ := cctEthTestPool.SwapTokenForCCT(amount)
				finalAmountOut, _ := cctEthTestPool.SwapCCTForToken(intermediateCCT)
				ethTestStore.Transfer(poolEscrow, creditAddress, finalAmountOut)
				recordPoolPrice("ETHTEST", cctEthTestPool, timestamp)
				return
			}
		}
	}

	ethTestStore.Transfer(escrow, creditAddress, amount)
}

type doubleSignEvidence struct {
	BlockA core.Block `json:"blockA"`
	BlockB core.Block `json:"blockB"`
}

type slashReportData struct {
	Reason           string              `json:"reason"` // "missed_block" or "double_sign"
	ValidatorAddress string              `json:"validatorAddress"`
	RoundHash        string              `json:"roundHash"` // missed_block dedup key
	Evidence         *doubleSignEvidence `json:"evidence,omitempty"`
}

// applySlashReport is what actually applies a slashing penalty -
// replicated the same way as every other action, so every node
// agrees on a validator's reputation/slash-count instead of only the
// node that happened to detect the offense first. The report itself
// is just a trigger the reporting node submitted (see
// consensus_loop.go's reportMissedBlock / peer.go's reportDoubleSign)
// - the actual penalty only applies here, and only after this
// function's own checks pass.
func applySlashReport(tx core.Transaction) {
	var data slashReportData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	status, ok := validatorStore.Get(data.ValidatorAddress)
	if !ok {
		return
	}

	switch data.Reason {
	case "double_sign":
		if !verifyDoubleSignEvidence(data.ValidatorAddress, data.Evidence) {
			return
		}

		dedupKey := "doublesign|" + data.ValidatorAddress + "|" + strconv.FormatUint(data.Evidence.BlockA.Index, 10)
		if slashAlreadyApplied(dedupKey) {
			return
		}

		wallet := walletStore.Get(data.ValidatorAddress)
		v := hydrateValidator(status)
		v.Wallet = *wallet

		core.ApplyDoubleSign(v)

		wallet.Reputation = v.Wallet.Reputation
		validatorStore.RecordSlash(data.ValidatorAddress, v.SlashCount-status.SlashCount)
		slashStake(data.ValidatorAddress, core.DoubleSignStakeSlashBps)
		banIfSlashThresholdCrossed(status)

	case "missed_block":
		dedupKey := "missed|" + data.ValidatorAddress + "|" + data.RoundHash
		if slashApplied(dedupKey) {
			return // already fully resolved - extra corroborating reports are no-ops
		}

		// A missed block is an absence - unlike double-sign above, no
		// cryptographic proof is possible, so a single node's report
		// alone isn't enough to trust. Require real corroboration from
		// multiple independently-running nodes before the penalty
		// itself ever applies - see missed_block_confirmation.go.
		count := recordMissedBlockReporter(dedupKey, tx.AuthAddress)
		required := requiredMissedBlockConfirmations(distinctActiveValidatorNodeCount())
		if count < required {
			return // waiting for more independent corroboration
		}

		if !markSlashApplied(dedupKey) {
			return // another report already crossed the threshold and applied this
		}
		clearMissedBlockReporters(dedupKey)

		wallet := walletStore.Get(data.ValidatorAddress)
		v := hydrateValidator(status)
		v.Wallet = *wallet

		core.ApplyMissedBlock(v)

		wallet.Reputation = v.Wallet.Reputation
		validatorStore.RecordMiss(data.ValidatorAddress)
		validatorStore.RecordSlash(data.ValidatorAddress, v.SlashCount-status.SlashCount)
		slashStake(data.ValidatorAddress, core.MissedBlockStakeSlashBps)
		banIfSlashThresholdCrossed(status)
	}
}

// slashStake seizes bps basis points of address's currently-staked CCT
// - PoSR's literal "may lose staked CCT" penalty. Burns the seized
// amount from the real CCT stake escrow (stakeEscrowAddress) rather
// than crediting anyone, so a validator can never profit from
// reporting another one, then re-syncs Wallet.StakedCCT so the
// read-model reflects the new, lower balance immediately.
func slashStake(address string, bps uint64) {
	staked := stakingStore.Stakes[address]["CCT"]
	if staked == 0 {
		return
	}
	amount := staked * bps / 10000
	if amount == 0 {
		return
	}
	seized := stakingStore.Slash(address, "CCT", amount)
	if seized == 0 {
		return
	}
	tokenStore.Burn(stakeEscrowAddress("CCT"), seized)
	stakingStore.SyncWallet(walletStore.Get(address), address)
}

// banIfSlashThresholdCrossed sets PermanentlyBanned once status's
// SlashCount (already updated by the caller's RecordSlash call) hits
// core.PermanentBanSlashCountThreshold - PoSR's "repeated offences may
// permanently remove validator privileges." Idempotent - setting an
// already-true flag again is harmless, so no extra guard is needed
// against this firing on every subsequent offense past the threshold.
func banIfSlashThresholdCrossed(status *core.ValidatorStatus) {
	if status.SlashCount >= core.PermanentBanSlashCountThreshold {
		status.PermanentlyBanned = true
	}
}

// applyBridgeDeposit credits ETHB (the auto-bridge feature's unified
// wei-denominated ETH balance) once a TxBridgeDeposit report - see
// api/eth_bridge.go's pollAndReportBridgeDeposit - confirms. Only the
// node that owns a given custodial account will ever detect and
// submit reports for it (the same disclosed non-gossip limitation as
// core/eth_custodial.go generally), but this apply function itself
// runs identically on every node once the report IS confirmed, so
// every node converges on the same ETHB credit regardless of which
// node originally noticed the deposit.
func applyBridgeDeposit(tx core.Transaction) {
	var data bridgeDepositReportData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	account, ok := ethCustodialStore.Get(data.CreditChainAddress)
	if !ok {
		return
	}

	// Dedup key includes the reported balance itself, not just the
	// account - a genuinely new (higher) deposit naturally gets a new
	// key, while a resubmission of the same report is caught here
	// rather than by trusting the reporting node not to resend it.
	dedupKey := "bridgedeposit|" + data.CreditChainAddress + "|" + data.NewBalanceWei
	if slashAlreadyApplied(dedupKey) {
		return
	}

	newBalance, ok := new(big.Int).SetString(data.NewBalanceWei, 10)
	if !ok {
		return
	}

	baseline := new(big.Int)
	if account.LastCreditedWei != "" {
		if _, ok := baseline.SetString(account.LastCreditedWei, 10); !ok {
			baseline.SetInt64(0)
		}
	}

	if newBalance.Cmp(baseline) <= 0 {
		return // stale/non-increasing report - nothing to credit
	}

	delta := new(big.Int).Sub(newBalance, baseline)
	if !delta.IsUint64() {
		// Overflow guard (see bridgedEthStore's doc comment in
		// api/token.go on the uint64 wei cap) - reject rather than
		// silently truncate a credit this large.
		return
	}

	ethbAddress := resolveBalanceAddress(data.CreditChainAddress, account.CreditChainPublicKey, "ETHB")
	bridgedEthStore.Mint(ethbAddress, delta.Uint64())
	account.LastCreditedWei = data.NewBalanceWei
}

// applyBridgeBtcDeposit credits BTCB (satoshi-denominated) once a
// TxBridgeBtcDeposit report - see api/btc_bridge.go's
// pollAndReportBtcDeposit - confirms. Same convergence property as
// applyBridgeDeposit: only the node a given address was registered on
// will ever detect and submit reports for it, but this apply function
// runs identically on every node once the report IS confirmed.
func applyBridgeBtcDeposit(tx core.Transaction) {
	var data btcBridgeDepositReportData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	watched, ok := btcAddressStore.Get(data.CreditChainAddress)
	if !ok {
		return
	}

	// Dedup key includes the reported balance itself, not just the
	// address - same reasoning as applyBridgeDeposit's dedupKey.
	dedupKey := "btcbridgedeposit|" + data.CreditChainAddress + "|" + fmt.Sprintf("%d", data.NewBalanceSats)
	if slashAlreadyApplied(dedupKey) {
		return
	}

	if data.NewBalanceSats <= watched.LastCreditedSats {
		return // stale/non-increasing report - nothing to credit
	}

	delta := data.NewBalanceSats - watched.LastCreditedSats

	// Uses the DEPOSITOR's public key (captured at registration time,
	// see core.BtcWatchedAddress's doc comment) - NOT tx.AuthPublicKey,
	// which on this transaction type is the REPORTING NODE's own key
	// (see verifyTxAuth's TxBridgeBtcDeposit case), not the wallet's.
	pub, err := hex.DecodeString(watched.CreditChainPublicKey)
	if err != nil {
		return
	}
	btcbAddress := core.DeriveTokenAddress(pub, core.BTCBAddressTag)
	bridgedBtcStore.Mint(btcbAddress, delta)
	watched.LastCreditedSats = data.NewBalanceSats
}

// applyBridgeWithdraw debits ETHB for an "ERC network" withdrawal
// (see api/eth_bridge_withdraw.go's HandleBridgedEthWithdraw, which
// already triggered the real Sepolia broadcast synchronously before
// this confirms - see that file's doc comment for the full ordering/
// atomicity discussion). Debits amount+data.FeeWei from the sender's
// ETHB sub-address - FeeWei is the live-gas-price+0.05%-bridge-fee
// figure the handler computed and snapshotted at request time (see
// ethbWithdrawFeeWei's doc comment for why this is trusted here rather
// than recomputed - determinism requires every node to debit the
// identical, already-decided amount). The fee portion is swept to a
// synthetic sink address, same pattern as cctTransferFeePoolAddress.
// Silently no-ops on insufficient balance (TokenStore.Transfer's
// standard convention) - the real broadcast already happened by the
// time this runs, so a no-op here would only occur if the fast-fail/
// reservation checks in the handler were somehow bypassed, which
// releases the node-local reservation on this node once this
// transaction is seen (it originated here, so it's the same node
// either way).
func applyBridgeWithdraw(tx core.Transaction) {
	var data bridgeWithdrawData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	ethbSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, "ETHB")
	total := tx.Amount + data.FeeWei

	if bridgedEthStore.BalanceOf(ethbSide) >= total {
		bridgedEthStore.Transfer(ethbSide, ethbBridgeFeeAddress(), data.FeeWei)
		bridgedEthStore.Burn(ethbSide, tx.Amount) // the real ETH already left the custodial reserve, not routed to any CCT-side address
	}

	releaseBridgeWithdrawal(ethbSide, total)
}

// ethbBridgeFeeAddress is a synthetic, nobody-holds-the-private-key
// sink for ETHB bridge fees - same pattern as
// cctTransferFeePoolAddress/registrationFeeAddress. Not swept
// anywhere (unlike the CCT transfer fee, which funds each block's
// validator) - this is CreditChain's own bridge revenue, a policy
// decision on what to do with it is out of scope for this feature.
func ethbBridgeFeeAddress() string {
	return "FEE:ETHB_BRIDGE"
}

func btcbBridgeFeeAddress() string {
	return "FEE:BTCB_BRIDGE"
}

// verifyDoubleSignEvidence independently re-derives the fraud proof
// from the evidence alone - it does NOT trust the reporting node's
// claim. Both blocks must be at the same height, have different
// (self-consistent) hashes, be credited to the same validator, and
// each carry a signature that actually verifies against its own
// claimed producer's known public key.
func verifyDoubleSignEvidence(validatorAddress string, ev *doubleSignEvidence) bool {
	if ev == nil {
		return false
	}

	a, b := ev.BlockA, ev.BlockB

	if a.Index != b.Index || a.Hash == b.Hash {
		return false
	}

	if a.Validator != validatorAddress || b.Validator != validatorAddress {
		return false
	}

	if core.ComputeBlockHash(a) != a.Hash || core.ComputeBlockHash(b) != b.Hash {
		return false
	}

	return verifyBlockProducer(a) && verifyBlockProducer(b)
}

type validatorRegisterData struct {
	NodeID        string `json:"nodeId"`
	NodePublicKey string `json:"nodePublicKey"`
}

// applyValidatorRegister is what actually adds an address to the
// shared ValidatorStore - replicated the same way as every other
// action, so every node agrees on who's a validator (and therefore
// who PoSR selection can even elect) instead of each node only
// knowing about validators registered directly against it. It also
// records which specific node the validator's key-holder delegated
// block-production to (embedded by the node that received the
// original signed request - see HandleRegisterValidator), which is
// what lets consensus_loop.go/peer.go tell "a node that knows about
// this validator" apart from "the node actually authorized to
// produce for it."
func applyValidatorRegister(tx core.Transaction) {
	// Staged-onboarding gate (see api/validator_application.go): must
	// be at least as strict as HandleRegisterValidator's HTTP pre-
	// check, never weaker - a transaction crafted directly, bypassing
	// the HTTP handler, gets no free pass.
	if !validatorApplicationStore.IsApproved(tx.From) {
		return
	}

	wallet := walletStore.Get(tx.From)
	stakingStore.SyncWallet(wallet, tx.From)

	validatorCount := len(validatorStore.Validators)

	// PoSR requirement: "Stake the minimum required amount of CCT" -
	// gated on StakedCCT specifically, not StakedETH (a legacy holdover
	// from before multi-token staking existed - see
	// core/staking_sync.go's doc comment). This is also the actual
	// consensus-applied check, so it must be at least as strict as
	// HandleRegisterValidator's HTTP pre-check (api/validators.go),
	// never weaker - a transaction crafted directly, bypassing the
	// HTTP handler, gets no free pass. minValidatorStakeForCount is the
	// stepped per-validator-count requirement (stage 3 of the staged
	// pipeline).
	if wallet.StakedCCT < minValidatorStakeForCount(validatorCount) || wallet.Reputation < MinValidatorReputation {
		return
	}

	// PoSR requirement: "no unresolved validator penalties" - a
	// permanently banned address (see core/slashing.go's
	// PermanentBanSlashCountThreshold) can never (re-)register, full
	// stop, regardless of current stake/reputation.
	if status, ok := validatorStore.Get(tx.From); ok && status.PermanentlyBanned {
		return
	}

	// The fee may have moved between submission and confirmation -
	// re-checked and only actually deducted here, never trusted from
	// submission time alone (same pattern as applyTokenRegister's
	// ETHTEST creation fee). validatorRegisterFeeForCount already reads
	// through governanceParam, so a passed PARAM_CHANGE proposal
	// changing "ValidatorRegisterFeeCCT" still takes effect here
	// without a code change - see api/governance_params.go.
	if !tokenStore.Transfer(tx.From, registrationFeeAddress(), validatorRegisterFeeForCount(validatorCount)) {
		return
	}

	var data validatorRegisterData
	_ = json.Unmarshal([]byte(tx.Data), &data)

	validatorStore.Register(tx.From, data.NodeID, data.NodePublicKey)
}

// validatorDelegationEscrowAddress is where delegated CCT sits while
// bonded to a validator - a single shared escrow, mirroring
// stakeEscrowAddress's pattern exactly. The DelegationStore ledger
// (validator -> delegator -> amount) is what tracks whose share is
// whose; the escrow itself is undifferentiated, same as staking.
const validatorDelegationEscrowAddress = "validator-delegation-escrow"

type validatorDelegateData struct {
	Validator string `json:"validator"`
	Amount    uint64 `json:"amount"`
}

// applyValidatorDelegate moves real CCT from the delegator's own
// balance into the shared delegation escrow and records the ledger
// entry - re-validates the target is still a registered, active
// validator and isn't the delegator's own address at apply time
// (never trusted from submission-time state alone), same convention
// as every other handler in this codebase.
func applyValidatorDelegate(tx core.Transaction) {
	var data validatorDelegateData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if tx.Amount == 0 || data.Validator == tx.From {
		return
	}
	v, ok := validatorStore.Get(data.Validator)
	if !ok || !v.Active {
		return
	}
	if !tokenStore.Transfer(tx.From, validatorDelegationEscrowAddress, tx.Amount) {
		return
	}
	delegationStore.Delegate(data.Validator, tx.From, tx.Amount)
}

type validatorUndelegateData struct {
	Validator string `json:"validator"`
	Amount    uint64 `json:"amount"`
}

// applyValidatorUndelegate queues an undelegate request rather than
// instantly returning the CCT - see core.DelegationStore's doc
// comment for why (the position stays counted toward the validator's
// PoSR weight and still shares in rewards until it matures), same
// unbonding-period model applyUnstake already uses for regular stake.
func applyValidatorUndelegate(tx core.Transaction) {
	var data validatorUndelegateData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if tx.Amount == 0 {
		return
	}
	maturesAt := tx.Timestamp + core.UnbondingPeriodSeconds
	delegationStore.BeginUndelegate(data.Validator, tx.From, tx.Amount, maturesAt)
}

type validatorSetCommissionData struct {
	CommissionBps uint16 `json:"commissionBps"`
}

// applyValidatorSetCommission updates a validator's own commission
// rate - core.ValidatorStore.SetCommission independently re-validates
// tx.From is actually a registered validator and the rate is within
// MaxCommissionBps, so this can't be abused even if verifyTxAuth's
// signature check were somehow bypassed.
func applyValidatorSetCommission(tx core.Transaction) {
	var data validatorSetCommissionData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	validatorStore.SetCommission(tx.From, data.CommissionBps)
}

// stakeData carries which token a stake/unstake transaction concerns.
// Absent/empty Symbol means ETHTEST - the only stakeable token before
// this field existed, and still the default so old, already-signed
// requests keep working unchanged.
type stakeData struct {
	Symbol string `json:"symbol"`
	Amount uint64 `json:"amount"`
}

func stakeSymbolFromData(data string) string {
	if data == "" {
		return "ETHTEST"
	}
	var parsed stakeData
	if err := json.Unmarshal([]byte(data), &parsed); err != nil || parsed.Symbol == "" {
		return "ETHTEST"
	}
	return parsed.Symbol
}

// stakeTokenStore resolves which of the three stakeable tokens' stores
// a stake/unstake transaction actually moves real balance through.
// Deliberately only these three - staking is a separate concept from
// the general CCT/REPT/custom-token universe resolveTokenStore covers.
func stakeTokenStore(symbol string) (*core.TokenStore, error) {
	switch symbol {
	case "CCT":
		return tokenStore, nil
	case "ETHTEST":
		return ethTestStore, nil
	case "USDCTEST":
		return usdcTestStore, nil
	}
	return nil, fmt.Errorf("unsupported stake symbol %q", symbol)
}

// stakeEscrowAddress is where actually-staked balance sits while
// bonded - same synthetic-address convention poolAddress/
// p2pOfferEscrowAddress already use elsewhere in this file.
func stakeEscrowAddress(symbol string) string {
	return "STAKE:" + symbol
}

func applyStake(tx core.Transaction) {
	symbol := stakeSymbolFromData(tx.Data)

	store, err := stakeTokenStore(symbol)
	if err != nil {
		return
	}

	// Re-check the pilot's per-account, per-symbol stake cap here too,
	// not just at submission (api/stake.go) - two stakes from the same
	// account pending in the mempool at once could both individually
	// look under the cap at submission time but jointly exceed it once
	// both confirm. Silently dropping the over-cap one (rather than
	// partially applying it) mirrors how every other re-checked
	// condition in this file behaves.
	if stakingStore.StakeOf(tx.From, symbol)+tx.Amount > core.MaxTestStakePerAccount {
		return
	}

	// The staked amount is now real - actually moved out of the
	// holder's own CCT/ETHTEST/USDCTEST balance into an escrow address,
	// not just a self-declared number with nothing backing it (the
	// pre-existing gap MaxTestStakePerAccount's doc comment used to
	// describe). ETHTEST/USDCTEST balances live at a sub-address
	// derived from the staker's public key, same as everywhere else
	// those tokens are moved.
	balanceAddr := resolveBalanceAddress(tx.From, tx.AuthPublicKey, symbol)
	if !store.Transfer(balanceAddr, stakeEscrowAddress(symbol), tx.Amount) {
		return
	}

	stakingStore.StakeETH(tx.From, symbol, tx.Amount)
	stakingStore.SyncWallet(walletStore.Get(tx.From), tx.From)

	// Reward is based on the amount actually staked in THIS action, not
	// the account's running total - stakingStore.Reward(address,
	// symbol) reads Stakes AFTER StakeETH already added to it, so using
	// that here would re-mint 10% of the entire balance on every single
	// stake call, however small - an unbounded reward-amplification bug
	// (restake the same funds in tiny increments enough times and mint
	// far more than 10% of anything you actually contributed), not the
	// "estimated reward for what you're staking" the UI already
	// promises. CCT is the uniform reward currency regardless of which
	// token was staked, matching the existing precedent (ETHTEST
	// staking already rewarded in CCT, never in ETHTEST itself).
	reward := core.RewardForStake(tx.Amount)
	mintCCTCapped(tx.From, reward)

	// oETH is a 1:1 receipt balance for staked ETHTEST specifically
	// (the only symbol this predates and names itself after) - minted
	// here, burned back out in creditMaturedUnbond once the real
	// ETHTEST actually returns, so it never over- or
	// under-represents what's genuinely staked. Staking CCT/USDCTEST
	// mints no receipt token - oETH has always meant "staked ETH,"
	// not a generic staking-receipt concept.
	if symbol == "ETHTEST" {
		walletStore.Get(tx.From).OETHBalance += tx.Amount
	}
}

type borrowData struct {
	Interest uint64 `json:"interest"`
}

// hasEnoughCollateral is the non-mutating fast-fail check HTTP
// handlers use before ever recording a transaction - true if amount of
// EITHER locked CCT or locked oETH alone would cover it (mirroring
// exactly what reserveCollateralForLoan enforces for real at
// confirmation time, just without actually reserving anything yet).
func hasEnoughCollateral(address string, amount uint64) bool {
	return collateralStore.LockedOf(address, "CCT") >= amount ||
		collateralStore.LockedOf(address, "OETH") >= amount
}

// reserveCollateralForLoan earmarks exactly amount of the borrower's
// locked collateral to back one specific loan - preferring CCT, then
// falling back to OETH, but always a single symbol per loan (never a
// mix), so releaseLoanCollateral can later return the exact same
// amount to the exact same source. This replaces the old
// hasCollateral, which only checked "is anything at all locked or
// staked" without ever relating the amount to the loan size - the gap
// that let repayment release the wrong amount (or nothing at all) for
// a loan that was never actually 1:1 backed by anything specific. The
// reservation itself happens here (removed from collateralStore's
// pool immediately), not just checked - so the same locked collateral
// can't be reserved twice for two different loans.
func reserveCollateralForLoan(address string, amount uint64) (symbol string, ok bool) {
	if collateralStore.Release(address, "CCT", amount) {
		return "CCT", true
	}
	if collateralStore.Release(address, "OETH", amount) {
		return "OETH", true
	}
	return "", false
}

// peekCollateralSymbolForLoan is reserveCollateralForLoan's non-
// mutating twin - same CCT-then-OETH preference, just a read instead
// of an actual reservation. Used at request time (HandleBorrow) to
// price a loan's interest rate off the collateral it's about to be
// backed by, before the real reservation happens later at block-
// confirmation time (applyBorrow).
func peekCollateralSymbolForLoan(address string, amount uint64) (symbol string, ok bool) {
	if collateralStore.LockedOf(address, "CCT") >= amount {
		return "CCT", true
	}
	if collateralStore.LockedOf(address, "OETH") >= amount {
		return "OETH", true
	}
	return "", false
}

// collateralValueInYen prices collateral in YEN-e-equivalent terms for
// the liquidation engine, reusing applySwapTokenToToken's exact two-hop
// pattern as a pure quote (no state mutation): OETH -> CCT via
// cctEthTestPool, then that CCT amount -> USDCTEST via cctUsdcTestPool,
// treating the USDCTEST-equivalent result as YEN-e's value too (YEN-e
// has no market of its own - see the approved Phase B design, which
// deliberately proxies through USDCTEST's existing pool rather than
// seeding a new one). ok=false means "no live price available - fall
// back to due-date-only handling", NEVER "worth zero" or "worth
// infinite" - callers must treat it as "can't assess", not a value.
// CCT collateral always returns ok=false: CCT is this system's pricing
// numeraire, nothing else prices CCT against a single arbitrary pool,
// so CCT-collateralized loans stay due-date-only in this phase, exactly
// matching today's behavior.
func collateralValueInYen(symbol string, amount uint64) (valueInYen uint64, ok bool) {
	if symbol != "OETH" || amount == 0 {
		return 0, false
	}

	quotedCCT, ok := cctEthTestPool.QuoteTokenForCCT(amount)
	if !ok {
		return 0, false
	}
	quotedYen, ok := cctUsdcTestPool.QuoteCCTForToken(quotedCCT)
	if !ok {
		return 0, false
	}
	return quotedYen, true
}

// releaseLoanCollateral returns a repaid loan's earmarked collateral
// to the exact staked/oETH balance it was reserved from -
// collateralStore is deliberately NOT credited back here, since this
// collateral is fully released to the user, not returned to the
// general locked-but-unallocated pool.
func releaseLoanCollateral(loan *core.Loan) {
	creditCollateralTo(loan.Borrower, loan.CollateralSymbol, loan.CollateralAmount)
}

// creditCollateralTo moves a collateral amount into recipient's real
// staked-CCT/oETH balance - the same destination releaseLoanCollateral
// and (formerly) forfeitLoanCollateralToLender each credited into,
// just factored out so liquidateLoan can split a single loan's
// collateral between two recipients (partial seize + partial refund)
// without duplicating the CCT/OETH switch three times over.
func creditCollateralTo(recipient, symbol string, amount uint64) {
	if amount == 0 || recipient == "" {
		return
	}

	switch symbol {
	case "CCT":
		if stakingStore.Stakes[recipient] == nil {
			stakingStore.Stakes[recipient] = make(map[string]uint64)
		}
		stakingStore.Stakes[recipient]["CCT"] += amount

	case "OETH":
		walletStore.Get(recipient).OETHBalance += amount
	}
}

// liquidateLoan is the shared collateral-disposal path for BOTH the
// due-date sweep (processLoanDefaults) and the proactive price sweep
// (processCollateralLiquidations) - replaces the old
// forfeitLoanCollateralToLender, which always seized 100% of
// collateral unconditionally regardless of what it was actually worth.
// Returns the shortfall (0 if the lender was fully covered) - the
// caller decides what that shortfall means for loan status/reputation
// (see processLoanDefaults), since that's identical regardless of
// which sweep triggered the liquidation.
//
// reason is accepted for caller-side logging/notification context
// (e.g. "overdue" vs "price-triggered") - liquidateLoan itself treats
// every trigger identically, which is the whole point of sharing it.
func liquidateLoan(loan *core.Loan, asOf int64, reason string) (shortfall uint64) {
	owed := repayAmountFor(loan)

	if loan.CollateralAmount == 0 || loan.Lender == "" {
		return owed
	}

	// CCT collateral has no live YEN-e price (CCT is this system's
	// pricing numeraire - nothing prices CCT against a single
	// arbitrary pool), so it keeps today's exact behavior: seize
	// everything, assume the lender recovered nothing.
	collateralValue, ok := collateralValueInYen(loan.CollateralSymbol, loan.CollateralAmount)
	if !ok {
		creditCollateralTo(loan.Lender, loan.CollateralSymbol, loan.CollateralAmount)
		loan.CollateralAmount = 0
		return owed
	}

	if collateralValue >= owed {
		// Collateral covers the debt - seize only the proportional
		// slice needed (approximated linearly off the quoted value;
		// the AMM curve isn't perfectly linear at this size, but this
		// is close enough for a demo-scale liquidation engine and
		// never seizes more than CollateralAmount), refund the rest.
		seize := loan.CollateralAmount
		if collateralValue > 0 {
			seize = loan.CollateralAmount * owed / collateralValue
			if seize > loan.CollateralAmount {
				seize = loan.CollateralAmount
			}
		}
		refund := loan.CollateralAmount - seize

		creditCollateralTo(loan.Lender, loan.CollateralSymbol, seize)
		creditCollateralTo(loan.Borrower, loan.CollateralSymbol, refund)
		loan.CollateralAmount = 0
		loan.Status = core.LoanRepaid
		loan.RepaidAt = asOf
		return 0
	}

	// Collateral falls short - seize all of it, shortfall is whatever
	// value it couldn't cover.
	creditCollateralTo(loan.Lender, loan.CollateralSymbol, loan.CollateralAmount)
	loan.CollateralAmount = 0
	return owed - collateralValue
}

// dueAtFor computes a peer-funded loan's due timestamp from the
// lender's offer duration, anchored to the block timestamp the loan
// was actually funded at (never wall-clock) - 0 (no due date, can
// never default) if the offer didn't set a duration, which keeps
// every offer/loan created before this feature existed exempt rather
// than retroactively defaulting them.
func dueAtFor(durationSeconds uint64, fundedAt int64) int64 {
	if durationSeconds == 0 {
		return 0
	}
	return fundedAt + int64(durationSeconds)
}

// advanceLoanLifecycle is the single place every Default Protection
// stage transition happens (Healthy -> Warning -> MarginCall ->
// GracePeriod -> liquidate -> AwaitingFinalRedemption) - called once
// per candidate loan from BOTH processLoanDefaults and
// processCollateralLiquidations below, each computing its own atRisk
// signal from its own trigger (due date vs. collateral ratio).
// Idempotent with respect to (loan.RiskStage, asOf): every branch
// checks the CURRENT stage before acting, so a loan that's
// simultaneously overdue AND under-collateralized - visited by both
// sweeps in the same block - never gets double-advanced.
//
// liquidateLoan's seize/refund/shortfall math and
// creditEngine.DefaultLoan/shortfallReputationDelta's penalty math are
// NOT reimplemented here - only WHEN they fire moves. liquidateLoan
// now fires only once the GracePeriod deadline expires (previously
// fired the instant a sweep condition was true); DefaultLoan now fires
// only from processFinalRedemptions once the Final Redemption window
// also expires (previously fired atomically with liquidation) - see
// that function's own doc comment.
func advanceLoanLifecycle(loan *core.Loan, atRisk bool, asOf int64, reason string) {
	switch loan.RiskStage {
	case core.LoanRiskHealthy:
		if !atRisk {
			return
		}
		loan.RiskStage = core.LoanRiskWarning
		loan.RiskStageEnteredAt = asOf
		notifyUser(loan.Borrower, "LOAN_RISK_WARNING",
			"Your collateral has entered the warning zone. Add collateral or repay before liquidation.", loan.ID)

	case core.LoanRiskWarning:
		if !atRisk {
			loan.RiskStage = core.LoanRiskHealthy
			loan.RiskStageEnteredAt = 0
			return
		}
		if asOf-loan.RiskStageEnteredAt >= core.LoanRiskWarningDwellSeconds {
			loan.RiskStage = core.LoanRiskMarginCall
			loan.RiskStageEnteredAt = asOf
			notifyUser(loan.Borrower, "LOAN_MARGIN_CALL",
				"Margin call: add collateral, repay part or all of this loan, or close it voluntarily before a grace period begins.", loan.ID)
		}

	case core.LoanRiskMarginCall:
		if !atRisk {
			loan.RiskStage = core.LoanRiskHealthy
			loan.RiskStageEnteredAt = 0
			return
		}
		if asOf-loan.RiskStageEnteredAt >= core.LoanRiskMarginCallDwellSeconds {
			loan.RiskStage = core.LoanRiskGracePeriod
			loan.RiskStageEnteredAt = asOf
			loan.RiskStageDeadline = asOf + core.LoanRiskGracePeriodSeconds
			notifyUser(loan.Borrower, "LOAN_GRACE_PERIOD_STARTED",
				fmt.Sprintf("Your grace period has begun - repay or add collateral within %d hours to avoid liquidation.",
					core.LoanRiskGracePeriodSeconds/3600), loan.ID)
		}

	case core.LoanRiskGracePeriod:
		if !atRisk {
			loan.RiskStage = core.LoanRiskHealthy
			loan.RiskStageEnteredAt = 0
			loan.RiskStageDeadline = 0
			return
		}
		if asOf < loan.RiskStageDeadline {
			return
		}

		// Grace period expired and still at risk - liquidate now,
		// reusing liquidateLoan completely unchanged.
		owed := repayAmountFor(loan)
		shortfall := liquidateLoan(loan, asOf, reason)

		if shortfall == 0 {
			// Same "collateral fully covered it, no penalty" outcome
			// this codebase already had - just reached later now.
			// liquidateLoan already flipped loan.Status to LoanRepaid.
			loan.RiskStage = core.LoanRiskHealthy
			loan.RiskStageEnteredAt = 0
			loan.RiskStageDeadline = 0
			creditHistory.Add(loan.Borrower, core.CreditEvent{Type: "LOAN_REPAID", Amount: loan.Amount})
			notifyUser(loan.Borrower, "LOAN_REPAID", fmt.Sprintf("Your %d %s loan's grace period expired and it was liquidated, but your collateral fully covered what was owed - no Mining Power penalty, and any leftover collateral was returned to you", loan.Amount, loanSymbol(loan)), loan.ID)
			notifyUser(loan.Lender, "LOAN_REPAID", fmt.Sprintf("A %d %s loan was liquidated after its grace period expired and you were made whole from the borrower's collateral", loan.Amount, loanSymbol(loan)), loan.ID)
			return
		}

		loan.RiskStage = core.LoanRiskAwaitingFinalRedemption
		loan.RiskStageEnteredAt = asOf
		loan.RiskStageDeadline = asOf + core.LoanRiskFinalRedemptionWindowSeconds
		loan.LiquidatedShortfall = shortfall
		loan.LiquidatedOwed = owed
		notifyUser(loan.Borrower, "LOAN_FINAL_REDEMPTION_NOTICE",
			fmt.Sprintf("You still owe %d %s. Repay within %d hours to preserve your Mining Power.",
				shortfall, loanSymbol(loan), core.LoanRiskFinalRedemptionWindowSeconds/3600), loan.ID)
		notifyUser(loan.Lender, "LOAN_LIQUIDATED",
			fmt.Sprintf("A %d %s loan's collateral was liquidated after its grace period expired - it covered %d of what was owed. A final redemption window is open before Mining Power transfers.",
				loan.Amount, loanSymbol(loan), owed-shortfall), loan.ID)

	case core.LoanRiskAwaitingFinalRedemption, core.LoanRiskDefaulted:
		// Terminal for this function - resolved by processFinalRedemptions
		// (deadline expiry) or applyLoanRedeemShortfall (borrower pays in
		// time, api/loan_redemption.go). Nothing to do here.
	}
}

// processLoanDefaults sweeps every peer-funded loan whose due date has
// passed while still unpaid - block-timestamp-driven determinism, the
// same pattern processDormantWallets already uses, so every node that
// applies this same block reaches the identical set of at-risk loans.
// Self-funded loans (Lender == "", see core.Loan's doc comment) are
// never swept here - there's no real counterparty for "the lender is
// always made whole" to protect, so default doesn't carry the
// peer-funded marketplace's lender-protection meaning for those.
//
// A passed due date can't un-pass, so atRisk is always true for every
// loan this sweep selects - the actual liquidation decision (and the
// Warning/MarginCall/GracePeriod stages leading up to it) live in
// advanceLoanLifecycle, called once per loan here.
func processLoanDefaults(asOf int64) {
	var overdueIDs []string
	for id, loan := range loanStore.Loans {
		if loan.Status == core.LoanFunded && loan.Lender != "" && loan.DueAt > 0 && loan.DueAt < asOf {
			overdueIDs = append(overdueIDs, id)
		}
	}
	if len(overdueIDs) == 0 {
		return
	}
	// Same "sort for deterministic processing order" reasoning as
	// processDormantWallets - Go map iteration order is randomized.
	sort.Strings(overdueIDs)

	for _, id := range overdueIDs {
		advanceLoanLifecycle(loanStore.Loans[id], true, asOf, "overdue")
	}
}

// processCollateralLiquidations sweeps every peer-funded, OETH-
// collateralized loan - not just ones currently failing the threshold,
// unlike before this feature: a loan whose collateral value recovers
// needs to be re-visited so advanceLoanLifecycle can step it back down
// to Healthy, which requires evaluating atRisk fresh on every pass, on
// every such loan, every block. Same block-timestamp-driven
// determinism as processLoanDefaults (sorted iteration - Go map order
// is randomized, every node must reach the identical result).
// CCT-collateralized loans are never scanned here - collateralValueInYen
// always returns ok=false for CCT, so they'd never trigger anyway, but
// skipping them explicitly documents that this phase's proactive
// liquidation is OETH-only by design (see the approved Phase B plan).
func processCollateralLiquidations(asOf int64) {
	var oethLoanIDs []string
	for id, loan := range loanStore.Loans {
		if loan.Status == core.LoanFunded && loan.Lender != "" && loan.CollateralSymbol == "OETH" {
			oethLoanIDs = append(oethLoanIDs, id)
		}
	}
	if len(oethLoanIDs) == 0 {
		return
	}
	sort.Strings(oethLoanIDs)

	for _, id := range oethLoanIDs {
		loan := loanStore.Loans[id]

		collateralValue, ok := collateralValueInYen(loan.CollateralSymbol, loan.CollateralAmount)
		if !ok {
			continue // no live price - can't judge risk, leave this loan's stage untouched
		}
		owed := repayAmountFor(loan)
		if owed == 0 {
			continue
		}

		// collateralValue/owed < threshold/100, rearranged to avoid
		// floating point: collateralValue*100 < threshold*owed.
		atRisk := collateralValue*100 < core.LiquidationThresholdPercent*owed
		advanceLoanLifecycle(loan, atRisk, asOf, "price-triggered")
	}
}

// processFinalRedemptions is the ONLY place creditEngine.DefaultLoan /
// shortfallReputationDelta actually fires now - moved here, unmodified
// in what it does, from processLoanDefaults/processCollateralLiquidations,
// so the Mining Power transfer happens only once every recovery
// opportunity (repay, add collateral, grace period, final redemption)
// has actually been exhausted, per the Default Protection philosophy
// of never punishing immediately. Same block-timestamp-driven
// determinism/sorted-iteration convention as every sweep above.
func processFinalRedemptions(asOf int64) {
	var ids []string
	for id, loan := range loanStore.Loans {
		if loan.RiskStage == core.LoanRiskAwaitingFinalRedemption &&
			loan.RiskStageDeadline > 0 && loan.RiskStageDeadline < asOf {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	sort.Strings(ids)

	for _, id := range ids {
		loan := loanStore.Loans[id]
		shortfall, owed := loan.LiquidatedShortfall, loan.LiquidatedOwed

		delta := creditEngine.DefaultLoan(loan, shortfall, owed)
		loan.RiskStage = core.LoanRiskDefaulted
		loan.RiskStageDeadline = 0

		if delta > 0 {
			if reward := core.IncreaseWalletReputation(walletStore.Get(loan.Lender), delta); reward > 0 {
				mintRept(loan.Lender, reward)
			}

			reputationEventStore.Record(&core.ReputationEvent{
				TxID: "final-redemption-default-" + loan.ID, Target: loan.Borrower, Delta: -int64(delta),
				Reason: "Defaulted on a peer-funded loan after the final redemption window expired", Timestamp: asOf,
			})
			reputationEventStore.Record(&core.ReputationEvent{
				TxID: "final-redemption-default-" + loan.ID + "-lender", Target: loan.Lender, Delta: int64(delta),
				Reason: "Received a defaulting borrower's collateral and Mining Power", Timestamp: asOf,
			})
		}

		creditHistory.Add(loan.Borrower, core.CreditEvent{Type: "LOAN_DEFAULT", Amount: loan.Amount})

		notifyUser(loan.Borrower, "LOAN_DEFAULT", fmt.Sprintf("Your final redemption window expired - your Mining Power was reduced for the %d %s shortfall left uncovered", shortfall, loanSymbol(loan)), loan.ID)
		notifyUser(loan.Lender, "LOAN_DEFAULT", fmt.Sprintf("A borrower's final redemption window expired on their %d %s loan - you received their Mining Power for the %d shortfall", loan.Amount, loanSymbol(loan), shortfall), loan.ID)
	}
}

func applyBorrow(tx core.Transaction) {
	// Collateral may have moved between submission and confirmation -
	// re-check (and reserve) at apply time rather than trusting the
	// submission-time check alone.
	collateralSymbol, ok := reserveCollateralForLoan(tx.To, tx.Amount)
	if !ok {
		return
	}

	var data borrowData
	_ = json.Unmarshal([]byte(tx.Data), &data)

	id := "loan-" + tx.ID[:min(8, len(tx.ID))]

	loanStore.CreateLoan(id, tx.To, tx.Amount, data.Interest, collateralSymbol, tx.Amount, tx.Timestamp)

	creditHistory.Add(tx.To, core.CreditEvent{Type: "BORROW", Amount: tx.Amount})
	core.RecordLoanTaken(creditStore.Get(tx.To))

	// Borrowed funds land in the separate YEN-e ledger, not CCT - keeps
	// "earned by staking/governance" (CCT) and "borrowed" (YEN-e, the
	// stable settlement currency) visibly distinct balances, never
	// commingled. Minted into the borrower's derived YEN-e sub-address
	// (see resolveBalanceAddress), not their CCT address -
	// tx.AuthPublicKey is the borrower's own authenticated public key
	// (see verifyTxAuth's TxBorrow case).
	yenSide := resolveBalanceAddress(tx.To, tx.AuthPublicKey, "YENE")
	yenStore.Mint(yenSide, tx.Amount)
}

func applyLoanOfferCreate(tx core.Transaction) {
	var data loanOfferCreateData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.TotalAmount == 0 {
		return
	}
	// Defense-in-depth: HandleCreateLoanOffer already checked this at
	// request time (see requireLendingAccess), but re-checked here too
	// since reputation could have dropped between submission and this
	// block confirming - never trust submission-time state alone.
	if !kycStore.IsApproved(tx.From, walletStore.Get(tx.From).Reputation) {
		return
	}

	store, err := resolveTokenStore(data.Symbol)
	if err != nil {
		return
	}

	lenderSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, data.Symbol)
	escrow := loanOfferEscrowAddress(tx.ID)

	if !store.Transfer(lenderSide, escrow, data.TotalAmount) {
		return
	}

	loanOfferStore.Create(&core.LoanOffer{
		ID:              tx.ID,
		Lender:          tx.From,
		LenderPublicKey: tx.AuthPublicKey,
		Symbol:          data.Symbol,
		RatePercent:     data.RatePercent,
		DurationSeconds: data.DurationSeconds,
		TotalAmount:     data.TotalAmount,
		RemainingAmount: data.TotalAmount,
		Status:          core.LoanOfferOpen,
		CreatedAt:       tx.Timestamp,
	})
}

// applyLoanOfferDraw is the borrower's own choice of amount actually
// taking effect - re-checks everything submission-time already
// checked (offer still open/has enough capital, borrower still has
// collateral and is still within their borrow limit), since any of
// that could have changed between submission and confirmation.
func applyLoanOfferDraw(tx core.Transaction) {
	var data loanOfferDrawData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.Amount == 0 {
		return
	}

	offer, ok := loanOfferStore.Get(data.OfferID)
	if !ok {
		return
	}

	wallet := walletStore.Get(tx.From)
	if data.Amount > core.BorrowLimit(wallet.Reputation) {
		return
	}

	// Reserved first - if either step below fails, it's refunded back
	// to the pool (same refund-on-later-failure convention every other
	// two-step mutation in this codebase already follows), since
	// there's no single atomic operation covering both this and the
	// offer draw.
	collateralSymbol, ok := reserveCollateralForLoan(tx.From, data.Amount)
	if !ok {
		return
	}

	store, err := resolveTokenStore(offer.Symbol)
	if err != nil {
		collateralStore.Add(tx.From, collateralSymbol, data.Amount)
		return
	}

	if !loanOfferStore.Draw(offer.ID, data.Amount) {
		collateralStore.Add(tx.From, collateralSymbol, data.Amount)
		return
	}

	borrowerSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, offer.Symbol)
	escrow := loanOfferEscrowAddress(offer.ID)
	store.Transfer(escrow, borrowerSide, data.Amount)

	id := "loan-" + tx.ID[:min(8, len(tx.ID))]
	loanStore.Add(&core.Loan{
		ID:               id,
		Borrower:         tx.From,
		Lender:           offer.Lender,
		LenderPublicKey:  offer.LenderPublicKey,
		Symbol:           offer.Symbol,
		Amount:           data.Amount,
		Interest:         offer.RatePercent,
		Status:           core.LoanFunded,
		CollateralSymbol: collateralSymbol,
		CollateralAmount: data.Amount,
		DueAt:            dueAtFor(offer.DurationSeconds, tx.Timestamp),
		CreatedAt:        tx.Timestamp,
	})

	creditHistory.Add(tx.From, core.CreditEvent{Type: "BORROW", Amount: data.Amount})
	core.RecordLoanTaken(creditStore.Get(tx.From))

	// Lender-side reputation/profile reward - the same effect
	// creditEngine.FundLoan applies for the older /loans/fund path, but
	// that function requires an existing LoanPending record (a
	// precondition a direct offer-draw never goes through, since the
	// loan here is created already-funded), so it's inlined directly
	// rather than reusing a function whose guard would just reject it.
	creditEngine.Profiles.Get(offer.Lender).RecordFunding(data.Amount)
	if reward := core.IncreaseWalletReputation(walletStore.Get(offer.Lender), core.ReputationDeltaLoanFunded); reward > 0 {
		mintRept(offer.Lender, reward)
	}
	// Target is the LENDER, not tx.From (the borrower drawing this
	// offer) - see core.ReputationEvent's doc comment for why this
	// needs its own explicit target rather than assuming tx.From.
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: offer.Lender, Delta: int64(core.ReputationDeltaLoanFunded),
		Reason: "Funded a loan (peer-funded draw)", Timestamp: tx.Timestamp,
	})
}

// applyLoanOfferClose refunds whatever capital the lender never got
// drawn back to their own derived USDCTEST address.
func applyLoanOfferClose(tx core.Transaction) {
	var data loanOfferCloseData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	offer, ok := loanOfferStore.Close(data.OfferID, tx.From)
	if !ok {
		return
	}

	store, err := resolveTokenStore(offer.Symbol)
	if err != nil {
		return
	}

	lenderSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, offer.Symbol)
	escrow := loanOfferEscrowAddress(offer.ID)
	store.Transfer(escrow, lenderSide, offer.RemainingAmount)
}

// applyLoanRequestCreate re-validates everything HandleCreateLoanRequest
// already checked at submission time (collateral could have moved
// since then) before actually reserving it - the same never-trust-
// submission-time-alone discipline every other two-step mutation in
// this codebase follows.
func applyLoanRequestCreate(tx core.Transaction) {
	var data loanRequestCreateData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.Amount == 0 || data.CollateralAmount < data.Amount || data.MaxInterestPercent == 0 {
		return
	}
	if data.CollateralSymbol != "CCT" && data.CollateralSymbol != "OETH" {
		return
	}
	if len(data.Purpose) > core.MaxLoanRequestPurposeLen {
		return
	}

	wallet := walletStore.Get(tx.From)
	if data.Amount > core.BorrowLimit(wallet.Reputation) {
		return
	}

	// Reserves exactly the borrower's chosen CollateralAmount (unlike
	// reserveCollateralForLoan, which always reserves an amount equal
	// to the loan itself, picking CCT-then-OETH automatically) - here
	// the borrower already chose both the symbol and the amount, which
	// may exceed the loan amount (a deliberate, disclosed enhancement
	// over the legacy 1:1-only paths - see core.LoanRequest's doc
	// comment).
	if !collateralStore.Release(tx.From, data.CollateralSymbol, data.CollateralAmount) {
		return
	}

	id := "loanreq-" + tx.ID[:min(8, len(tx.ID))]
	loanRequestStore.Create(&core.LoanRequest{
		ID:                 id,
		Borrower:           tx.From,
		BorrowerPublicKey:  tx.AuthPublicKey,
		RequestedSymbol:    data.RequestedSymbol,
		Amount:             data.Amount,
		CollateralSymbol:   data.CollateralSymbol,
		CollateralAmount:   data.CollateralAmount,
		MaxInterestPercent: data.MaxInterestPercent,
		DurationSeconds:    data.DurationSeconds,
		Purpose:            data.Purpose,
		Status:             core.LoanRequestOpen,
		CreatedAt:          tx.Timestamp,
	})
}

// applyLoanRequestCancel releases the reserved collateral back to the
// borrower's general locked pool - mirrors applyLoanOfferClose's
// refund, just against CollateralStore instead of a token escrow.
func applyLoanRequestCancel(tx core.Transaction) {
	var data loanRequestCancelData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	request, ok := loanRequestStore.Cancel(data.RequestID, tx.From)
	if !ok {
		return
	}

	collateralStore.Add(tx.From, request.CollateralSymbol, request.CollateralAmount)
}

// applyLoanRequestFulfill is the lender-initiated mirror of
// applyLoanOfferDraw - see core.Negotiation's RequestID field and
// TxLoanRequestFulfill's verifyTxAuth case for why the signed payload
// here is just "I confirm this negotiation" rather than the economic
// terms directly: the real terms (RequestID, RatePercent) live on the
// Negotiation record, which only the lender it names can ever execute.
// Order matters below - the fund transfer is attempted before
// LoanRequestStore.Fulfill mutates anything, so a failed transfer
// (insufficient lender balance) leaves no partial state to unwind.
func applyLoanRequestFulfill(tx core.Transaction) {
	var data negotiationActionData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	n, ok := negotiationStore.Get(data.NegotiationID)
	if !ok || n.RequestID == "" || n.Lender != tx.From {
		return
	}

	request, ok := loanRequestStore.Get(n.RequestID)
	if !ok || request.Status != core.LoanRequestOpen {
		return
	}
	if n.RatePercent == 0 || n.RatePercent > request.MaxInterestPercent {
		return
	}

	store, err := resolveTokenStore(request.RequestedSymbol)
	if err != nil {
		return
	}

	lenderSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, request.RequestedSymbol)
	borrowerSide := resolveBalanceAddress(request.Borrower, request.BorrowerPublicKey, request.RequestedSymbol)
	if !store.Transfer(lenderSide, borrowerSide, request.Amount) {
		return
	}

	if _, ok := loanRequestStore.Fulfill(request.ID); !ok {
		// Extremely unlikely (would mean the request was cancelled/
		// fulfilled by something else between the read above and here,
		// impossible within this single-threaded apply pass, but
		// checked rather than assumed) - the funds already moved, so
		// refund rather than leave them stuck at the borrower with no
		// loan record.
		store.Transfer(borrowerSide, lenderSide, request.Amount)
		return
	}

	id := "loan-" + tx.ID[:min(8, len(tx.ID))]
	loanStore.Add(&core.Loan{
		ID:               id,
		Borrower:         request.Borrower,
		Lender:           tx.From,
		LenderPublicKey:  tx.AuthPublicKey,
		Symbol:           request.RequestedSymbol,
		Amount:           request.Amount,
		Interest:         n.RatePercent,
		Status:           core.LoanFunded,
		CollateralSymbol: request.CollateralSymbol,
		CollateralAmount: request.CollateralAmount,
		DueAt:            dueAtFor(request.DurationSeconds, tx.Timestamp),
		CreatedAt:        tx.Timestamp,
	})

	creditHistory.Add(request.Borrower, core.CreditEvent{Type: "BORROW", Amount: request.Amount})
	core.RecordLoanTaken(creditStore.Get(request.Borrower))

	// Lender-side reputation/profile reward - same inlined pattern
	// applyLoanOfferDraw uses (see its own comment on why
	// creditEngine.FundLoan isn't reused here either).
	creditEngine.Profiles.Get(tx.From).RecordFunding(request.Amount)
	if reward := core.IncreaseWalletReputation(walletStore.Get(tx.From), core.ReputationDeltaLoanFunded); reward > 0 {
		mintRept(tx.From, reward)
	}
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: tx.From, Delta: int64(core.ReputationDeltaLoanFunded),
		Reason: "Funded a loan (borrower request)", Timestamp: tx.Timestamp,
	})
}

func applyModeratorRegister(tx core.Transaction) {
	// The fee may have moved between submission and confirmation -
	// re-checked and only actually deducted here, never trusted from
	// submission time alone (same pattern as applyTokenRegister's
	// ETHTEST creation fee).
	if !tokenStore.Transfer(tx.From, registrationFeeAddress(), ModeratorRegisterFeeCCT) {
		return
	}

	if !moderatorStore.Register(tx.From, tx.AuthPublicKey, tx.Timestamp) {
		return
	}

	wallet := walletStore.Get(tx.From)
	if reward := core.IncreaseWalletReputation(wallet, core.ModeratorReputationBonus); reward > 0 {
		mintRept(tx.From, reward)
	}
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: tx.From, Delta: int64(core.ModeratorReputationBonus),
		Reason: "Registered as a moderator (one-time)", Timestamp: tx.Timestamp,
	})
}

func applyModeratorConfirm(tx core.Transaction) {
	var data moderationConfirmData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	var ok bool
	switch data.Kind {
	case "negotiation":
		ok = negotiationStore.Moderate(data.ID, tx.From, data.Disputed)
	case "p2p":
		ok = p2pOfferStore.Moderate(data.ID, tx.From, data.Disputed)
	}
	if !ok {
		return
	}

	moderatorStore.RecordConfirmation(tx.From, data.Disputed)

	// Only a genuine confirmation grows the moderator's own reputation -
	// a dispute is still legitimate, necessary work, but rewarding it
	// identically to a confirmation would incentivize disputing trades
	// just to farm reputation.
	if !data.Disputed {
		wallet := walletStore.Get(tx.From)
		if reward := core.IncreaseWalletReputation(wallet, core.ModeratorConfirmReputationBonus); reward > 0 {
			mintRept(tx.From, reward)
		}
		reputationEventStore.Record(&core.ReputationEvent{
			TxID: tx.ID, Target: tx.From, Delta: int64(core.ModeratorConfirmReputationBonus),
			Reason: "Confirmed an undisputed trade as moderator", Timestamp: tx.Timestamp,
		})
	}
}

func applyKYCSubmit(tx core.Transaction) {
	var data kycSubmitData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if len(data.FullName) == 0 || len(data.FullName) > 200 {
		return
	}
	if len(data.DateOfBirth) == 0 || len(data.DateOfBirth) > 20 {
		return
	}
	if len(data.Country) == 0 || len(data.Country) > 100 {
		return
	}
	if !data.AgreedTerms {
		return
	}

	kycStore.Submit(tx.From, data.FullName, data.DateOfBirth, data.Country, data.AgreedTerms, tx.Timestamp)
}

func applyCompanyApply(tx core.Transaction) {
	var data companyApplyData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if len(data.Name) == 0 || len(data.Name) > 200 {
		return
	}

	// The fee may have moved between submission and confirmation -
	// re-checked and only actually deducted here, never trusted from
	// submission time alone (same pattern as applyTokenRegister's
	// ETHTEST creation fee).
	if !tokenStore.Transfer(tx.From, registrationFeeAddress(), HubApplyFeeCCT) {
		return
	}

	companyStore.Apply(&core.CompanyApplication{
		Address:      tx.From,
		PublicKey:    tx.AuthPublicKey,
		Name:         data.Name,
		Description:  data.Description,
		DocumentNote: data.DocumentNote,
		ContactEmail: data.ContactEmail,
		Status:       core.CompanyPending,
		// Assigned here (block-confirmation time), not in the HTTP
		// handler, for the same determinism reasoning as every other
		// moderator assignment (see core.ModeratorStore.AssignNext).
		ModeratorAddress: moderatorStore.AssignNext(),
		CreatedAt:        tx.Timestamp,
	})
}

func applyCompanyUpdateContact(tx core.Transaction) {
	var data companyUpdateContactData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if len(data.ContactEmail) > 200 {
		return
	}

	companyStore.SetContactEmail(tx.From, data.ContactEmail)
}

func applyCompanyReview(tx core.Transaction) {
	var data companyReviewData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	companyStore.Review(data.Address, tx.From, data.Approve, tx.Timestamp)
}

func applyCompanyReassignModerator(tx core.Transaction) {
	var data companyReassignData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	// Re-verified at confirmation time, not just trusted from
	// submission time - the caller may have deregistered (if that ever
	// becomes possible) between submission and confirmation.
	if !moderatorStore.IsModerator(tx.From) {
		return
	}

	companyStore.ReassignModerator(data.Address, tx.From)
}

func applyBuyRequestCreate(tx core.Transaction) {
	var data buyRequestCreateData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.WantAmount == 0 || data.PayAmount == 0 || len(data.PaySymbol) == 0 || len(data.PaySymbol) > 20 {
		return
	}
	if _, err := resolveTokenStore(data.WantSymbol); err != nil {
		return
	}
	// Verification may have changed between submission and
	// confirmation - re-checked here, not just at submission time.
	if !companyStore.IsVerified(tx.From) {
		return
	}

	buyRequestStore.Create(&core.BuyRequest{
		ID:           tx.ID,
		Hub:          tx.From,
		HubPublicKey: tx.AuthPublicKey,
		WantSymbol:   data.WantSymbol,
		WantAmount:   data.WantAmount,
		PaySymbol:    data.PaySymbol,
		PayAmount:    data.PayAmount,
		Status:       core.BuyRequestOpen,
		CreatedAt:    tx.Timestamp,
	})
}

// applyBuyRequestFulfill is the buy-side counterpart to
// applyP2POfferCreate - the fulfilling user (tx.From) becomes the
// escrow depositor exactly as if they'd created a normal EXTERNAL P2P
// offer themselves, with the hub pre-set as the accepted counterparty
// (fulfillment collapses "create" and "accept" into one step, since
// the terms were already fixed by the hub's standing bid). From here
// on it's the identical seller-confirm/buyer-confirm/moderator-
// resolve settlement flow as any other EXTERNAL offer - the fulfiller
// plays the "seller" role (they escrowed the crypto and receive
// payment), the hub plays the "buyer" role (they pay and receive the
// crypto), matching exactly who does what in the real trade.
func applyBuyRequestFulfill(tx core.Transaction) {
	var data buyRequestActionData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	request, ok := buyRequestStore.Get(data.RequestID)
	if !ok || request.Status != core.BuyRequestOpen || request.Hub == tx.From {
		return
	}

	wantStore, err := resolveTokenStore(request.WantSymbol)
	if err != nil {
		return
	}

	offerID := tx.ID
	escrow := p2pOfferEscrowAddress(offerID)
	fulfillerSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, request.WantSymbol)

	if !wantStore.Transfer(fulfillerSide, escrow, request.WantAmount) {
		return
	}

	if _, ok := buyRequestStore.Fulfill(request.ID, offerID); !ok {
		// Someone else's fulfillment landed first - refund immediately
		// rather than leaving the fulfiller's crypto stuck in escrow
		// for nothing.
		wantStore.Transfer(escrow, fulfillerSide, request.WantAmount)
		return
	}

	p2pOfferStore.Create(&core.P2POffer{
		ID:                   offerID,
		Creator:              tx.From,
		CreatorPublicKey:     tx.AuthPublicKey,
		OfferSymbol:          request.WantSymbol,
		OfferAmount:          request.WantAmount,
		WantSymbol:           request.PaySymbol,
		WantAmount:           request.PayAmount,
		PaymentMode:          core.PaymentModeExternal,
		Status:               core.OfferAccepted,
		AcceptedBy:           request.Hub,
		AcceptedByPublicKey:  request.HubPublicKey,
		SettlementStatus:     core.SettlementAwaitingPayment,
		AwaitingPaymentSince: tx.Timestamp,
		CreatedAt:            tx.Timestamp,
		ModeratorAddress:     moderatorStore.AssignNext(),
		ModerationStatus:     core.ModerationPending,
	})
}

func applyBuyRequestCancel(tx core.Transaction) {
	var data buyRequestActionData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	buyRequestStore.Cancel(data.RequestID, tx.From)
}

func applyHubRateSet(tx core.Transaction) {
	var data hubRateSetData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	// Worker/operator delegation (or lack of it) may have changed
	// between submission and confirmation - re-checked here, not just
	// trusted from submission time.
	hub, ok := authorizedHubActor(data.Hub, tx.From)
	if !ok {
		return
	}
	// When the hub posts directly, this is the hub's own flagship
	// listing and HubPublicKey is the hub's own key (tx.AuthPublicKey
	// already IS the hub's key in that case). When an active worker
	// posts instead, this is now THAT WORKER'S OWN independent listing
	// (OperatorAddress set) - a separate sub-marketplace with its own
	// inventory, not an edit to the hub's shared rate - so
	// HubPublicKey must be the OPERATOR's own key (also
	// tx.AuthPublicKey, since tx.From is the operator here), never
	// looked up from the hub's company record.
	operatorAddress := ""
	if hub != tx.From {
		operatorAddress = tx.From
	}
	hubPublicKey := tx.AuthPublicKey
	if _, err := resolveTokenStore(data.Symbol); err != nil {
		return
	}
	if len(data.FiatSymbol) == 0 || len(data.FiatSymbol) > 20 {
		return
	}
	if data.SellRate == 0 && data.BuyRate == 0 {
		return
	}

	if data.Mode == "ONCHAIN" {
		if data.FiatSymbol == data.Symbol {
			return
		}
		if _, err := resolveTokenStore(data.FiatSymbol); err != nil {
			return
		}
	} else if data.Mode != "" && data.Mode != "OFFAPP" {
		return
	}

	hubRateStore.Set(&core.HubRate{
		Hub:             hub,
		HubPublicKey:    hubPublicKey,
		Symbol:          data.Symbol,
		FiatSymbol:      data.FiatSymbol,
		SellRate:        data.SellRate,
		BuyRate:         data.BuyRate,
		Active:          data.Active,
		Mode:            data.Mode,
		UpdatedAt:       tx.Timestamp,
		PostedBy:        tx.From,
		OperatorAddress: operatorAddress,
	})
}

// applyHubRulesSet posts/replaces a hub's preset trading rules text -
// see core.HubRules's doc comment.
func applyHubRulesSet(tx core.Transaction) {
	var data hubRulesSetData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if !companyStore.IsVerified(tx.From) {
		return
	}
	if len(data.Text) > 4000 {
		return
	}

	hubRulesStore.Set(tx.From, data.Text, data.PaymentWindowMinutes, data.MinReputationToApply, data.CannedReplies, tx.Timestamp)
}

// applyHubWorkerAdd delegates day-to-day pricing to another wallet -
// only the hub's own signature authorizes this (see core.HubWorker's
// doc comment), re-checked here rather than trusted from submission
// time in case verification lapsed in between.
func applyHubWorkerAdd(tx core.Transaction) {
	var data hubWorkerAddData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if !companyStore.IsVerified(tx.From) {
		return
	}
	if data.WorkerAddress == "" || data.WorkerPublicKey == "" {
		return
	}
	pubBytes, err := hex.DecodeString(data.WorkerPublicKey)
	if err != nil || core.GenerateAddress(pubBytes) != data.WorkerAddress {
		return
	}

	hubWorkerStore.Add(&core.HubWorker{
		HubAddress:      tx.From,
		WorkerAddress:   data.WorkerAddress,
		WorkerPublicKey: data.WorkerPublicKey,
		Name:            data.Name,
		Active:          true,
		AddedAt:         tx.Timestamp,
	})
}

func applyHubWorkerRemove(tx core.Transaction) {
	var data hubWorkerRemoveData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if !companyStore.IsVerified(tx.From) {
		return
	}

	hubWorkerStore.Remove(tx.From, data.WorkerAddress)
}

// applyHubStorefrontSet posts/replaces a hub's custom front-page
// design - see core.HubStorefront's doc comment for why this is only
// ever rendered client-side inside a sandboxed iframe.
func applyHubStorefrontSet(tx core.Transaction) {
	var data hubStorefrontSetData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if !companyStore.IsVerified(tx.From) {
		return
	}
	if len(data.HTML) > core.HubStorefrontMaxBytes {
		return
	}

	hubStorefrontStore.Set(tx.From, data.HTML, tx.Timestamp)
}

// settleHubTradeOnChain performs an ONCHAIN-mode hub trade atomically,
// in one step, with no escrow and nothing left to settle - both legs
// (Symbol and the counter token, FiatSymbol repurposed as a real token
// under Mode=="ONCHAIN", see core.HubRate's doc comment) move directly
// between hub and requester right here. A P2POffer record is still
// created afterward purely as trade history (same list a trader
// already checks for their EXTERNAL trades), created pre-settled
// (Status Accepted, no SettlementStatus - see PaymentMode's doc
// comment: "" SettlementStatus is exactly what every other ONCHAIN
// offer already uses) so the UI never renders a chat/settlement action
// for it.
func settleHubTradeOnChain(tx core.Transaction, data hubTradeRequestData, rate *core.HubRate) {
	symbolStore, err := resolveTokenStore(data.Symbol)
	if err != nil {
		return
	}
	counterStore, err := resolveTokenStore(rate.FiatSymbol)
	if err != nil {
		return
	}

	offerID := tx.ID
	hubSymbolSide := resolveBalanceAddress(rate.InventoryAddress(), rate.HubPublicKey, data.Symbol)
	hubCounterSide := resolveBalanceAddress(rate.InventoryAddress(), rate.HubPublicKey, rate.FiatSymbol)
	userSymbolSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, data.Symbol)
	userCounterSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, rate.FiatSymbol)

	var creator, acceptedBy, acceptedByPublicKey, creatorPublicKey string
	var counterAmount uint64

	switch data.Direction {
	case "BUY_FROM_HUB":
		// User receives Symbol from the hub, pays counterAmount of
		// FiatSymbol to the hub - both legs must succeed together;
		// if the second leg fails, the first is reversed rather than
		// leaving the hub's tokens gone with nothing paid for them.
		if rate.SellRate == 0 {
			return
		}
		counterAmount = data.Amount * rate.SellRate / core.HubRateScale
		if symbolStore.BalanceOf(hubSymbolSide) < data.Amount {
			return
		}
		if counterStore.BalanceOf(userCounterSide) < counterAmount {
			return
		}
		if !symbolStore.Transfer(hubSymbolSide, userSymbolSide, data.Amount) {
			return
		}
		if !counterStore.Transfer(userCounterSide, hubCounterSide, counterAmount) {
			// Refund the first leg - no atomic multi-store transaction
			// primitive exists in this codebase (same disclosed
			// limitation as every other two-step mutation here, e.g.
			// applyLoanOfferDraw's collateral-reservation refund).
			symbolStore.Transfer(userSymbolSide, hubSymbolSide, data.Amount)
			return
		}
		// Both legs are committed now - only from here is this a real,
		// final trade, so the platform fee is taken retroactively on
		// each side (skimmed out of what was just received, never out
		// of the reversible first leg before the second leg was known
		// to succeed - see the refund above).
		skimPlatformFee(symbolStore, userSymbolSide, data.Amount)
		skimPlatformFee(counterStore, hubCounterSide, counterAmount)
		creator, creatorPublicKey = rate.InventoryAddress(), rate.HubPublicKey
		acceptedBy, acceptedByPublicKey = tx.From, tx.AuthPublicKey

	case "SELL_TO_HUB":
		// User pays Symbol, receives counterAmount of FiatSymbol from
		// the hub.
		if rate.BuyRate == 0 {
			return
		}
		counterAmount = data.Amount * rate.BuyRate / core.HubRateScale
		if symbolStore.BalanceOf(userSymbolSide) < data.Amount {
			return
		}
		if counterStore.BalanceOf(hubCounterSide) < counterAmount {
			return
		}
		if !symbolStore.Transfer(userSymbolSide, hubSymbolSide, data.Amount) {
			return
		}
		if !counterStore.Transfer(hubCounterSide, userCounterSide, counterAmount) {
			symbolStore.Transfer(hubSymbolSide, userSymbolSide, data.Amount)
			return
		}
		skimPlatformFee(symbolStore, hubSymbolSide, data.Amount)
		skimPlatformFee(counterStore, userCounterSide, counterAmount)
		creator, creatorPublicKey = tx.From, tx.AuthPublicKey
		acceptedBy, acceptedByPublicKey = rate.InventoryAddress(), rate.HubPublicKey

	default:
		return
	}

	p2pOfferStore.Create(&core.P2POffer{
		ID:                  offerID,
		Creator:             creator,
		CreatorPublicKey:    creatorPublicKey,
		OfferSymbol:         data.Symbol,
		OfferAmount:         data.Amount,
		WantSymbol:          rate.FiatSymbol,
		WantAmount:          counterAmount,
		PaymentMode:         core.PaymentModeOnChain,
		Status:              core.OfferAccepted,
		AcceptedBy:          acceptedBy,
		AcceptedByPublicKey: acceptedByPublicKey,
		CreatedAt:           tx.Timestamp,
		HubAddress:          data.Hub,
	})
}

// applyHubTradeRequest instantiates and immediately settles-into-
// escrow a real EXTERNAL P2POffer from a hub's standing rate, for
// whatever amount the requester chose - the buyer's own request is
// both "create" and "accept" in one step (there's no separate hub
// confirmation before escrow: posting an Active rate IS the hub's
// standing pre-authorization, same as an AMM pool's liquidity provider
// isn't asked again for each individual swap). From this point on the
// resulting core.P2POffer is completely ordinary EXTERNAL-mode state -
// settles through the exact same seller-confirm/buyer-confirm/
// moderator-resolve flow as any other hub offer or fulfilled buy
// request (see applyBuyRequestFulfill, whose Creator/AcceptedBy
// role-assignment this mirrors exactly for the SELL_TO_HUB direction).
func applyHubTradeRequest(tx core.Transaction) {
	var data hubTradeRequestData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.Amount == 0 || data.Hub == tx.From || data.Operator == tx.From {
		return
	}
	if data.Direction != "BUY_FROM_HUB" && data.Direction != "SELL_TO_HUB" {
		return
	}

	rate, ok := hubRateStore.Get(data.Hub, data.Symbol, data.Operator, data.FiatSymbol)
	if !ok || !rate.Active {
		return
	}
	// Re-verify hub is still a verified Trading Hub - and, for an
	// operator's own listing, that the operator is still an active
	// worker of it - at confirmation time, not just when the rate was
	// posted.
	if !companyStore.IsVerified(data.Hub) {
		return
	}
	if data.Operator != "" && !hubWorkerStore.IsActiveWorker(data.Hub, data.Operator) {
		return
	}

	if rate.IsOnChain() {
		settleHubTradeOnChain(tx, data, rate)
		return
	}

	store, err := resolveTokenStore(data.Symbol)
	if err != nil {
		return
	}

	offerID := tx.ID
	escrow := p2pOfferEscrowAddress(offerID)

	switch data.Direction {
	case "BUY_FROM_HUB":
		// The user is buying Symbol FROM the hub, paying fiat - the
		// hub's own balance is escrowed here (moved without a fresh
		// per-trade hub signature, since the hub's Active rate is
		// itself the standing authorization for exactly this).
		if rate.SellRate == 0 {
			return
		}
		fiatAmount := data.Amount * rate.SellRate / core.HubRateScale
		hubSide := resolveBalanceAddress(rate.InventoryAddress(), rate.HubPublicKey, data.Symbol)
		// The LIVE balance (hub's own, or the operator's own for an
		// operator listing) is the range - re-checked here, not trusted
		// from submission time, and never a pre-fixed amount (see
		// core.HubRate's doc comment).
		if store.BalanceOf(hubSide) < data.Amount {
			return
		}
		if !store.Transfer(hubSide, escrow, data.Amount) {
			return
		}

		p2pOfferStore.Create(&core.P2POffer{
			ID:                   offerID,
			Creator:              rate.InventoryAddress(),
			CreatorPublicKey:     rate.HubPublicKey,
			OfferSymbol:          data.Symbol,
			OfferAmount:          data.Amount,
			WantSymbol:           rate.FiatSymbol,
			WantAmount:           fiatAmount,
			PaymentMode:          core.PaymentModeExternal,
			Status:               core.OfferAccepted,
			AcceptedBy:           tx.From,
			AcceptedByPublicKey:  tx.AuthPublicKey,
			SettlementStatus:     core.SettlementAwaitingPayment,
			AwaitingPaymentSince: tx.Timestamp,
			CreatedAt:            tx.Timestamp,
			ModeratorAddress:     moderatorStore.AssignNext(),
			ModerationStatus:     core.ModerationPending,
			HubAddress:           data.Hub,
		})

	case "SELL_TO_HUB":
		// The user is selling Symbol TO the hub - their own balance is
		// escrowed, fully authorized by their own signature on this
		// very transaction (no special hub pre-authorization needed).
		if rate.BuyRate == 0 {
			return
		}
		fiatAmount := data.Amount * rate.BuyRate / core.HubRateScale
		userSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, data.Symbol)
		if store.BalanceOf(userSide) < data.Amount {
			return
		}
		if !store.Transfer(userSide, escrow, data.Amount) {
			return
		}

		p2pOfferStore.Create(&core.P2POffer{
			ID:                   offerID,
			Creator:              tx.From,
			CreatorPublicKey:     tx.AuthPublicKey,
			OfferSymbol:          data.Symbol,
			OfferAmount:          data.Amount,
			WantSymbol:           rate.FiatSymbol,
			WantAmount:           fiatAmount,
			PaymentMode:          core.PaymentModeExternal,
			Status:               core.OfferAccepted,
			AcceptedBy:           rate.InventoryAddress(),
			AcceptedByPublicKey:  rate.HubPublicKey,
			SettlementStatus:     core.SettlementAwaitingPayment,
			AwaitingPaymentSince: tx.Timestamp,
			CreatedAt:            tx.Timestamp,
			ModeratorAddress:     moderatorStore.AssignNext(),
			ModerationStatus:     core.ModerationPending,
			HubAddress:           data.Hub,
		})
	}
}

func applySetRecoveryEmail(tx core.Transaction) {
	var data recoveryEmailRequestData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if len(data.Email) == 0 || len(data.Email) > 200 {
		return
	}

	recoveryStore.SetEmail(tx.From, data.Email)
}

// migrateWalletAssets moves everything keyed by the raw address
// (CCT, REPT, staked ETH, reputation/tier-progress) from oldAddress to
// newAddress. ETHTEST/USDCTEST live at a derived sub-address keyed by
// PUBLIC KEY, not the raw address (see core.DeriveTokenAddress) - only
// migrated if the caller supplied and proved the old public key (see
// HandleRecoverWallet's doc comment for why that's optional and what
// gets left behind if it's omitted).
func migrateWalletAssets(oldAddress, newAddress, newPublicKey, oldPublicKeyHex string) {
	if bal := tokenStore.BalanceOf(oldAddress); bal > 0 {
		tokenStore.Transfer(oldAddress, newAddress, bal)
	}
	if bal := reptStore.BalanceOf(oldAddress); bal > 0 {
		reptStore.Transfer(oldAddress, newAddress, bal)
	}

	// Rewrite any open loan's Borrower/Lender pointing at the old
	// address - LoanStore.Loans is a plain unindexed map (confirmed via
	// core/loan_store.go), so this is a bounded full scan, not a
	// lookup; harmless no-op for addresses with no loans.
	for _, loan := range loanStore.Loans {
		if loan.Borrower == oldAddress {
			loan.Borrower = newAddress
		}
		if loan.Lender == oldAddress {
			loan.Lender = newAddress
		}
	}

	// Escrowed balance itself doesn't need to move - each symbol's
	// escrow (see stakeEscrowAddress) is a single shared pool, not
	// per-account, so only the ledger entry recording who it belongs to
	// needs to move.
	for symbol, amount := range stakingStore.Stakes[oldAddress] {
		if amount == 0 {
			continue
		}
		if stakingStore.Stakes[newAddress] == nil {
			stakingStore.Stakes[newAddress] = make(map[string]uint64)
		}
		stakingStore.Stakes[newAddress][symbol] += amount
	}
	stakingStore.Stakes[oldAddress] = nil

	oldWallet := walletStore.Get(oldAddress)
	newWallet := walletStore.Get(newAddress)
	newWallet.Reputation = oldWallet.Reputation
	newWallet.HighestTierReached = oldWallet.HighestTierReached
	oldWallet.Reputation = 0
	oldWallet.HighestTierReached = ""
	stakingStore.SyncWallet(newWallet, newAddress)
	stakingStore.SyncWallet(oldWallet, oldAddress)

	if oldPublicKeyHex == "" {
		return
	}
	oldPub, err := hex.DecodeString(oldPublicKeyHex)
	if err != nil || core.GenerateAddress(oldPub) != oldAddress {
		return
	}
	newPub, err := hex.DecodeString(newPublicKey)
	if err != nil {
		return
	}

	oldEth := core.DeriveTokenAddress(oldPub, core.ETHTestAddressTag)
	newEth := core.DeriveTokenAddress(newPub, core.ETHTestAddressTag)
	if bal := ethTestStore.BalanceOf(oldEth); bal > 0 {
		ethTestStore.Transfer(oldEth, newEth, bal)
	}

	oldUsdc := core.DeriveTokenAddress(oldPub, core.USDCTestAddressTag)
	newUsdc := core.DeriveTokenAddress(newPub, core.USDCTestAddressTag)
	if bal := usdcTestStore.BalanceOf(oldUsdc); bal > 0 {
		usdcTestStore.Transfer(oldUsdc, newUsdc, bal)
	}

	oldYen := core.DeriveTokenAddress(oldPub, core.YenAddressTag)
	newYen := core.DeriveTokenAddress(newPub, core.YenAddressTag)
	if bal := yenStore.BalanceOf(oldYen); bal > 0 {
		yenStore.Transfer(oldYen, newYen, bal)
	}
}

func applyRecoverWallet(tx core.Transaction) {
	var data recoverWalletData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	// Every check here is re-verified deterministically at apply time,
	// not trusted from submission time - see HandleRecoverWallet's
	// fast-fail checks for the submission-time versions of the same
	// logic.
	if !recoveryStore.IsRecoverable(data.OldAddress, data.Email) {
		return
	}
	oldWallet := walletStore.Get(data.OldAddress)
	if oldWallet.CreatedAt == 0 || time.Unix(oldWallet.CreatedAt, 0).UTC().Year() != data.ClaimedYear {
		return
	}
	if !recoveryStore.MarkMigrated(data.OldAddress, tx.From) {
		return
	}

	migrateWalletAssets(data.OldAddress, tx.From, tx.AuthPublicKey, data.OldPublicKey)
}

type hdWalletMigrateData struct {
	NewAddress   string `json:"newAddress"`
	NewPublicKey string `json:"newPublicKey"`
}

// applyHDWalletMigrate moves a pre-Multi-Chain-HD-Wallet legacy
// address's assets to a new HD-derived address. tx.From/tx.AuthAddress
// is the OLD address (verifyTxAuth already required its signature) -
// unlike applyRecoverWallet, there's no email/security-question check
// here because the old key itself is the proof of ownership.
func applyHDWalletMigrate(tx core.Transaction) {
	var data hdWalletMigrateData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	newPub, err := hex.DecodeString(data.NewPublicKey)
	if err != nil || core.GenerateAddress(newPub) != data.NewAddress {
		return
	}

	if !recoveryStore.MarkMigrated(tx.From, data.NewAddress) {
		return
	}

	// tx.AuthPublicKey is the OLD address's public key (it's who signed
	// this transaction) - always available here, unlike
	// applyRecoverWallet's optional OldPublicKey field, so ETHTEST/
	// USDCTEST/YENE sub-addresses and loan Borrower/Lender always move.
	migrateWalletAssets(tx.From, data.NewAddress, data.NewPublicKey, tx.AuthPublicKey)
}

type repayData struct {
	LoanID string `json:"loanId"`
	Mode   string `json:"mode"` // "direct" (/repay) or "p2p" (/loans/repay)
}

// repayAmountFor is the principal plus interest (a whole-percent
// rate, e.g. Interest=12 means 12%) - what actually has to move for a
// loan to be considered repaid, not just its bare principal.
func repayAmountFor(loan *core.Loan) uint64 {
	return loan.Amount + (loan.Amount*loan.Interest)/100
}

// loanSymbol is loan.Symbol with a fallback for loans recorded before
// that field existed - those were always USDCTEST (the only ledger
// self-funded borrowing has ever minted into).
func loanSymbol(loan *core.Loan) string {
	if loan.Symbol == "" {
		return "YENE"
	}
	return loan.Symbol
}

// settleRepayment is what used to be entirely missing: repaying a
// loan actually moving value, not just flipping its status. A
// self-funded loan (Lender == "", see core.Loan's doc comment) has no
// real lender to pay - repayment burns the amount instead, undoing
// the supply that was minted out of thin air when it was borrowed. A
// peer-funded loan (drawn against a LoanOffer) transfers the full
// amount to the lender's own resolved address, in whichever token the
// loan was denominated in. Returns false - moving nothing - if the
// borrower can't actually afford it, so a loan can never be marked
// repaid while still unpaid.
func settleRepayment(loan *core.Loan, tx core.Transaction) bool {
	symbol := loanSymbol(loan)

	store, err := resolveTokenStore(symbol)
	if err != nil {
		return false
	}

	borrowerSide := resolveBalanceAddress(loan.Borrower, tx.AuthPublicKey, symbol)
	amount := repayAmountFor(loan)

	if loan.Lender == "" {
		return store.Burn(borrowerSide, amount)
	}

	lenderSide := resolveBalanceAddress(loan.Lender, loan.LenderPublicKey, symbol)
	return store.Transfer(borrowerSide, lenderSide, amount)
}

func applyRepay(tx core.Transaction) {
	var data repayData
	_ = json.Unmarshal([]byte(tx.Data), &data)

	loan, ok := loanStore.Get(data.LoanID)
	if !ok {
		return
	}
	// Already repaid (or defaulted) - re-checked here so repaying the
	// same loan twice can't unlock collateral or move funds twice.
	if loan.Status != core.LoanFunded {
		return
	}

	if !settleRepayment(loan, tx) {
		return
	}

	// Released for both modes, unconditionally - this earmarked
	// collateral belongs to this loan alone regardless of which path
	// funded it, and goes back to the exact staked CCT / oETH balance
	// it was reserved from (see reserveCollateralForLoan), never to
	// liquid CCT and never in an amount just assumed to equal the loan
	// principal.
	releaseLoanCollateral(loan)

	if data.Mode == "p2p" {
		if reward := creditEngine.RepayLoan(loan); reward > 0 {
			mintRept(loan.Borrower, reward)
		}
		reputationEventStore.Record(&core.ReputationEvent{
			TxID: tx.ID, Target: loan.Borrower, Delta: int64(core.ReputationDeltaLoanRepaidOnTime),
			Reason: "Repaid a peer-funded loan", Timestamp: tx.Timestamp,
		})
		return
	}

	loan.Status = core.LoanRepaid
	loan.RepaidAt = tx.Timestamp

	profile := creditStore.Get(loan.Borrower)
	core.RecordLoanRepaid(profile)
	creditHistory.Add(loan.Borrower, core.CreditEvent{Type: "REPAY", Amount: loan.Amount})
}

type lendData struct {
	LoanID string `json:"loanId"`
}

func applyFundLoan(tx core.Transaction) {
	var data lendData
	_ = json.Unmarshal([]byte(tx.Data), &data)

	loan, ok := loanStore.Get(data.LoanID)
	if !ok {
		return
	}

	if reward := creditEngine.FundLoan(loan, tx.From); reward > 0 {
		mintRept(tx.From, reward)
	}
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: tx.From, Delta: int64(core.ReputationDeltaLoanFunded),
		Reason: "Funded a loan", Timestamp: tx.Timestamp,
	})

	stats := lenderStore.Get(tx.From)
	stats.LoansFunded++
	stats.AmountLent += loan.Amount
	stats.InterestEarned += loan.Interest
}

type governanceData struct {
	Action      string `json:"action"` // "create", "vote", or "activate"
	Title       string `json:"title"`
	Description string `json:"description"`
	ProposalID  string `json:"proposalId"`
	Support     bool   `json:"support"`

	// Kind/ParamKey/ParamValue/TargetVersion are only read by the
	// "create" action, and only meaningful when Kind is non-empty -
	// see core.ProposalKind's doc comment and
	// processProposalFinalization for what a PASSED outcome of each
	// kind actually does.
	Kind          string `json:"kind,omitempty"`
	ParamKey      string `json:"paramKey,omitempty"`
	ParamValue    uint64 `json:"paramValue,omitempty"`
	TargetVersion string `json:"targetVersion,omitempty"`

	// SystemGenerated is only ever set true by processAutoProposal's
	// own self-signed "create" transaction - never trusted from a
	// real user-submitted request (HandleCreateProposal never sets
	// it), so a wallet can't fake a system-generated badge on its own
	// proposal.
	SystemGenerated bool `json:"systemGenerated,omitempty"`
}

func applyGovernance(tx core.Transaction) {
	var data governanceData
	_ = json.Unmarshal([]byte(tx.Data), &data)

	switch data.Action {
	case "create":
		id := "prop-" + tx.ID[:min(8, len(tx.ID))]
		proposalStore.Create(&core.Proposal{
			ID:              id,
			Title:           data.Title,
			Description:     data.Description,
			CreatedBy:       tx.From,
			Open:            true,
			Quorum:          core.DefaultProposalQuorum,
			Deadline:        tx.Timestamp + core.ProposalVotingWindowSeconds,
			Kind:            core.ProposalKind(data.Kind),
			ParamKey:        data.ParamKey,
			ParamValue:      data.ParamValue,
			TargetVersion:   data.TargetVersion,
			SystemGenerated: data.SystemGenerated,
		})

	case "vote":
		// Voting power is read fresh at confirmation time, not at
		// submission time - your vote's weight reflects your
		// reputation/stake when it actually lands in a block. Zero
		// means the wallet never activated governance at all (see
		// votingWeight's doc comment) - HandleVoteProposal already
		// pre-checks this, but the apply-side check is what's actually
		// enforced, same as every other consensus-applied guard in
		// this file.
		power := votingWeight(tx.From)
		if power == 0 {
			return
		}

		_, _ = proposalStore.Vote(data.ProposalID, tx.From, data.Support, power, tx.Timestamp)

	case "activate":
		// "Activated governance" is defined as VotingPower > 0 - this
		// field already existed but was never set anywhere (always 0),
		// so this is the one place that turns it on. Idempotent: never
		// lowers an already-nonzero value back down. See
		// HandlePostGovernanceStatus, which gates status posting on
		// this same flag.
		wallet := walletStore.Get(tx.From)
		if wallet.VotingPower == 0 {
			wallet.VotingPower = 1
		}
	}
}

// ProposalPassedProposerReward/ProposalPassedVoterReward are the real
// incentives for a proposal that reaches quorum and passes - see
// processProposalFinalization. Matches the existing Gold-tier REPT
// reward's order of magnitude (core/reputation_reward.go) rather than
// a value invented from nothing. Nothing mints on a FAILED outcome -
// only a proposal that actually mobilized enough real weight and
// passed pays out, so this doesn't incentivize bandwagon-voting or
// spamming proposals that never clear quorum.
const (
	ProposalPassedProposerReward = 50
	ProposalPassedVoterReward    = 5
)

// processProposalFinalization sweeps every still-open proposal whose
// deadline has passed and closes it - same block-timestamp-driven
// determinism as processLoanDefaults/processDormantWallets, so every
// node reaches the identical PASSED/FAILED outcome for the same
// proposal at the same block. This is the first place
// Proposal.Passed()-equivalent logic actually has a consequence - see
// the governance plan's §2 for why nothing closed proposals or acted
// on the outcome before this existed.
func processProposalFinalization(asOf int64) {
	var ids []string
	for id, p := range proposalStore.Proposals {
		if p.Open && asOf >= p.Deadline {
			ids = append(ids, id)
		}
	}
	// Map iteration order is randomized in Go - sorting here means
	// every node processes the exact same sequence, same convention as
	// processDormantWallets.
	sort.Strings(ids)

	for _, id := range ids {
		finalizeProposal(proposalStore.Proposals[id], asOf)
	}
}

func finalizeProposal(p *core.Proposal, asOf int64) {
	p.Open = false

	totalWeight := p.YesVotes + p.NoVotes
	if totalWeight < p.Quorum || p.YesVotes <= p.NoVotes {
		p.Status = core.ProposalStatusFailed
		return
	}

	p.Status = core.ProposalStatusPassed

	// Real incentives, only on a genuine PASSED outcome - see the
	// constants' doc comment above.
	mintRept(p.CreatedBy, ProposalPassedProposerReward)

	var voters []string
	for addr := range proposalStore.Voted[p.ID] {
		voters = append(voters, addr)
	}
	sort.Strings(voters)
	for _, addr := range voters {
		mintRept(addr, ProposalPassedVoterReward)
	}

	// A PARAM_CHANGE/VERSION_ACTIVATION proposal's real protocol
	// consequence only ever happens here, gated behind exactly this
	// PASSED outcome - see core.ProposalKind's doc comment and
	// api/governance_params.go/core.ActivatedProtocolVersion for what
	// each does. A plain proposal (Kind == "") has no further effect
	// beyond the incentive payouts above.
	switch p.Kind {
	case core.ProposalKindParamChange:
		if p.ParamKey != "" {
			governanceParamsStore.Set(p.ParamKey, p.ParamValue)
		}
	case core.ProposalKindVersionActivation:
		if p.TargetVersion != "" {
			core.ActivatedProtocolVersion = p.TargetVersion
		}
	}
}

// autoProposalCheckIntervalBlocks is how often the network checks
// whether governance has gone quiet - a judgment call (roughly
// "check a few times a day" at this codebase's ~8s consensus tick),
// not derived from any spec.
const autoProposalCheckIntervalBlocks = 5400

// autoProposalPrompts is the rotating pool processAutoProposal draws
// from - plain civic-engagement questions only (never PARAM_CHANGE/
// VERSION_ACTIVATION - see core.Proposal.SystemGenerated's doc
// comment), so a quiet week never means the network silently changes
// itself, only that the community gets asked something to keep the
// conversation going.
var autoProposalPrompts = []struct{ Title, Description string }{
	{
		"What should the community focus on next?",
		"Open discussion - vote Yes to signal general support for continuing to prioritize new features and growth, No to signal a preference for consolidating and polishing what already exists.",
	},
	{
		"Should CreditChain highlight more community-run Trading Hubs?",
		"A system-generated prompt to keep governance active. Vote Yes if you'd like to see more hub spotlights/announcements, No if the current pace is fine.",
	},
	{
		"Is the current reputation/lending balance working for you?",
		"A system-generated check-in. Vote Yes if the reputation and borrowing system feels fair as-is, No if you think it needs a closer look.",
	},
	{
		"Should the community prioritize onboarding new validators?",
		"A system-generated prompt. Vote Yes to signal support for validator-growth initiatives, No if network size feels adequate right now.",
	},
}

// processAutoProposal keeps the governance feed "constantly active" -
// the community's own asked-for behavior: real, user-created
// proposals always take priority and this never overrides or
// competes with one (it only ever fires when NOTHING is currently
// open), but a governance page that's gone quiet for a while
// automatically gets a new civic-engagement prompt rather than
// sitting empty. Self-signed and gossiped like any other node-
// authored transaction (TxSlashReport/TxBridgeDeposit precedent) -
// whichever node's report is confirmed first wins, every other node's
// own attempt is a no-op once a proposal is already open.
func processAutoProposal(block core.Block) {
	if block.Index == 0 || block.Index%autoProposalCheckIntervalBlocks != 0 {
		return
	}

	for _, p := range proposalStore.Proposals {
		if p.Open {
			return
		}
	}

	prompt := autoProposalPrompts[(block.Index/autoProposalCheckIntervalBlocks)%uint64(len(autoProposalPrompts))]

	data, _ := json.Marshal(governanceData{
		Action:          "create",
		Title:           prompt.Title,
		Description:     prompt.Description,
		SystemGenerated: true,
	})
	recordTx(selfSignedEnvelope(data), reporterAddress(), "", 0, core.TxGovernance, string(data))
}

// CCTTransferFee is charged on every plain CCT send (a TxExchange
// resolving to symbol CCT) - requested as "half a CCT," but CCT
// balances are plain whole-number uint64 everywhere in this codebase
// (see core/token_store.go) with no smaller unit, so a literal 0.5
// isn't representable without rescaling every balance, constant, and
// persisted value in the system. This is the nearest practical whole-
// CCT substitution, a disclosed rounding rather than a silent
// reinterpretation - true sub-CCT precision is a much larger, separate
// change if it's ever wanted. Only CCT sends carry this fee -
// ETHTEST/USDCTEST/REPT/custom-token transfers are untouched.
const CCTTransferFee = 1

// ETHBCCTNetworkFeeWei is the flat wei fee charged on an ETHB ("CCT
// network") send - the cheap/fast side of the auto-bridge feature's
// network choice (see api/eth_bridge.go), as opposed to an ERC-network
// withdrawal's live-tracked gas fee + 0.05% bridge fee
// (applyBridgeWithdraw). Charged in ETH (deducted from the sender's
// ETHB balance, on top of the amount sent), never in CCT - "ETH is the
// mother asset, so ETH-denominated fees are always paid in ETH" (this
// replaces an earlier version of this fee that was mistakenly charged
// in CCT). Illustrative testnet value, a judgment call like every
// other flat fee in this codebase - there's no real per-transaction
// gas cost for an internal ledger move (nothing broadcasts to Sepolia
// for a CCT-network send), so this isn't gas-derived the way the
// ERC-network fee now is.
const ETHBCCTNetworkFeeWei = 100_000_000_000 // 0.0000001 ETH, illustrative

// UsdcTestTransferFeeWei is the flat wei fee charged on every USDCTEST
// send, deducted from the sender's ETHB balance - "ETH is the mother
// network, so USDT[-equivalent] pays its fees in ETH" per the user's
// framing. USDCTEST itself has no bridge/second-network concept today
// (confirmed: pure synthetic faucet token, no external chain link) -
// this implements the one send path that exists; "in either network"
// has nothing further to apply to until USDCTEST gets a real bridge of
// its own.
const UsdcTestTransferFeeWei = 100_000_000_000 // 0.0000001 ETH, same illustrative order as ETHBCCTNetworkFeeWei

// BTCBCCTNetworkFeeSats is BTCB's own-currency-pays-its-own-fee flat
// fee on a "CCT network" (internal-ledger) send - same philosophy as
// ETHBCCTNetworkFeeWei, charged in satoshis from the sender's own BTCB
// balance, on top of the amount sent. Illustrative testnet value.
const BTCBCCTNetworkFeeSats = 500 // 0.000005 BTC, illustrative

// cctTransferFeePoolAddress is a synthetic, nobody-holds-the-private-
// key holding address (same pattern as poolAddress/
// tokenCreationFeeAddress) that accumulates CCT transfer fees while a
// block's transactions are being applied - applyBlockReward sweeps
// whatever's sitting here to that block's own validator right after,
// so each block's fees fund whoever actually included it, and the
// pool is back to zero before the next block's transactions run.
func cctTransferFeePoolAddress() string {
	return "FEE:CCT_TRANSFER"
}

// registrationFeeAddress is the shared synthetic, nobody-holds-the-
// private-key destination for Hub/Moderator/Validator registration
// fees (HubApplyFeeCCT, ModeratorRegisterFeeCCT,
// ValidatorRegisterFeeCCT) - same tokenCreationFeeAddress-style
// pattern, not swept anywhere, just a permanent visible-in-explorer
// sink. Shared across all three roles since they're the same kind of
// cost (an anti-spam entry fee), not three separately tracked pools.
func registrationFeeAddress() string {
	return "FEE:REGISTRATION"
}

// applyTransfer moves tx.Amount of whichever symbol Data names from
// the sender's side (their own CCT address, or their derived
// ETHTEST/USDCTEST sub-address - see resolveBalanceAddress) to tx.To
// AS GIVEN, unresolved - the recipient is expected to have already
// supplied the correct address for that symbol (their ETHTEST address
// to receive ETHTEST, not their CCT address), the same way a real
// blockchain requires the right address for the right chain. Data
// defaults to CCT for transactions recorded before Symbol existed.
func applyTransfer(tx core.Transaction) {
	symbol := "CCT"
	if tx.Data != "" {
		var data tokenTransferData
		if err := json.Unmarshal([]byte(tx.Data), &data); err == nil && data.Symbol != "" {
			symbol = data.Symbol
		}
	}

	store, err := resolveTokenStore(symbol)
	if err != nil {
		return
	}

	fromSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, symbol)

	if symbol == "CCT" {
		// The transfer amount and its fee both have to be available, or
		// neither moves - re-checked here (never trusted from
		// submission time alone), same convention as every other fee in
		// this codebase (see applyTokenRegister's ETHTEST creation fee).
		// Reads through governanceParam so a passed PARAM_CHANGE
		// proposal changing "CCTTransferFee" takes effect here without
		// a code change - see api/governance_params.go.
		fee := governanceParam("CCTTransferFee", CCTTransferFee)
		if store.BalanceOf(fromSide) < tx.Amount+fee {
			return
		}
		if !store.Transfer(fromSide, cctTransferFeePoolAddress(), fee) {
			return
		}
	} else if symbol == "ETHB" {
		// "CCT network" send (see api/eth_bridge.go) - fee is now
		// charged in ETH, from the SAME ETHB balance being sent, on top
		// of the amount (own-currency-pays-its-own-fee, matching the
		// ERC-network withdrawal's existing shape) - see
		// ETHBCCTNetworkFeeWei's doc comment for why this moved off CCT.
		total := tx.Amount + ETHBCCTNetworkFeeWei
		if store.BalanceOf(fromSide) < total {
			return
		}
		if !store.Transfer(fromSide, ethbBridgeFeeAddress(), ETHBCCTNetworkFeeWei) {
			return
		}
	} else if symbol == "USDCTEST" {
		// USDCTEST pays its transfer fee in ETH (from the sender's ETHB
		// balance), not in USDCTEST itself or CCT - see
		// UsdcTestTransferFeeWei's doc comment.
		ethbSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, "ETHB")
		if bridgedEthStore.BalanceOf(ethbSide) < UsdcTestTransferFeeWei || store.BalanceOf(fromSide) < tx.Amount {
			return
		}
		if !bridgedEthStore.Transfer(ethbSide, ethbBridgeFeeAddress(), UsdcTestTransferFeeWei) {
			return
		}
	} else if symbol == "BTCB" {
		// "CCT network" send (see api/btc_bridge.go) - own-currency-
		// pays-its-own-fee, same shape as the ETHB branch above.
		total := tx.Amount + BTCBCCTNetworkFeeSats
		if store.BalanceOf(fromSide) < total {
			return
		}
		if !store.Transfer(fromSide, btcbBridgeFeeAddress(), BTCBCCTNetworkFeeSats) {
			return
		}
	}

	store.Transfer(fromSide, tx.To, tx.Amount)
}

// applyFaucetClaim is what actually grants the one-time faucet amount -
// replicated the same way as every other action, so every node agrees
// on who has already claimed instead of only the one node that
// happened to receive the original HTTP request. The faucet used to
// mint directly and instantly inside HandleFaucet, entirely local to
// whichever node got the request - fine in isolation, but with no
// gossip/replication at all, two production nodes' CCT supply could
// (and did) drift apart by however many claims landed on one node but
// not the other. Amounts are always the fixed FaucetAmount/
// FaucetETHTestAmount/FaucetUSDCTestAmount constants, never anything
// read from the transaction - there is nothing here for a malicious
// producer to inflate.
func applyFaucetClaim(tx core.Transaction) {
	faucetClaimed.mu.Lock()
	if faucetClaimed.seen[tx.From] {
		faucetClaimed.mu.Unlock()
		return
	}
	faucetClaimed.seen[tx.From] = true
	faucetClaimed.mu.Unlock()

	pub, err := hex.DecodeString(tx.AuthPublicKey)
	if err != nil {
		return
	}

	tokenStore.Mint(tx.From, FaucetAmount)
	ethTestStore.Mint(core.DeriveTokenAddress(pub, core.ETHTestAddressTag), FaucetETHTestAmount)
	usdcTestStore.Mint(core.DeriveTokenAddress(pub, core.USDCTestAddressTag), FaucetUSDCTestAmount)
}

// applyYeneClaim mints a testnet YEN-e amount deterministically derived
// from the transaction's own ID (see yeneClaimAmount) - the cooldown
// check is re-run here, identically on every node, rather than trusted
// from the HTTP handler's optimistic fast-fail.
func applyYeneClaim(tx core.Transaction) {
	if !yeneClaimStore.CanClaim(tx.From, tx.Timestamp, YeneClaimCooldownSeconds) {
		return
	}
	yeneClaimStore.RecordClaim(tx.From, tx.Timestamp)

	pub, err := hex.DecodeString(tx.AuthPublicKey)
	if err != nil {
		return
	}

	amount := yeneClaimAmount(tx.ID)
	yenStore.Mint(core.DeriveTokenAddress(pub, core.YenAddressTag), amount)
}

// orderEscrowAddress is one escrow per order, never shared/commingled
// across simultaneously-open orders - the exact same per-instance
// pattern as p2pOfferEscrowAddress (api/p2p_offers.go), reusing the
// transaction's own ID as the order ID the same way that function
// reuses it as the offer ID.
func orderEscrowAddress(orderID string) string {
	return "ORDERBOOK:" + orderID
}

// orderByID resolves a TradeRecord's BuyOrderID/SellOrderID back to
// the full core.Order (for its Owner/PublicKey) - taker isn't in
// orderBookStore yet at the point Match runs (see
// core.OrderBookStore.Match's doc comment), so it has to be checked
// directly rather than only through the store, which only ever holds
// orders that were already resting before this Match call.
func orderByID(taker *core.Order, id string) *core.Order {
	if id == taker.ID {
		return taker
	}
	o, _ := orderBookStore.Get(id)
	return o
}

// applyOrderPlace escrows a new limit order's funds, attempts an
// immediate match against the resting book, settles every resulting
// trade's real token balances, and rests whatever's left. Every check
// the HTTP handler (HandlePlaceOrder) already did is re-verified here
// against current state - never trusted from submission time alone,
// same convention as every other mutating handler in this codebase.
func applyOrderPlace(tx core.Transaction) {
	var data orderPlaceData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	if !orderBookSymbols[data.Symbol] {
		return
	}
	side := core.OrderSide(data.Side)
	if side != core.OrderBuy && side != core.OrderSell {
		return
	}
	if data.Price == 0 || data.Amount == 0 {
		return
	}

	store, err := resolveTokenStore(data.Symbol)
	if err != nil {
		return
	}

	escrow := orderEscrowAddress(tx.ID)

	if side == core.OrderSell {
		sellSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, data.Symbol)
		if !store.Transfer(sellSide, escrow, data.Amount) {
			return
		}
	} else {
		cost := core.OrderCCTCost(data.Amount, data.Price)
		if !tokenStore.Transfer(tx.From, escrow, cost) {
			return
		}
	}

	taker := &core.Order{
		ID:        tx.ID,
		Owner:     tx.From,
		PublicKey: tx.AuthPublicKey,
		Symbol:    data.Symbol,
		Side:      side,
		Price:     data.Price,
		Amount:    data.Amount,
		Remaining: data.Amount,
		Status:    core.OrderOpen,
		CreatedAt: tx.Timestamp,
	}

	trades := orderBookStore.Match(taker)
	orderBookStore.Create(taker)

	var lastPrice uint64
	var takerFilled, takerActualCost uint64
	for _, trade := range trades {
		buyOrder := orderByID(taker, trade.BuyOrderID)
		sellOrder := orderByID(taker, trade.SellOrderID)
		if buyOrder == nil || sellOrder == nil {
			continue
		}

		cctCost := core.OrderCCTCost(trade.Amount, trade.Price)
		buyerBaseSide := resolveBalanceAddress(buyOrder.Owner, buyOrder.PublicKey, trade.Symbol)

		store.Transfer(orderEscrowAddress(sellOrder.ID), buyerBaseSide, trade.Amount)
		tokenStore.Transfer(orderEscrowAddress(buyOrder.ID), sellOrder.Owner, cctCost)

		lastPrice = trade.Price
		if trade.BuyOrderID == taker.ID {
			takerFilled += trade.Amount
			takerActualCost += cctCost
		}
	}

	// A BUY taker that matched at a cheaper-than-limit resting price
	// still escrowed the worst-case cost (at its own limit price) up
	// front - refund the surplus for the filled portion back to the
	// buyer's real CCT balance now that settlement is done, standard
	// exchange "price improvement" convention. The unfilled portion's
	// escrow (if any remains resting) is untouched here - it's
	// released later, either by a future fill's settlement above or
	// by applyOrderCancel.
	if side == core.OrderBuy && takerFilled > 0 {
		worstCaseCost := core.OrderCCTCost(takerFilled, data.Price)
		if worstCaseCost > takerActualCost {
			tokenStore.Transfer(escrow, tx.From, worstCaseCost-takerActualCost)
		}
	}

	if len(trades) > 0 {
		tradeHistoryStore.Record(trades)
		priceHistoryStore.RecordPrice(data.Symbol, lastPrice, tx.Timestamp)
	}
}

// applyOrderCancel releases whatever's left of a still-open order's
// escrow back to its owner and marks it CANCELLED. Re-verifies
// ownership/open-status itself (core.OrderBookStore.Cancel already
// enforces this), never trusting the HTTP handler's own preview check
// alone.
func applyOrderCancel(tx core.Transaction) {
	var data orderCancelData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	order, ok := orderBookStore.Cancel(data.OrderID, tx.From)
	if !ok || order.Remaining == 0 {
		return
	}

	escrow := orderEscrowAddress(order.ID)

	if order.Side == core.OrderSell {
		store, err := resolveTokenStore(order.Symbol)
		if err != nil {
			return
		}
		sellSide := resolveBalanceAddress(order.Owner, order.PublicKey, order.Symbol)
		store.Transfer(escrow, sellSide, order.Remaining)
	} else {
		refund := core.OrderCCTCost(order.Remaining, order.Price)
		tokenStore.Transfer(escrow, order.Owner, refund)
	}
}
