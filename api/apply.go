package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
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
		creditMaturedUnbond(entry)
	}

	// Same block-timestamp-driven determinism as everything else here
	// - "unlike Bitcoin, it can't be lost forever" (see
	// core/dormancy.go): wallets quiet long enough get swept and
	// redistributed to whoever's still active.
	processDormantWallets(block.Timestamp)
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

func applyBlockReward(block core.Block) {
	if nonAddressValidators[block.Validator] {
		return
	}

	wallet := walletStore.Get(block.Validator)
	wallet.Rewards += ProduceBlockReward
	tokenStore.Mint(block.Validator, ProduceBlockReward)

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
			reptStore.Mint(block.Validator, reward)
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

	case core.TxLend, core.TxRepay, core.TxGovernance, core.TxFaucetClaim:
		// No Amount is trusted directly from these transactions - the
		// apply* functions for these either re-derive the amount from
		// existing trusted state (the loan record itself for
		// Lend/Repay), carry none at all (Governance), or mint a fixed
		// server-side constant regardless of anything the client sent
		// (FaucetClaim) - so only the actor's identity needs binding
		// here.
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
		// Node-authored, not wallet-authored - same reasoning as
		// TxSlashReport above. The actual authorization (ADMIN_TOKEN
		// check) already happened at the HTTP layer before this was
		// ever recorded (see HandleTokenMint) - other nodes applying
		// this transaction trust that the submitting node did that
		// check correctly, the same disclosed trust model already used
		// for slash reports (one node's attestation, not a separate
		// consensus threshold).
		return tx.Data == string(tx.AuthPayload)

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

	case core.TxExchange:
		applyTransfer(tx)

	case core.TxValidatorRegister:
		applyValidatorRegister(tx)

	case core.TxLockCollateral:
		applyLockCollateral(tx)

	case core.TxSlashReport:
		applySlashReport(tx)

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

	case core.TxLoanOfferCreate:
		applyLoanOfferCreate(tx)

	case core.TxLoanOfferDraw:
		applyLoanOfferDraw(tx)

	case core.TxLoanOfferClose:
		applyLoanOfferClose(tx)

	case core.TxModeratorRegister:
		applyModeratorRegister(tx)

	case core.TxModeratorConfirm:
		applyModeratorConfirm(tx)

	case core.TxP2PExternalAccept:
		applyP2PExternalAccept(tx)

	case core.TxP2PSellerConfirmPaid:
		applyP2PSellerConfirmPaid(tx)

	case core.TxP2PBuyerMarkPaid:
		applyP2PBuyerMarkPaid(tx)

	case core.TxP2PBuyerConfirmPaid:
		applyP2PBuyerConfirmPaid(tx)

	case core.TxP2PModeratorResolve:
		applyP2PModeratorResolve(tx)

	case core.TxCompanyApply:
		applyCompanyApply(tx)

	case core.TxCompanyReview:
		applyCompanyReview(tx)

	case core.TxCompanyReassignModerator:
		applyCompanyReassignModerator(tx)

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

	case core.TxHubStorefrontSet:
		applyHubStorefrontSet(tx)

	case core.TxTokenMint:
		applyTokenMint(tx)
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
		tokenStore.Mint(data.Address, data.Amount)
		return
	}

	store, err := resolveTokenStore(data.Symbol)
	if err != nil {
		return
	}
	store.Mint(data.Address, data.Amount)
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
	tokenRegistry.Register(data.Symbol, data.Name, tx.From, data.InitialSupply)
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
		applySwapCCTToToken(tx.From, tx.AuthPublicKey, data.ToSymbol, data.AmountIn)

	case data.ToSymbol == "CCT":
		applySwapTokenToCCT(tx.From, tx.AuthPublicKey, data.FromSymbol, data.AmountIn)

	default:
		applySwapTokenToToken(tx.From, tx.AuthPublicKey, data.FromSymbol, data.ToSymbol, data.AmountIn)
	}
}

func applySwapCCTToToken(from, publicKey, toSymbol string, amountIn uint64) {
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
}

func applySwapTokenToCCT(from, publicKey, fromSymbol string, amountIn uint64) {
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
}

// applySwapTokenToToken routes fromSymbol -> CCT -> toSymbol through
// both pools' escrow accounts. Both legs are QUOTED first (pure
// calculation, no state mutation) so the whole swap either goes
// through or is rejected outright - never left half-applied with the
// user's input gone but no output credited because the second leg
// turned out to lack liquidity.
func applySwapTokenToToken(from, publicKey, fromSymbol, toSymbol string, amountIn uint64) {
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
}

// ReputationDeltaP2PTrade is the reputation reward BOTH parties earn
// when a P2P offer is successfully accepted - a completed trade is a
// mutual accomplishment (unlike a loan, where only the borrower
// "achieves" repayment), so both sides get it. Set 40% above the
// closest existing precedent for a genuinely completed exchange
// (ReputationDeltaLoanRepaidOnTime, 10 - see core/credit_engine.go) -
// a disclosed judgment call, not a locked spec value.
const ReputationDeltaP2PTrade = 14 // 10 * 1.4, rounded down

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
		reptStore.Mint(tx.From, reward)
	}
	if reward := core.IncreaseWalletReputation(walletStore.Get(offer.Creator), ReputationDeltaP2PTrade); reward > 0 {
		reptStore.Mint(offer.Creator, reward)
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

	p2pOfferStore.ConfirmSellerPaid(data.OfferID, tx.From)
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
		releaseEscrowToBuyer(offer, offer.AcceptedBy, offer.AcceptedByPublicKey)
		return
	}

	offerStore, err := resolveTokenStore(offer.OfferSymbol)
	if err != nil {
		return
	}
	sellerSide := resolveBalanceAddress(offer.Creator, offer.CreatorPublicKey, offer.OfferSymbol)
	offerStore.Transfer(p2pOfferEscrowAddress(offer.ID), sellerSide, offer.OfferAmount)
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
func creditMaturedUnbond(entry core.UnbondingEntry) {
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
		creditMaturedETHTestViaSwap(entry.Amount, creditAddress)
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
func creditMaturedETHTestViaSwap(amount uint64, creditAddress string) {
	escrow := stakeEscrowAddress("ETHTEST")
	poolEscrow := poolAddress("ETHTEST")

	quotedCCT, ok := cctEthTestPool.QuoteTokenForCCT(amount)
	if ok {
		if _, ok2 := cctEthTestPool.QuoteCCTForToken(quotedCCT); ok2 {
			if ethTestStore.Transfer(escrow, poolEscrow, amount) {
				intermediateCCT, _ := cctEthTestPool.SwapTokenForCCT(amount)
				finalAmountOut, _ := cctEthTestPool.SwapCCTForToken(intermediateCCT)
				ethTestStore.Transfer(poolEscrow, creditAddress, finalAmountOut)
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

	case "missed_block":
		dedupKey := "missed|" + data.ValidatorAddress + "|" + data.RoundHash
		if slashAlreadyApplied(dedupKey) {
			return
		}

		wallet := walletStore.Get(data.ValidatorAddress)
		v := hydrateValidator(status)
		v.Wallet = *wallet

		core.ApplyMissedBlock(v)

		wallet.Reputation = v.Wallet.Reputation
		validatorStore.RecordMiss(data.ValidatorAddress)
		validatorStore.RecordSlash(data.ValidatorAddress, v.SlashCount-status.SlashCount)
	}
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
	wallet := walletStore.Get(tx.From)
	stakingStore.SyncWallet(wallet, tx.From)

	if wallet.StakedETH == 0 || wallet.Reputation < MinValidatorReputation {
		return
	}

	var data validatorRegisterData
	_ = json.Unmarshal([]byte(tx.Data), &data)

	validatorStore.Register(tx.From, data.NodeID, data.NodePublicKey)
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
	tokenStore.Mint(tx.From, reward)

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

// releaseLoanCollateral returns a repaid loan's earmarked collateral
// to the exact staked/oETH balance it was reserved from -
// collateralStore is deliberately NOT credited back here, since this
// collateral is fully released to the user, not returned to the
// general locked-but-unallocated pool.
func releaseLoanCollateral(loan *core.Loan) {
	if loan.CollateralAmount == 0 {
		return
	}

	switch loan.CollateralSymbol {
	case "CCT":
		if stakingStore.Stakes[loan.Borrower] == nil {
			stakingStore.Stakes[loan.Borrower] = make(map[string]uint64)
		}
		stakingStore.Stakes[loan.Borrower]["CCT"] += loan.CollateralAmount

	case "OETH":
		walletStore.Get(loan.Borrower).OETHBalance += loan.CollateralAmount
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

	loanStore.CreateLoan(id, tx.To, tx.Amount, data.Interest, collateralSymbol, tx.Amount)

	creditHistory.Add(tx.To, core.CreditEvent{Type: "BORROW", Amount: tx.Amount})
	core.RecordLoanTaken(creditStore.Get(tx.To))

	// Borrowed funds land in the separate USDCTEST ledger, not CCT -
	// keeps "earned by staking/governance" and "borrowed" visibly
	// distinct balances, never commingled. Minted into the borrower's
	// derived USDCTEST sub-address (see resolveBalanceAddress), not
	// their CCT address - tx.AuthPublicKey is the borrower's own
	// authenticated public key (see verifyTxAuth's TxBorrow case).
	usdcSide := resolveBalanceAddress(tx.To, tx.AuthPublicKey, "USDCTEST")
	usdcTestStore.Mint(usdcSide, tx.Amount)
}

func applyLoanOfferCreate(tx core.Transaction) {
	var data loanOfferCreateData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.TotalAmount == 0 {
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
		reptStore.Mint(offer.Lender, reward)
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

func applyModeratorRegister(tx core.Transaction) {
	if !moderatorStore.Register(tx.From, tx.AuthPublicKey, tx.Timestamp) {
		return
	}

	wallet := walletStore.Get(tx.From)
	if reward := core.IncreaseWalletReputation(wallet, core.ModeratorReputationBonus); reward > 0 {
		reptStore.Mint(tx.From, reward)
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
			reptStore.Mint(tx.From, reward)
		}
		reputationEventStore.Record(&core.ReputationEvent{
			TxID: tx.ID, Target: tx.From, Delta: int64(core.ModeratorConfirmReputationBonus),
			Reason: "Confirmed an undisputed trade as moderator", Timestamp: tx.Timestamp,
		})
	}
}

func applyCompanyApply(tx core.Transaction) {
	var data companyApplyData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if len(data.Name) == 0 || len(data.Name) > 200 {
		return
	}

	companyStore.Apply(&core.CompanyApplication{
		Address:      tx.From,
		PublicKey:    tx.AuthPublicKey,
		Name:         data.Name,
		Description:  data.Description,
		DocumentNote: data.DocumentNote,
		Status:       core.CompanyPending,
		// Assigned here (block-confirmation time), not in the HTTP
		// handler, for the same determinism reasoning as every other
		// moderator assignment (see core.ModeratorStore.AssignNext).
		ModeratorAddress: moderatorStore.AssignNext(),
		CreatedAt:        tx.Timestamp,
	})
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
		ID:                  offerID,
		Creator:             tx.From,
		CreatorPublicKey:    tx.AuthPublicKey,
		OfferSymbol:         request.WantSymbol,
		OfferAmount:         request.WantAmount,
		WantSymbol:          request.PaySymbol,
		WantAmount:          request.PayAmount,
		PaymentMode:         core.PaymentModeExternal,
		Status:              core.OfferAccepted,
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
	// Worker delegation (or lack of it) may have changed between
	// submission and confirmation - re-checked here, not just trusted
	// from submission time.
	hub, ok := authorizedHubActor(data.Hub, tx.From)
	if !ok {
		return
	}
	// HubPublicKey must always be the HUB's own public key (it's used
	// elsewhere to re-derive the hub's ETHTEST/USDCTEST sub-address -
	// see resolveBalanceAddress), never a delegated worker's - when the
	// hub posts directly tx.AuthPublicKey already IS the hub's key, but
	// when a worker posts on the hub's behalf it's the worker's key, so
	// the hub's real public key is looked up from its own company
	// application record instead.
	hubPublicKey := tx.AuthPublicKey
	if hub != tx.From {
		app, ok := companyStore.Get(hub)
		if !ok {
			return
		}
		hubPublicKey = app.PublicKey
	}
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
		Hub:          hub,
		HubPublicKey: hubPublicKey,
		Symbol:       data.Symbol,
		FiatSymbol:   data.FiatSymbol,
		SellRate:     data.SellRate,
		BuyRate:      data.BuyRate,
		Active:       data.Active,
		Mode:         data.Mode,
		UpdatedAt:    tx.Timestamp,
		PostedBy:     tx.From,
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

	hubRulesStore.Set(tx.From, data.Text, data.PaymentWindowMinutes, tx.Timestamp)
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
	hubSymbolSide := resolveBalanceAddress(data.Hub, rate.HubPublicKey, data.Symbol)
	hubCounterSide := resolveBalanceAddress(data.Hub, rate.HubPublicKey, rate.FiatSymbol)
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
		counterAmount = data.Amount * rate.SellRate
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
		creator, creatorPublicKey = data.Hub, rate.HubPublicKey
		acceptedBy, acceptedByPublicKey = tx.From, tx.AuthPublicKey

	case "SELL_TO_HUB":
		// User pays Symbol, receives counterAmount of FiatSymbol from
		// the hub.
		if rate.BuyRate == 0 {
			return
		}
		counterAmount = data.Amount * rate.BuyRate
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
		acceptedBy, acceptedByPublicKey = data.Hub, rate.HubPublicKey

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
	if data.Amount == 0 || data.Hub == tx.From {
		return
	}
	if data.Direction != "BUY_FROM_HUB" && data.Direction != "SELL_TO_HUB" {
		return
	}

	rate, ok := hubRateStore.Get(data.Hub, data.Symbol)
	if !ok || !rate.Active {
		return
	}
	// Re-verify hub is still a verified Trading Hub at confirmation
	// time, not just when the rate was posted.
	if !companyStore.IsVerified(data.Hub) {
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
		fiatAmount := data.Amount * rate.SellRate
		hubSide := resolveBalanceAddress(data.Hub, rate.HubPublicKey, data.Symbol)
		// The hub's LIVE balance is the range - re-checked here, not
		// trusted from submission time, and never a pre-fixed amount
		// (see core.HubRate's doc comment).
		if store.BalanceOf(hubSide) < data.Amount {
			return
		}
		if !store.Transfer(hubSide, escrow, data.Amount) {
			return
		}

		p2pOfferStore.Create(&core.P2POffer{
			ID:                  offerID,
			Creator:             data.Hub,
			CreatorPublicKey:    rate.HubPublicKey,
			OfferSymbol:         data.Symbol,
			OfferAmount:         data.Amount,
			WantSymbol:          rate.FiatSymbol,
			WantAmount:          fiatAmount,
			PaymentMode:         core.PaymentModeExternal,
			Status:              core.OfferAccepted,
			AcceptedBy:           tx.From,
			AcceptedByPublicKey:  tx.AuthPublicKey,
			SettlementStatus:     core.SettlementAwaitingPayment,
			AwaitingPaymentSince: tx.Timestamp,
			CreatedAt:            tx.Timestamp,
			ModeratorAddress:     moderatorStore.AssignNext(),
			ModerationStatus:     core.ModerationPending,
		})

	case "SELL_TO_HUB":
		// The user is selling Symbol TO the hub - their own balance is
		// escrowed, fully authorized by their own signature on this
		// very transaction (no special hub pre-authorization needed).
		if rate.BuyRate == 0 {
			return
		}
		fiatAmount := data.Amount * rate.BuyRate
		userSide := resolveBalanceAddress(tx.From, tx.AuthPublicKey, data.Symbol)
		if store.BalanceOf(userSide) < data.Amount {
			return
		}
		if !store.Transfer(userSide, escrow, data.Amount) {
			return
		}

		p2pOfferStore.Create(&core.P2POffer{
			ID:                  offerID,
			Creator:             tx.From,
			CreatorPublicKey:    tx.AuthPublicKey,
			OfferSymbol:         data.Symbol,
			OfferAmount:         data.Amount,
			WantSymbol:          rate.FiatSymbol,
			WantAmount:          fiatAmount,
			PaymentMode:         core.PaymentModeExternal,
			Status:              core.OfferAccepted,
			AcceptedBy:           data.Hub,
			AcceptedByPublicKey:  rate.HubPublicKey,
			SettlementStatus:     core.SettlementAwaitingPayment,
			AwaitingPaymentSince: tx.Timestamp,
			CreatedAt:            tx.Timestamp,
			ModeratorAddress:     moderatorStore.AssignNext(),
			ModerationStatus:     core.ModerationPending,
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
		return "USDCTEST"
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
			reptStore.Mint(loan.Borrower, reward)
		}
		reputationEventStore.Record(&core.ReputationEvent{
			TxID: tx.ID, Target: loan.Borrower, Delta: int64(core.ReputationDeltaLoanRepaidOnTime),
			Reason: "Repaid a peer-funded loan", Timestamp: tx.Timestamp,
		})
		return
	}

	loan.Status = core.LoanRepaid

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
		reptStore.Mint(tx.From, reward)
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
	Action      string `json:"action"` // "create" or "vote"
	Title       string `json:"title"`
	Description string `json:"description"`
	ProposalID  string `json:"proposalId"`
	Support     bool   `json:"support"`
}

func applyGovernance(tx core.Transaction) {
	var data governanceData
	_ = json.Unmarshal([]byte(tx.Data), &data)

	switch data.Action {
	case "create":
		id := "prop-" + tx.ID[:min(8, len(tx.ID))]
		proposalStore.Create(&core.Proposal{
			ID:          id,
			Title:       data.Title,
			Description: data.Description,
			CreatedBy:   tx.From,
			Open:        true,
		})

	case "vote":
		// Voting power is read fresh at confirmation time, not at
		// submission time - your vote's weight reflects your
		// reputation/stake when it actually lands in a block.
		power := walletStore.Get(tx.From).VotingPower
		if power == 0 {
			power = 1
		}

		_, _ = proposalStore.Vote(data.ProposalID, tx.From, data.Support, power)
	}
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
