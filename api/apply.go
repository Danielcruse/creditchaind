package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// platformFeeValidatorPoolAddress is a per-symbol synthetic sink for
// the marketplace platform fee's validator share - same
// "nobody-holds-the-private-key, swept at applyBlockReward time"
// pattern cctTransferFeePoolAddress already established for the CCT
// transfer fee, just keyed per symbol since marketplace trades aren't
// CCT-only (see resolveTokenStore). Kept genuinely separate from
// cctTransferFeePoolAddress even for CCT-denominated trades - the two
// are different revenue sources worth tracking independently, not
// commingled just because they happen to share a symbol sometimes.
func platformFeeValidatorPoolAddress(symbol string) string {
	return "FEE:PLATFORM_VALIDATOR:" + symbol
}

// platformFeeModeratorPoolAddress is the moderator-share counterpart -
// accumulates the 10% moderator slice from every trade that settles
// WITHOUT needing moderator dispute resolution (an assigned moderator
// who was never actually called on still deserves a cut - same
// "still gets weighed in, just not for this one round" reasoning PoSR
// already applies to validators) - swept every two weeks, split by
// reputation x uptime across every currently-active moderator (see
// sweepModeratorFeePool). A trade that DOES need dispute resolution
// pays its moderator share directly to whichever moderator actually
// resolved it instead (see splitPlatformFee's resolvingModerator
// parameter) - compensation for real work done, not pooled with
// everyone else's.
func platformFeeModeratorPoolAddress(symbol string) string {
	return "FEE:PLATFORM_MODERATOR:" + symbol
}

// outernetOperatorAddress is the real, operator-held wallet Outernet's
// own 10% cut of the marketplace platform fee pays into - a genuinely
// separate destination from treasuryStore.Treasury.FoundationAddress
// (the CreditChain protocol's own treasury): Outernet is the operator
// running this specific interface/deployment, not the protocol itself,
// the same "storefront operator vs. the marketplace platform it rents
// space on" distinction the user drew explicitly when this was
// designed. Deliberately a plain constant, not an admin-settable field
// like FoundationAddress - nothing about this request asked for it to
// be reconfigurable later; if it ever needs to change, that's a code
// change and redeploy, matching every other fixed pool/fee address in
// this file. Stored in its raw (un-prefixed) form - see
// normalizeCreditChainAddress's doc comment for why the "cc"-prefixed
// display form must never be used as a literal storage key.
const outernetOperatorAddress = "11c53eb5e43a85001a3b3abf778548d111c9f1ed"

// splitPlatformFee divides a computed platform fee: 50% burned, 20%
// into this symbol's validator pool (swept to whichever validator
// produces the next block, see applyBlockReward), 10% to the
// Foundation Treasury (protocol admin), 10% to outernetOperatorAddress
// (the operator's own cut), and 10% to the moderator side - paid
// directly to resolvingModerator if a real dispute was actually
// resolved to reach this settlement (non-empty), or pooled into
// platformFeeModeratorPoolAddress otherwise (see that function's doc
// comment). The 50% burn is a REAL Burn call (permanently reduces
// supply), not a transfer to an unclaimed address - a deliberate
// deflationary choice, distinct from every other "fee sink nobody
// holds the key to" pattern in this codebase, which preserve supply.
// When the burned symbol is CCT, the burned amount accumulates into
// core.Treasury.CCTBurnedPendingReward directly - see that field's doc
// comment for how applyBlockReward turns this into the validator's
// block reward. Every other symbol's burn (ETH_CCC1, USDC_CCC1,
// BTC_CCC1) still reduces its own supply the same way, and ALSO feeds
// the same accumulator - valued in CCT terms via
// cctEquivalentWholeOfBurn (api/burn_value_oracle.go), never by
// actually converting the burned balance itself: nothing here mints,
// burns, or moves any OTHER token ledger to make this happen, so this
// never reopens the deliberate "no floating CCT pool for a real vault-
// backed asset" decision (see api/swap_fair_value.go's doc comment).
// REPT has no defined USD/CCT exchange rate anywhere in this codebase,
// so its burns still don't feed the reward - a disclosed limitation,
// not an oversight.
func splitPlatformFee(store wholeUnitStore, symbol, from string, fee uint64, resolvingModerator string) {
	if fee == 0 {
		return
	}
	burnShare := fee * 50 / 100
	validatorShare := fee * 20 / 100
	adminShare := fee * 10 / 100
	outernetShare := fee * 10 / 100
	moderatorShare := fee - burnShare - validatorShare - adminShare - outernetShare // remainder covers rounding, currently exactly 10%

	if burnShare > 0 {
		store.Burn(from, burnShare)
		if symbol == "CCT" {
			treasuryStore.Treasury.CCTBurnedPendingReward += burnShare
		} else {
			treasuryStore.Treasury.CCTBurnedPendingReward += cctEquivalentWholeOfBurn(symbol, burnShare)
		}
	}
	if validatorShare > 0 {
		store.Transfer(from, platformFeeValidatorPoolAddress(symbol), validatorShare)
	}
	if adminShare > 0 {
		store.Transfer(from, treasuryStore.Treasury.FoundationAddress, adminShare)
	}
	if outernetShare > 0 {
		store.Transfer(from, outernetOperatorAddress, outernetShare)
	}
	if moderatorShare > 0 {
		if resolvingModerator != "" {
			store.Transfer(from, resolvingModerator, moderatorShare)
		} else {
			store.Transfer(from, platformFeeModeratorPoolAddress(symbol), moderatorShare)
		}
	}
}

// applyPlatformFee is the single place every completed P2P Trading
// Center settlement (direct P2P offer accept/release, Hub Rate trade,
// Buy Request fulfillment) routes its final payout through - splits
// amount into (amount - fee) to the real recipient and
// core.PlatformFeeAmount(amount) across splitPlatformFee's five
// destinations, both drawn from the same already-escrowed/authorized
// balance so total value moved never exceeds amount. resolvingModerator
// is "" for every ordinary settlement (the moderator share pools, see
// platformFeeModeratorPoolAddress) - only a dispute a moderator
// actually resolved passes a real address here, see
// applyP2PModeratorResolve.
func applyPlatformFee(store wholeUnitStore, symbol, from, to string, amount uint64, resolvingModerator string) bool {
	fee := core.PlatformFeeAmount(amount)
	if fee == 0 {
		return store.Transfer(from, to, amount)
	}
	if !store.Transfer(from, to, amount-fee) {
		return false
	}
	splitPlatformFee(store, symbol, from, fee, resolvingModerator)
	return true
}

// skimPlatformFee pulls the fee slice of amount back out of address
// AFTER address has already received amount in full - used where a
// trade settles in two legs that must both succeed before either is
// truly final (see settleHubTradeOnChain), so the fee can only safely
// be taken once there's no remaining refund path that would otherwise
// need to account for it. Never reached via a moderator-resolved
// dispute path, so always pools the moderator share (see
// applyPlatformFee's own doc comment on resolvingModerator).
func skimPlatformFee(store wholeUnitStore, symbol, address string, amount uint64) {
	fee := core.PlatformFeeAmount(amount)
	splitPlatformFee(store, symbol, address, fee, "")
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
		cctStore.Transfer(validatorDelegationEscrowAddress, entry.Delegator, core.FormatCctSmallest(core.CctWholeToSmallest(entry.Amount)))
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

	// Governance RC pool - same epoch cadence as the sweep above, but
	// credits one synthetic pool address instead of per-wallet - see
	// its own doc comment (api/rc_governance_pool_mining.go).
	processGovernanceRCPoolMining(block)

	// One-time legacy ETHTEST/USDCTEST stake redemption - see its own
	// doc comment (api/legacy_stake_migration.go). Runs every block
	// like any other sweep here, but is a no-op forever once its
	// one-time pass completes.
	processLegacyStakeMigrationSweep(block)

	// Governance-conviction bootstrap (one-time, grandfathers already-
	// engaged wallets in at their current GovernanceTrust) MUST run
	// before the recurring sweep below on any given wallet, or that
	// wallet would grow from 0 for a step or two first - see
	// api/governance_conviction.go's own doc comment on why this race
	// is harmless (self-correcting, never a wrong end state) but the
	// ordering still matters for how quickly it corrects.
	processGovernanceConvictionBootstrap(block)
	processGovernanceConvictionSweep(block)

	// Same block-timestamp-driven determinism as every sweep above -
	// proposals whose 7-day voting window has passed close identically
	// on every node, with identical PASSED/FAILED outcomes and (on
	// PASSED) identical REPT payouts/protocol-parameter effects.
	processProposalFinalization(block.Timestamp)

	// Same block-timestamp-driven determinism - executive-only votes on
	// RealWorldTrustWeightBps close identically on every node, with
	// identical PASSED/FAILED outcomes and (on PASSED) identical
	// governanceParamsStore effects - see api/executive_proposal.go.
	processExecutiveProposalFinalization(block.Timestamp)

	// Keeps the governance feed "constantly active" even when no real
	// user has proposed anything lately - see processAutoProposal's
	// own doc comment.
	processAutoProposal(block)

	// Same block-timestamp-driven determinism - a refund-resolved P2P
	// dispute's at-fault party who neither paid nor declined within the
	// window gets both penalties applied automatically, identically on
	// every node. See resolveExpiredDisputeFaultChoices's own doc
	// comment.
	resolveExpiredDisputeFaultChoices(block)

	// Same block-timestamp-driven determinism - the pooled moderator
	// fee share gets distributed to active moderators once every two
	// weeks. See sweepModeratorFeePoolIfDue's own doc comment.
	sweepModeratorFeePoolIfDue(block)

	// Housekeeping only - ListActive already excludes an expired-but-
	// not-yet-swept post on its own (see SponsoredPostStore.ExpireStale's
	// doc comment), so this just keeps Status accurate for display.
	sponsoredPostStore.ExpireStale(block.Timestamp)
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

	dormancyPeriodSeconds := int64(governanceParamsStore.GetOrDefault("DormancyPeriodSeconds", uint64(core.DormancyPeriodSeconds)))

	for address, w := range walletStore.Wallets {
		if w.IsDormant(asOf, dormancyPeriodSeconds) {
			dormantAddrs = append(dormantAddrs, address)
		}
		// Deliberately a separate check, not an else-if paired with
		// !IsDormant() - see QualifiesForAirdrop's doc comment for why
		// those two aren't interchangeable (an already-reclaimed wallet
		// reports IsDormant()==false forever, but that's not proof of
		// current activity).
		if w.QualifiesForAirdrop(asOf, dormancyPeriodSeconds) {
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

// applyBlockReward pays out the PoSR block reward for a produced
// block (see pendingBurnBasedBlockReward - 90% of the CCT burned from
// platform fees since the last block) - 100% to the validator if
// nobody has delegated to them (identical to behavior before
// delegation pools existed), or split between the validator's own
// commission cut and a pro-rata share to every delegator (by their
// delegated amount / the pool's total) once they have a real pool
// behind them. See core.ValidatorStatus.CommissionBps and
// core.DelegationStore's doc comments for the full model.
// mintRept is the single call site every REPT tier-reward mint goes
// through - centralizes the "Minted REPT" notification alongside the
// actual mint instead of relying on every one of this file's several
// mint sites to remember it individually.
func mintRept(address string, amount uint64) {
	reptStore.Mint(address, amount)
	notifyUser(address, "REPT_MINTED", fmt.Sprintf("You minted %d REPT", amount), "")
}

// moderatorFeePoolSweepIntervalSeconds is how often the pooled
// moderator fee share (see platformFeeModeratorPoolAddress) gets
// distributed to every currently-active moderator, weighted by
// reputation x uptime.
const moderatorFeePoolSweepIntervalSeconds = 14 * 24 * 3600 // 2 weeks

// moderatorWeightPrecision mirrors core/consensus.go's posrPrecision -
// this sweep computes its reputation x uptime weight in pure integer
// fixed-point throughout (never float64), the same discipline that
// file's own doc comment explains at length: IEEE-754 guarantees basic
// operators are bit-identical everywhere, but this keeps every
// weighted-distribution calculation in the codebase consistent rather
// than relying on that guarantee call-by-call.
const moderatorWeightPrecision = 1_000_000

// sweepModeratorFeePoolIfDue distributes every native symbol's pooled
// moderator fee share across every currently-active, non-deactivated
// moderator, weighted by reputation x uptime - gated on
// moderatorFeePoolSweepIntervalSeconds having elapsed since the last
// sweep (treasuryStore.Treasury.LastModeratorPoolSweepAt), driven by
// block.Timestamp for the same cross-node determinism guarantee as
// every other periodic sweep in this file. A pool with nobody eligible
// to receive it (no active moderators, or every active moderator
// currently has 0 reputation or 0 uptime) is simply left alone until
// the next sweep - never burned, never stuck, just deferred.
func sweepModeratorFeePoolIfDue(block core.Block) {
	if block.Timestamp-treasuryStore.Treasury.LastModeratorPoolSweepAt < moderatorFeePoolSweepIntervalSeconds {
		return
	}
	treasuryStore.Treasury.LastModeratorPoolSweepAt = block.Timestamp

	var active []*core.ModeratorStatus
	for _, m := range moderatorStore.List() {
		if !m.Deactivated {
			active = append(active, m)
		}
	}
	if len(active) == 0 {
		return
	}
	// moderatorStore.List() explicitly does NOT guarantee stable order
	// (map iteration) - sorted here so every node visits the same
	// moderators in the same order, same "map iteration is randomized"
	// determinism discipline as applyBlockReward's own sorted delegator
	// walk. Each moderator's share below is computed independently of
	// the others (no running total carried between iterations), so this
	// sort doesn't change the final distributed amounts either way - but
	// leaving it unsorted would be an unexplained departure from this
	// file's own established convention for exactly this "every node
	// must agree" reasoning, however deep the actual answer.
	sort.Slice(active, func(i, j int) bool { return active[i].Address < active[j].Address })

	weights := make([]uint64, len(active))
	var totalWeight uint64
	for i, m := range active {
		reputation := walletStore.Get(m.Address).Reputation
		var uptimeFixed uint64
		if m.DisputesAssigned == 0 {
			uptimeFixed = moderatorWeightPrecision
		} else {
			uptimeFixed = m.DisputesResolved * moderatorWeightPrecision / m.DisputesAssigned
		}
		w := reputation * uptimeFixed
		weights[i] = w
		totalWeight += w
	}
	if totalWeight == 0 {
		return
	}

	for _, symbol := range []string{"CCT", "ETH_CCC1", "USDC_CCC1", "BTC_CCC1", "REPT"} {
		poolStore, err := resolveTokenStore(symbol)
		if err != nil {
			continue
		}
		poolAddr := platformFeeModeratorPoolAddress(symbol)
		poolBalance := poolStore.BalanceOf(poolAddr)
		if poolBalance == 0 {
			continue
		}
		for i, m := range active {
			share := poolBalance * weights[i] / totalWeight
			if share > 0 {
				poolStore.Transfer(poolAddr, m.Address, share)
			}
		}
	}
}

// pendingBurnBasedBlockReward is a read-only PREVIEW of what the next
// block's reward would be if produced right now - 90% of
// core.Treasury.CCTBurnedPendingReward, the exact formula
// applyBlockReward itself uses to actually pay out and reset that
// counter. Purely informational (never mutates state); used by the
// /validators API response and the delegator APY estimate so both
// reflect the network's real, currently-accruing reward rather than a
// stale fixed constant.
func pendingBurnBasedBlockReward() uint64 {
	return treasuryStore.Treasury.CCTBurnedPendingReward * 90 / 100
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
	// rather than split with delegators the way the PoSR block reward
	// is, since it's compensation for including the transaction, not a
	// staking yield.
	if feeCollected := cctStore.BalanceOf(cctTransferFeePoolAddress()); feeCollected != "0" {
		if cctStore.Transfer(cctTransferFeePoolAddress(), block.Validator, feeCollected) {
			wallet.Rewards += core.CctSmallestToWholeFloor(feeCollected)
		}
	}

	// Sweep every native symbol's marketplace platform-fee validator
	// share (see platformFeeValidatorPoolAddress/splitPlatformFee) the
	// same way, one native symbol at a time - a fixed, bounded list
	// (matching website/js/p2p.js's own p2pNativeSymbols) rather than
	// iterating tokenRegistry too, since developer-registered custom
	// tokens aren't part of the P2P Trading Center's native symbol set
	// this fee applies to.
	for _, symbol := range []string{"CCT", "ETH_CCC1", "USDC_CCC1", "BTC_CCC1", "REPT"} {
		poolStore, err := resolveTokenStore(symbol)
		if err != nil {
			continue
		}
		poolAddr := platformFeeValidatorPoolAddress(symbol)
		if poolBalance := poolStore.BalanceOf(poolAddr); poolBalance > 0 {
			poolStore.Transfer(poolAddr, block.Validator, poolBalance)
		}
	}

	totalDelegated := delegationStore.TotalDelegatedTo(block.Validator)

	// PoSR's block reward is tied directly to real fee-burning economic
	// activity - see core.Treasury.CCTBurnedPendingReward's doc
	// comment. Consumed and reset here, every block: a block produced
	// with no burn activity since the last one correctly pays zero
	// reward. Every mint below still routes through mintCCTCapped, so
	// this whole payout also respects MaxTotalCCTSupply - once the cap
	// is reached, minted amounts (and therefore Rewards credited)
	// correctly shrink toward zero rather than the ledger silently
	// exceeding the cap.
	reward := pendingBurnBasedBlockReward()
	treasuryStore.Treasury.CCTBurnedPendingReward = 0

	if totalDelegated == 0 {
		minted := mintCCTCappedWhole(block.Validator, reward)
		wallet.Rewards += core.CctSmallestToWholeFloor(minted)
	} else {
		status, _ := validatorStore.Get(block.Validator)
		var commissionBps uint64
		if status != nil {
			commissionBps = uint64(status.CommissionBps)
		}

		commission := reward * commissionBps / 10000
		poolShare := reward - commission

		mintedCommission := mintCCTCappedWhole(block.Validator, commission)
		wallet.Rewards += core.CctSmallestToWholeFloor(mintedCommission)

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
			mintedShare := mintCCTCappedWhole(delegator, share)
			dWallet.Rewards += core.CctSmallestToWholeFloor(mintedShare)
			distributed += share
		}

		// Integer-division remainder from the pro-rata split goes to the
		// validator rather than being lost or arbitrarily assigned to
		// whichever delegator sorts first - same "remainder stays
		// behind" rule reclaimDormantWallet already follows.
		if remainder := poolShare - distributed; remainder > 0 {
			mintedRemainder := mintCCTCappedWhole(block.Validator, remainder)
			wallet.Rewards += core.CctSmallestToWholeFloor(mintedRemainder)
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
		if reward := increaseTrust(wallet, core.DimensionContribution, blockProductionReputationGrowth, block.Timestamp); reward > 0 {
			mintRept(block.Validator, reward)
		}
	}
}

func applyTxOnce(tx core.Transaction) {
	// replayKeyForTx (below) is the actual H-03 fix: the dedup key is
	// no longer the attacker-choosable tx.ID field at all (for any
	// transaction timestamped at/after H03FixCutoverUnixSeconds) - see
	// its own doc comment. Computing it here, before authentication,
	// only short-circuits an exact repeat (same real signature bytes,
	// e.g. a genuine gossip/resync duplicate) - it does NOT yet mark
	// anything seen, so a forged transaction can never consume a slot
	// it hasn't earned by fully verifying below.
	replayKey := replayKeyForTx(tx)

	appliedTx.mu.Lock()
	if replayKey != "" && appliedTx.seen[replayKey] {
		appliedTx.mu.Unlock()
		return
	}
	appliedTx.mu.Unlock()

	if !verifyTxAuth(tx) {
		fmt.Println("rejected transaction with invalid/mismatched authentication:", tx.ID, tx.Type)
		return
	}

	// Only an authenticity- and structural-invariant-verified
	// transaction ever gets to consume its replay key. Re-checks
	// seen[] under the same lock in case another goroutine (e.g.
	// concurrent block processing) raced this one between the dedup
	// check above and here.
	appliedTx.mu.Lock()
	if replayKey != "" && appliedTx.seen[replayKey] {
		appliedTx.mu.Unlock()
		return
	}
	if replayKey != "" {
		appliedTx.seen[replayKey] = true
	}
	appliedTx.mu.Unlock()

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
	// this file relies on. Also captures PublicKeyHex here (see
	// core.Wallet's doc comment on that field) - the only place a
	// wallet's real public key is ever available server-side, since it
	// otherwise only exists transiently inside a live signed request.
	if tx.AuthAddress != "" {
		w := walletStore.Get(tx.AuthAddress)
		w.LastActivityAt = tx.Timestamp
		if tx.AuthPublicKey != "" {
			w.PublicKeyHex = tx.AuthPublicKey
		}
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
// H01FixCutoverUnixSeconds gates the tx.Timestamp/tx.AuthTimestamp
// binding check just below - see that check's doc comment for the
// full reasoning. Every transaction genuinely built by this codebase
// from this deploy onward already satisfies Timestamp==AuthTimestamp
// by construction (core.NewAuthenticatedTransaction), so this only
// ever matters for transactions that predate the fix: a fresh node
// resyncing/replaying the full historical chain must not reject
// already-confirmed, pre-fix blocks just because their Timestamp
// field was independently clock-derived at the time rather than
// bound to AuthTimestamp. AuthTimestamp itself can never be forged
// retroactively (it's part of what the original signature covers -
// see SignedEnvelope.CanonicalMessage), so gating on AuthTimestamp
// rather than the mutable Timestamp field is what makes this safe: an
// attacker can't fake an old-looking AuthTimestamp for a transaction
// they're constructing now without a genuinely old private-key
// signature over exactly that value, which they don't have.
// Deliberately several hours before actual deploy time as a buffer
// for anything already produced/pending under the pre-fix binary.
const H01FixCutoverUnixSeconds int64 = 1786492800 // 2026-08-12T00:00:00Z

// WalletRecoveryDiscontinuedCutoverUnixSeconds is the product decision
// (2026-08-16) to stop offering email+creation-year wallet recovery
// entirely, on testnet as well as mainnet - not just the narrower
// mainnet-only security hold audit finding C-04 already put in place.
// This mechanism was never real account recovery (see
// core/recovery.go's doc comment: it migrates balances to a brand new
// keypair, gated on facts - a linked email, a creation year - that are
// both derivable from public, on-chain data, not a real second
// factor) and shouldn't be offered as though it were an alternative to
// actually saving your BIP-39 backup phrase at wallet creation
// (website/register.html already does this correctly - the real fix
// here is removing the misleading "or set up email recovery instead"
// framing, not building a stronger replacement). Gated by a cutover,
// the same pattern H01FixCutoverUnixSeconds established just above:
// testnet already has real historical TxSetRecoveryEmail/
// TxRecoverWallet transactions from before this decision, and a node
// resyncing from genesis must still replay those exactly as they
// originally applied, so this only blocks transactions timestamped
// from the cutover onward, never retroactively invalidates history.
// Mainnet needs no such carve-out - audit finding C-04 already blocks
// it unconditionally via mainnetMode(), with no historical mainnet
// recovery transactions ever having existed.
const WalletRecoveryDiscontinuedCutoverUnixSeconds int64 = 1786924800 // 2026-08-17T00:00:00Z

// H03FixCutoverUnixSeconds is the full fix for audit finding H-03,
// replacing the earlier partial mitigation (moving the seen-marking to
// after verifyTxAuth, still in place below - see its own comments).
// That partial fix closed the "free, no signature needed" version of
// the attack; it deliberately left disclosed one residual gap: an
// attacker who genuinely controls SOME real private key could still
// sign a well-formed, authentically-signed transaction that copies a
// victim's tx.ID and is structurally valid enough to pass verifyTxAuth,
// even though it's doomed to fail some deeper semantic check (e.g.
// insufficient balance) - verifyTxAuth only validates authenticity and
// static structure, not runtime state, so it can't catch that class.
//
// The real fix, implemented here: for any transaction timestamped at
// or after this cutover, replayKeyForTx stops using the attacker-
// choosable tx.ID field entirely as the consensus replay key and uses
// a hash of tx.AuthSignature instead - a value cryptographically bound
// to the transaction's own real signing event. An attacker cannot
// choose what replay key their own transaction (forged or genuinely
// signed) consumes without already possessing the exact signature
// bytes of the transaction they're trying to poison, which by
// definition means it isn't a different, distinct transaction at all.
// This closes the attack completely, not just narrows it - simpler and
// smaller than the audit's own suggested alternative (replacing
// Transaction.ID as the primary identifier everywhere and extending
// core.ComputeBlockHash to cover the full canonical encoding): tx.ID
// itself is never signed (see core.SignedEnvelope's fields), so it was
// always free for the server to stop trusting for this one purpose
// without touching block hashing, historical IDs, or anything a
// frontend/API caller displays.
//
// Gated by AuthTimestamp (immutable, signed - see H01FixCutoverUnixSeconds's
// identical reasoning) so a node resyncing testnet's real history
// keeps deduplicating pre-fix transactions by tx.ID exactly as they
// were originally decided live, never retroactively re-litigating an
// already-settled replay decision.
const H03FixCutoverUnixSeconds int64 = 1786924800 // 2026-08-17T00:00:00Z

// TrustDimensionsCutoverUnixSeconds gates when the reputation-mutation
// call sites below switch from the old flat-Reputation-only functions
// (core.IncreaseWalletReputation/DecreaseWalletReputation) to the new
// dimension-aware ones (core.IncreaseWalletTrust/DecreaseWalletTrust,
// core/reputation.go) - same H01/H03-style cutover-timestamp pattern:
// every node must switch at the exact same point, or nodes straddling
// a rolling deploy would compute different Trust/Reputation state for
// the same transaction and fork. Every call site below is gated on
// tx.Timestamp (or the equivalent already-immutable, already-signed
// timestamp for that transaction type) against this constant, never on
// wall-clock or block.Timestamp alone unless that's what the site
// already used for H01/H03-style gating. Set a few days out from when
// this was added (2026-08-21) for the same rolling-deploy-safety
// margin H01/H03 used.
const TrustDimensionsCutoverUnixSeconds int64 = 1787529600 // 2026-08-24T00:00:00Z

// increaseTrust/decreaseTrust are the single chokepoint every
// reputation-mutating call site below goes through - routes to the new
// dimension-aware core.IncreaseWalletTrust/DecreaseWalletTrust once asOf
// (always the mutating transaction's own timestamp, never wall-clock)
// clears TrustDimensionsCutoverUnixSeconds, or the old flat
// core.IncreaseWalletReputation/DecreaseWalletReputation before it.
// Putting the branch here once, rather than repeating it at every call
// site, is what keeps the dimension migration below a one-line change
// per site instead of a five-line one.
func increaseTrust(w *core.Wallet, dim core.TrustDimension, amount uint64, asOf int64) uint64 {
	if asOf < TrustDimensionsCutoverUnixSeconds {
		return core.IncreaseWalletReputation(w, amount)
	}
	return core.IncreaseWalletTrust(w, dim, amount)
}

func decreaseTrust(w *core.Wallet, dim core.TrustDimension, amount uint64, asOf int64) {
	if asOf < TrustDimensionsCutoverUnixSeconds {
		core.DecreaseWalletReputation(w, amount)
		return
	}
	core.DecreaseWalletTrust(w, dim, amount)
}

// replayKeyForTx computes the consensus replay-dedup key for tx - see
// H03FixCutoverUnixSeconds's doc comment for the full reasoning. Pre-
// cutover transactions keep exactly their original tx.ID-based
// semantics; post-cutover transactions dedupe on a hash of their own
// signature instead, which is safe to compute even before verifyTxAuth
// has run (a collision here is only possible by already possessing the
// real signature bytes of a specific prior transaction, which makes it
// a genuine duplicate by definition, not a new attack surface).
func replayKeyForTx(tx core.Transaction) string {
	if tx.AuthTimestamp < H03FixCutoverUnixSeconds || tx.AuthSignature == "" {
		return tx.ID
	}
	sum := sha256.Sum256([]byte(tx.AuthSignature))
	return hex.EncodeToString(sum[:16])
}

// authorizedNodeReporterPublicKeys reads AUTHORIZED_NODE_REPORTER_PUBLIC_KEYS
// (comma-separated hex Ed25519 public keys) - an operator-controlled
// allowlist of node identities trusted to submit node-authored reports,
// same env-var-configured-allowlist pattern BRIDGE_VALIDATOR_ADDRESSES/
// ADMIN_MULTISIG_ADDRESSES already establish elsewhere in this
// codebase. This exists alongside (not instead of) the real PoSR
// ValidatorStore check below: a genuinely decentralized network should
// rely on staked, community-approved validators, but that pipeline is
// a real multi-day, wallet-signed onboarding process (see
// api/validator_application.go) - not something that can bootstrap
// itself the moment a fresh network's ValidatorStore is still empty.
// This lets an operator explicitly vouch for their own already-running
// node's identity (a value safe to read from data/identity.json's
// public field and set here - never the private key) without waiting
// on that pipeline, while still being exactly as revocable/auditable as
// every other env-var-configured trust boundary this project uses.
func authorizedNodeReporterPublicKeys() map[string]bool {
	raw := strings.TrimSpace(os.Getenv("AUTHORIZED_NODE_REPORTER_PUBLIC_KEYS"))
	if raw == "" {
		return nil
	}
	out := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		key := strings.ToLower(strings.TrimSpace(part))
		if key != "" {
			out[key] = true
		}
	}
	return out
}

// isAuthorizedNodeIdentity reports whether authPublicKey (hex-encoded)
// belongs to a node this network actually trusts to submit node-
// authored reports - either explicitly operator-allowlisted (see
// authorizedNodeReporterPublicKeys) or currently delegated block-
// production authority by some active, non-permanently-banned
// registered validator (core.ValidatorStatus.AuthorizedNodePublicKey,
// set only via a validator's own signed TxValidatorRegister). This is
// the real trust boundary node-authored reports (bridge deposits, vault
// release completion, slash reports, tx-hash records) need - see audit
// finding C-01. Proving a transaction is merely self-consistently
// signed (Data == AuthPayload, verifyTxAuth's pre-existing check) only
// shows SOME Ed25519 key signed it; anyone can generate one. This
// additionally proves the signer is a node this network's operator or
// its own staked validators actually chose to trust, not an arbitrary
// attacker-generated keypair with no stake, registration, or operator
// sign-off behind it.
func isAuthorizedNodeIdentity(authPublicKey string) bool {
	if authPublicKey == "" {
		return false
	}
	if authorizedNodeReporterPublicKeys()[strings.ToLower(authPublicKey)] {
		return true
	}
	for _, v := range validatorStore.Validators {
		if v.Active && !v.PermanentlyBanned && v.AuthorizedNodePublicKey == authPublicKey {
			return true
		}
	}
	return false
}

func verifyTxAuth(tx core.Transaction) bool {
	if err := core.VerifyTransactionAuth(tx); err != nil {
		return false
	}

	// Timestamp is a top-level Transaction field with no per-type Data
	// payload protecting it (unlike Amount/To/Data, which every case
	// below re-derives and compares against what was actually signed -
	// see this function's own doc comment). Without this,
	// core.ComputeBlockHash's tx.ID-only hashing (see audit finding
	// H-01) lets a block producer silently rewrite Timestamp after
	// signing with no detectable effect on the block hash - e.g.
	// backdating an unstake/undelegate request so its maturesAt is
	// already elapsed at confirmation, or manipulating
	// applyVaultGasPriceOracleUpdate's cooldown/SetAt timestamp.
	// AuthTimestamp is bound into the signature itself, so requiring
	// an exact match makes Timestamp exactly as tamper-evident as
	// every other signed field. Gated by H01FixCutoverUnixSeconds so a
	// fresh node replaying pre-fix chain history never breaks on it -
	// see that constant's doc comment.
	if tx.AuthTimestamp >= H01FixCutoverUnixSeconds && tx.Timestamp != tx.AuthTimestamp {
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

	case core.TxProposalPromote:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed proposalPromoteData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxRCInfuse:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed rcInfuseData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxPredictionVote:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed predictionVoteData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxExecutiveProposalCreate:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed executiveProposalCreateData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxExecutiveProposalVote:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed executiveProposalVoteData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxBridgeValidatorRegister:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed bridgeValidatorRegisterData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxVaultLockAttest:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed vaultLockAttestData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxVaultBurn:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed vaultBurnData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxVaultReleaseApprove:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed vaultReleaseApproveData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxVaultReleaseComplete:
		// Node-authored, not wallet-authored - same reasoning as
		// TxBridgeTxHashRecord above (recordVaultReleaseComplete is this
		// report's only submitter). Also requires the signer be an
		// authorized node identity (audit finding C-01) - without this,
		// anyone could forge a fake "release complete" report for
		// someone else's real pending withdrawal, permanently marking it
		// RELEASED (so the relay stops retrying) while the actual funds
		// never move.
		return isAuthorizedNodeIdentity(tx.AuthPublicKey) && tx.Data == string(tx.AuthPayload)

	case core.TxVaultGasPriceOracleUpdate:
		// Only proves a genuine signature bound to tx.From here - WHICH
		// address is actually authorized to move this parameter, and by
		// how much/how often, is enforced in
		// applyVaultGasPriceOracleUpdate, not here (matching
		// TxVaultLockAttest's own split between "is this really signed"
		// and "is this signer allowed to do this").
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed vaultGasPriceOracleUpdateData
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
		// Compare using bridgeWithdrawRequest (To/AmountWei only), NOT the
		// full bridgeWithdrawData - the client never signs FeeWei (it's
		// computed live, server-side, after signing), so comparing the
		// full struct would always fail once a real nonzero fee exists.
		// See bridgeWithdrawRequest's doc comment in eth_bridge_withdraw.go.
		var signed, claimed bridgeWithdrawRequest
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
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

	case core.TxTradeReview:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed tradeReviewData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxDisputeFaultPay, core.TxDisputeFaultDecline:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed disputeFaultChoiceData
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

	case core.TxP2PBuyerDispute:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed p2pBuyerDisputeData
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

	case core.TxCompanyUploadCertificate:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed companyUploadCertificateData
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

	case core.TxWalletConnectHub:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed walletConnectHubData
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

	case core.TxRequestContactEmailCode:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed requestContactEmailCodeData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxConfirmContactEmail:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed confirmContactEmailData
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

	case core.TxSponsoredPostCreate:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed sponsoredPostCreateData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxSponsoredPostReview:
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed sponsoredPostReviewData
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
		// without needing bespoke per-field comparison logic. Also
		// requires an authorized node identity (audit finding C-01) -
		// without this, an attacker could submit unlimited forged slash
		// reports under distinct self-generated addresses (reporter
		// counting only ever distinguished addresses, never verified
		// any of them were real validators) and Sybil-slash/permanently
		// ban an honest validator.
		return isAuthorizedNodeIdentity(tx.AuthPublicKey) && tx.Data == string(tx.AuthPayload)

	case core.TxTokenMint:
		// See verifyAdminTxAuth's doc comment: accepts either a real
		// signature from the configured admin wallet (independently
		// reverifiable by every node) or the legacy node-authored
		// shape kept only for the migration window.
		return verifyAdminTxAuth(tx)

	case core.TxSetProtocolManifest:
		// Deliberately NOT verifyAdminTxAuth directly - see
		// verifySetProtocolManifestAuth's own doc comment for why this
		// transaction type needs a strictly narrower check than the
		// generic admin-tx fallback.
		return verifySetProtocolManifestAuth(tx)

	case core.TxBridgeDeposit:
		// Node-authored, not wallet-authored - same reasoning as
		// TxSlashReport above (api/eth_bridge.go's
		// pollAndReportBridgeDeposit is this report's only submitter).
		// Also requires an authorized node identity (audit finding
		// C-01) - without this, anyone could forge a deposit report for
		// an attacker-controlled registered address with a fictional,
		// inflated external balance and mint unbacked ETHB. (Deposits
		// are additionally held entirely for now - see
		// api/security_hold.go - this closes the underlying auth gap
		// regardless of whether that hold is ever lifted.)
		return isAuthorizedNodeIdentity(tx.AuthPublicKey) && tx.Data == string(tx.AuthPayload)

	case core.TxBridgeBtcDeposit:
		// Node-authored, not wallet-authored - same reasoning as
		// TxBridgeDeposit above (api/btc_bridge.go's
		// pollAndReportBtcDeposit is this report's only submitter). Same
		// C-01 fix as TxBridgeDeposit.
		return isAuthorizedNodeIdentity(tx.AuthPublicKey) && tx.Data == string(tx.AuthPayload)

	case core.TxBridgeTxHashRecord:
		// Node-authored, not wallet-authored - same reasoning as
		// TxSlashReport/TxBridgeDeposit above (recordBridgeTxHash is
		// this report's only submitter). Same C-01 fix.
		return isAuthorizedNodeIdentity(tx.AuthPublicKey) && tx.Data == string(tx.AuthPayload)

	case core.TxBridgeBtcWithdraw:
		// Genuinely wallet-authored - the user themselves initiated this
		// real BTC-network send and their signature is what
		// HandleBtcBroadcast now requires (see that handler's doc
		// comment). Compare using bridgeBtcWithdrawRequest (To/AmountSats/
		// FeeSats only), NOT the full bridgeBtcWithdrawData - BtcTxid is
		// never signed by the client (it isn't known until after the real
		// broadcast succeeds), so comparing the full struct would always
		// fail once a real txid exists. See bridgeBtcWithdrawRequest's
		// doc comment in bridge_tx_hash.go.
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed bridgeBtcWithdrawRequest
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxFairValueSwap:
		// Genuinely wallet-authored - it's the caller's own balance
		// that moves. Compared as fairValueSwapRequest (NOT the full
		// fairValueSwapData) on both sides deliberately - ToAmount/
		// UsdValue/CctValue are computed by the server after signing,
		// so the client's signature could never have covered them;
		// comparing the full struct would make this reject every real
		// transaction the moment those computed fields are nonzero.
		if tx.AuthAddress != tx.From {
			return false
		}
		var signedSwap, claimedSwap fairValueSwapRequest
		if err := json.Unmarshal(tx.AuthPayload, &signedSwap); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimedSwap); err != nil {
			return false
		}
		return signedSwap == claimedSwap

	case core.TxTreasuryPurchaseConfirm, core.TxTreasurySetSalePrice, core.TxTreasurySetPaused, core.TxTreasurySetAddress, core.TxTreasuryPurchaseSendInstructions, core.TxAnnouncementCreate, core.TxAdminSetParam:
		// Admin-only - see verifyAdminTxAuth.
		return verifyAdminTxAuth(tx)

	case core.TxTreasuryPurchaseBuyerMarkedPaid:
		// Node-authored, not wallet-authored - same reasoning as
		// TxSlashReport above (HandleBuyerConfirmPurchasePayment is this
		// report's only submitter).
		return tx.Data == string(tx.AuthPayload)

	case core.TxGenesisPoolAllocation, core.TxGenesisPoolWithdraw, core.TxTokenomicsDecimalMigration:
		// Admin-only - see verifyAdminTxAuth.
		return verifyAdminTxAuth(tx)

	case core.TxAdminTransfer:
		// Admin-only - see verifyAdminTxAuth. Deliberately does NOT
		// additionally check tx.From against any signature, since the
		// entire point of this transaction type is moving value out of
		// an address whose own key is unreachable.
		return verifyAdminTxAuth(tx)

	case core.TxAdminNormalizeCcc1Balance:
		// Admin-only - see verifyAdminTxAuth. Same "moves value out of
		// an address with no reachable signature" reasoning as
		// TxAdminTransfer above - the whole point here is the source
		// address is an un-normalized artifact, not a real signable key.
		return verifyAdminTxAuth(tx)

	case core.TxAdminRefundStuckVaultBurn:
		// Admin-only - see verifyAdminTxAuth.
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

	case core.TxTreasuryPurchaseMarkPaidByBuyer:
		// Ordinary wallet-signed action, same shape as
		// TxTreasuryPurchaseRequest above - the caller must have signed
		// for their own address, and the signed payload must exactly
		// match what got applied. HandleTreasuryMarkPaidByBuyer already
		// checks the purchase's own BuyerAddress matches at submission
		// time; MarkBuyerPaidBySignedRequest (core/treasury.go)
		// re-checks it again at apply time, independently of this.
		if tx.AuthAddress != tx.From {
			return false
		}
		var signed, claimed treasuryMarkPaidByBuyerData
		if err := json.Unmarshal(tx.AuthPayload, &signed); err != nil {
			return false
		}
		if err := json.Unmarshal([]byte(tx.Data), &claimed); err != nil {
			return false
		}
		return signed == claimed

	case core.TxTreasurySetBankAccount:
		// Admin-only - see verifyAdminTxAuth.
		return verifyAdminTxAuth(tx)

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

	case core.TxBridgeTxHashRecord:
		applyBridgeTxHashRecord(tx)

	case core.TxBridgeBtcWithdraw:
		applyBridgeBtcWithdraw(tx)

	case core.TxFairValueSwap:
		applyFairValueSwap(tx)

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

	case core.TxProposalPromote:
		applyProposalPromote(tx)

	case core.TxRCInfuse:
		applyRCInfuse(tx)

	case core.TxPredictionVote:
		applyPredictionVote(tx)

	case core.TxBridgeValidatorRegister:
		applyBridgeValidatorRegister(tx)

	case core.TxVaultLockAttest:
		applyVaultLockAttest(tx)

	case core.TxVaultBurn:
		applyVaultBurn(tx)

	case core.TxVaultReleaseApprove:
		applyVaultReleaseApprove(tx)

	case core.TxVaultReleaseComplete:
		applyVaultReleaseComplete(tx)

	case core.TxVaultGasPriceOracleUpdate:
		applyVaultGasPriceOracleUpdate(tx)

	case core.TxExecutiveProposalCreate:
		applyExecutiveProposalCreate(tx)

	case core.TxExecutiveProposalVote:
		applyExecutiveProposalVote(tx)

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

	case core.TxTradeReview:
		applyTradeReview(tx)

	case core.TxDisputeFaultPay:
		applyDisputeFaultPay(tx)

	case core.TxDisputeFaultDecline:
		applyDisputeFaultDecline(tx)

	case core.TxP2PExternalAccept:
		applyP2PExternalAccept(tx)

	case core.TxP2PSellerConfirmPaid:
		applyP2PSellerConfirmPaid(tx)

	case core.TxP2PSellerDispute:
		applyP2PSellerDispute(tx)

	case core.TxP2PBuyerDispute:
		applyP2PBuyerDispute(tx)

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

	case core.TxCompanyUploadCertificate:
		applyCompanyUploadCertificate(tx)

	case core.TxCompanyReassignModerator:
		applyCompanyReassignModerator(tx)

	case core.TxCompanyUpdateContact:
		applyCompanyUpdateContact(tx)

	case core.TxWalletConnectHub:
		applyWalletConnectHub(tx)

	case core.TxBuyRequestCreate:
		applyBuyRequestCreate(tx)

	case core.TxBuyRequestFulfill:
		applyBuyRequestFulfill(tx)

	case core.TxBuyRequestCancel:
		applyBuyRequestCancel(tx)

	case core.TxSetRecoveryEmail:
		applySetRecoveryEmail(tx)

	case core.TxRequestContactEmailCode:
		applyRequestContactEmailCode(tx)

	case core.TxConfirmContactEmail:
		applyConfirmContactEmail(tx)

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

	case core.TxSponsoredPostCreate:
		applySponsoredPostCreate(tx)

	case core.TxSponsoredPostReview:
		applySponsoredPostReview(tx)

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

	case core.TxTreasuryPurchaseSendInstructions:
		applyTreasuryPurchaseSendInstructions(tx)

	case core.TxTreasuryPurchaseBuyerMarkedPaid:
		applyTreasuryPurchaseBuyerMarkedPaid(tx)

	case core.TxTreasuryPurchaseMarkPaidByBuyer:
		applyTreasuryPurchaseMarkPaidByBuyer(tx)

	case core.TxTreasurySetBankAccount:
		applyTreasurySetBankAccount(tx)

	case core.TxTreasurySetSalePrice:
		applyTreasurySetSalePrice(tx)

	case core.TxTreasurySetPaused:
		applyTreasurySetPaused(tx)

	case core.TxTreasurySetAddress:
		applyTreasurySetAddress(tx)

	case core.TxAdminSetParam:
		applyAdminSetParam(tx)

	case core.TxSetProtocolManifest:
		applySetProtocolManifest(tx)

	case core.TxGenesisPoolAllocation:
		applyGenesisPoolAllocation(tx)

	case core.TxGenesisPoolWithdraw:
		applyGenesisPoolWithdraw(tx)

	case core.TxTokenomicsDecimalMigration:
		applyTokenomicsDecimalMigration(tx)

	case core.TxAdminTransfer:
		applyAdminTransfer(tx)

	case core.TxAdminNormalizeCcc1Balance:
		applyAdminNormalizeCcc1Balance(tx)

	case core.TxAdminRefundStuckVaultBurn:
		applyAdminRefundStuckVaultBurn(tx)

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
	// Authoritative half of the mainnet block HandleTokenMint's own
	// submission-time check adds - this is the one that actually
	// matters for consensus-correctness, since a transaction crafted
	// directly (bypassing the HTTP handler entirely) still has to pass
	// through here before it takes effect. mainnetMode() is a fixed,
	// deployment-time property of this process, not something that can
	// vary per-transaction, so this can never diverge between nodes on
	// the same network.
	if mainnetMode() {
		return
	}
	// Same incident class as applyAdminTransfer's own doc comment: an
	// operator typing a destination address in its normal "cc"-prefixed
	// display form would otherwise mint to a key the recipient's real
	// wallet can never reach (normalizeEthAddress alone only handles
	// 0x-style checksum casing, not this address family's own cosmetic
	// prefix).
	data.Address = normalizeEthAddress(normalizeCreditChainAddress(data.Address))

	if data.Symbol == "" || data.Symbol == "CCT" {
		mintCCTCappedWhole(data.Address, data.Amount)
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
	if !core.ValidPurchaseMethod(data.Method) {
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
		Method:         data.Method,
		BuyerEmail:     data.BuyerEmail,
		BuyerName:      data.BuyerName,
		BuyerContact:   data.BuyerContact,
		Currency:       data.Currency,
		Status:         core.PurchaseAwaitingPayment,
		CreatedAt:      tx.Timestamp,
	})
}

func applyTreasuryPurchaseMarkPaidByBuyer(tx core.Transaction) {
	var data treasuryMarkPaidByBuyerData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	treasuryStore.MarkBuyerPaidBySignedRequest(data.PurchaseID, tx.From, tx.Timestamp)
}

func applyTreasurySetBankAccount(tx core.Transaction) {
	var data adminSetBankAccountData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.Currency == "" || data.AccountName == "" || data.AccountNumber == "" || data.BankName == "" {
		return
	}
	treasuryStore.SetBankAccount(data.BankAccountDetails)
}

type treasuryPurchaseSendInstructionsData struct {
	adminSendPurchaseInstructionsData
	Token string `json:"token"`
}

// applyTreasuryPurchaseSendInstructions persists the admin-supplied
// instructions and token exactly as submitted at HTTP-handler time -
// the email itself already went out optimistically there (see
// HandleAdminSendPurchaseInstructions's doc comment), so this only
// needs to make the replicated state match what every node's admin
// dashboard should now show.
func applyTreasuryPurchaseSendInstructions(tx core.Transaction) {
	var data treasuryPurchaseSendInstructionsData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.Instructions == "" || data.Token == "" {
		return
	}

	treasuryStore.SendInstructions(data.PurchaseID, data.Instructions, data.Token, tx.Timestamp)
}

type treasuryPurchaseBuyerMarkedPaidData struct {
	PurchaseID string `json:"purchaseId"`
	Token      string `json:"token"`
}

// applyTreasuryPurchaseBuyerMarkedPaid re-validates the token against
// replicated state (never trusts the submitting node's own fast-fail
// check) before flipping the purchase to BUYER_MARKED_PAID - see
// core.TreasuryStore.MarkBuyerPaid's doc comment for why this is
// informational only, not a funds-release trigger.
func applyTreasuryPurchaseBuyerMarkedPaid(tx core.Transaction) {
	var data treasuryPurchaseBuyerMarkedPaidData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	treasuryStore.MarkBuyerPaid(data.PurchaseID, data.Token, tx.Timestamp)
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
	if !ok || purchase.Status == core.PurchaseConfirmed {
		return
	}

	amountSmallest := core.FormatCctSmallest(core.CctWholeToSmallest(purchase.CCTAmount))
	if !cctStore.Transfer(treasuryStore.Treasury.FoundationAddress, purchase.BuyerAddress, amountSmallest) {
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

	cctAmount, err := core.ParseCctSmallest(data.CCTAmount)
	if err != nil || cctAmount.Sign() == 0 || data.TokenAmount == 0 {
		return
	}
	if poolTotalShares, _ := core.ParseCctSmallest(pool.TotalShares); poolTotalShares.Sign() > 0 && !ratioMatches(data.CCTAmount, data.TokenAmount, pool.CCTReserve, pool.TokenReserve) {
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

	if !cctStore.Transfer(tx.From, escrow, data.CCTAmount) {
		return
	}
	if !store.Transfer(tokenSide, escrow, data.TokenAmount) {
		// Refund the CCT side - this deposit isn't going through.
		cctStore.Transfer(escrow, tx.From, data.CCTAmount)
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
	cctStore.Transfer(escrow, tx.From, cctOut)
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
		if !cctStore.Transfer(tx.From, escrow, data.AmountIn) {
			return
		}
		amountOut, ok := pool.SwapCCTForToken(data.AmountIn)
		if !ok {
			cctStore.Transfer(escrow, tx.From, data.AmountIn)
			return
		}
		store.Transfer(escrow, tokenSide, amountOut)
		recordPoolPrice(data.Symbol, pool, tx.Timestamp)

	case "token_to_cct":
		amountIn, err := parseUint64Field(data.AmountIn)
		if err != nil || amountIn == 0 {
			return
		}
		if !store.Transfer(tokenSide, escrow, amountIn) {
			return
		}
		amountOut, ok := pool.SwapTokenForCCT(amountIn)
		if !ok {
			store.Transfer(escrow, tokenSide, amountIn)
			return
		}
		cctStore.Transfer(escrow, tx.From, amountOut)
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

	if data.FromSymbol == data.ToSymbol {
		return
	}

	switch {
	case data.FromSymbol == "CCT":
		applySwapCCTToToken(tx.From, tx.AuthPublicKey, data.ToSymbol, data.AmountIn, tx.Timestamp)

	case data.ToSymbol == "CCT":
		amountIn, err := parseUint64Field(data.AmountIn)
		if err != nil || amountIn == 0 {
			return
		}
		applySwapTokenToCCT(tx.From, tx.AuthPublicKey, data.FromSymbol, amountIn, tx.Timestamp)

	default:
		amountIn, err := parseUint64Field(data.AmountIn)
		if err != nil || amountIn == 0 {
			return
		}
		applySwapTokenToToken(tx.From, tx.AuthPublicKey, data.FromSymbol, data.ToSymbol, amountIn, tx.Timestamp)
	}
}

func applySwapCCTToToken(from, publicKey, toSymbol string, amountIn string, timestamp int64) {
	cctAmount, err := core.ParseCctSmallest(amountIn)
	if err != nil || cctAmount.Sign() == 0 {
		return
	}
	toStore, toPool, err := lookupCustomToken(toSymbol)
	if err != nil {
		return
	}
	toSide := resolveBalanceAddress(from, publicKey, toSymbol)
	escrow := poolAddress(toSymbol)

	if !cctStore.Transfer(from, escrow, amountIn) {
		return
	}
	amountOut, ok := toPool.SwapCCTForToken(amountIn)
	if !ok {
		cctStore.Transfer(escrow, from, amountIn)
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
	cctStore.Transfer(escrow, from, amountOut)
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
	cctStore.Transfer(fromEscrow, toEscrow, intermediateCCT)
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
	skimPlatformFee(wantStore, offer.WantSymbol, creatorWantSide, offer.WantAmount)
	applyPlatformFee(offerStore, offer.OfferSymbol, p2pOfferEscrowAddress(offer.ID), acceptorOfferSide, offer.OfferAmount, "")

	if reward := increaseTrust(walletStore.Get(tx.From), core.DimensionTrading, ReputationDeltaP2PTrade, tx.Timestamp); reward > 0 {
		mintRept(tx.From, reward)
	}
	if reward := increaseTrust(walletStore.Get(offer.Creator), core.DimensionTrading, ReputationDeltaP2PTrade, tx.Timestamp); reward > 0 {
		mintRept(offer.Creator, reward)
	}
	// Both parties earn the same amount for a P2P trade actually
	// completing - recorded against the acceptor (tx.From) here since
	// that's this transaction's own authenticated actor; the creator's
	// identical gain is described in the explorer's summary text
	// instead of a second event record (see buildTransactionSummary).
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: tx.From, Delta: int64(ReputationDeltaP2PTrade),
		Reason: "P2P trade completed", Timestamp: tx.Timestamp, Dimension: core.DimensionTrading.String(),
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

	if _, ok := p2pOfferStore.BeginExternalSettlement(offer.ID, tx.From, tx.AuthPublicKey, tx.Timestamp); !ok {
		// Someone else's accept for this same offer landed first, or it
		// was no longer OPEN/EXTERNAL by the time this confirmed - no
		// trade actually started, so no "your trade has started" email
		// should go out (previously this return value was silently
		// discarded, which would have fired the notification below
		// unconditionally regardless of whether anything really began).
		return
	}
	notifyTradeStarted(offer)
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
	releaseEscrowToBuyer(offer, offer.AcceptedBy, offer.AcceptedByPublicKey, "")
}

func applyP2PSellerDispute(tx core.Transaction) {
	var data p2pSellerDisputeData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	offer, ok := p2pOfferStore.DisputeAwaitingPayment(data.OfferID, tx.From)
	if !ok {
		return
	}
	moderatorStore.RecordDisputeAssigned(offer.ModeratorAddress)
	notifyDisputeOpened(offer, tx.From)
}

func applyP2PBuyerDispute(tx core.Transaction) {
	var data p2pBuyerDisputeData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	offer, ok := p2pOfferStore.DisputeAwaitingPaymentByBuyer(data.OfferID, tx.From, tx.Timestamp)
	if !ok {
		return
	}
	moderatorStore.RecordDisputeAssigned(offer.ModeratorAddress)
	notifyDisputeOpened(offer, tx.From)
}

func applyP2PBuyerMarkPaid(tx core.Transaction) {
	var data p2pBuyerMarkPaidData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	p2pOfferStore.MarkBuyerPaid(data.OfferID, tx.From, tx.Timestamp)
}

// releaseEscrowToBuyer moves an EXTERNAL offer's locked OfferAmount to
// the buyer - shared by the buyer's own agree-confirmation, the
// timeout-based auto-release sweep, and the moderator's dispute
// resolution, since all three end the same way once the trade is
// decided in the buyer's favor. resolvingModerator is "" for the first
// two (no dispute was ever resolved) and the real moderator address
// for the third - see applyPlatformFee's own doc comment.
func releaseEscrowToBuyer(offer *core.P2POffer, buyerAddress, buyerPublicKey, resolvingModerator string) {
	offerStore, err := resolveTokenStore(offer.OfferSymbol)
	if err != nil {
		return
	}

	buyerSide := resolveBalanceAddress(buyerAddress, buyerPublicKey, offer.OfferSymbol)
	applyPlatformFee(offerStore, offer.OfferSymbol, p2pOfferEscrowAddress(offer.ID), buyerSide, offer.OfferAmount, resolvingModerator)
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
		releaseEscrowToBuyer(offer, tx.From, tx.AuthPublicKey, "")
	} else {
		moderatorStore.RecordDisputeAssigned(offer.ModeratorAddress)
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
	moderatorStore.RecordDisputeResolved(tx.From)
	notifyDisputeResolved(offer, data.Release)

	if data.Release {
		// Moderator sided with the buyer - the buyer really paid and
		// the seller wrongly disputed/failed to confirm. Penalize the
		// seller (Creator), the party found to be acting in bad faith.
		// A release is a genuinely completed trade, so it collects the
		// normal platform fee - paid directly to tx.From (the moderator
		// who actually did the work of resolving this), not pooled (see
		// releaseEscrowToBuyer's own doc comment).
		releaseEscrowToBuyer(offer, offer.AcceptedBy, offer.AcceptedByPublicKey, tx.From)
		decreaseTrust(walletStore.Get(offer.Creator), core.DimensionDispute, ReputationDeltaP2PFraud, tx.Timestamp)
		reputationEventStore.Record(&core.ReputationEvent{
			TxID: tx.ID, Target: offer.Creator, Delta: -int64(ReputationDeltaP2PFraud),
			Reason: "Moderator found this trade claim to be false", Timestamp: tx.Timestamp, Dimension: core.DimensionDispute.String(),
		})
		return
	}

	// Moderator sided with the seller - the buyer's payment claim was
	// false. A refund collects no trade fee at all (see
	// core/platform_fee.go's own doc comment: "never from a refund"),
	// so there's nothing to pay the moderator from here the way a
	// release naturally provides. Rather than penalizing the buyer's
	// reputation immediately and unconditionally, give them
	// core.DisputeFaultChoiceWindowSeconds to choose: pay the
	// moderator's would-be fee directly (no reputation penalty) or
	// explicitly decline (reputation penalty, no fee). Neither choice
	// in time means both apply automatically - see
	// resolveExpiredDisputeFaultChoices.
	refundEscrowToSeller(offer)
	feeAmount := core.PlatformFeeAmount(offer.OfferAmount) * 10 / 100
	disputeFaultChoiceStore.Create(&core.DisputeFaultChoice{
		OfferID: offer.ID, AtFaultAddress: offer.AcceptedBy, ResolvingModerator: tx.From,
		Symbol: offer.OfferSymbol, FeeAmount: feeAmount, CreatedAt: tx.Timestamp,
		Deadline: tx.Timestamp + core.DisputeFaultChoiceWindowSeconds, Status: core.DisputeFaultChoicePending,
	})
	notifyUser(offer.AcceptedBy, "DISPUTE_FAULT_CHOICE",
		fmt.Sprintf("A moderator found your payment claim on trade %s to be false. You have 72 hours to either pay %d %s to the moderator or accept a reputation penalty - doing neither means both apply.", offer.ID, feeAmount, offer.OfferSymbol),
		offer.ID)
}

// applyDisputeFaultPay is the at-fault party choosing to pay the
// moderator's fee directly instead of losing reputation - re-validates
// everything HandleDisputeFaultChoicePay already checked at submission
// time (never trusted from there alone), same convention as every
// other apply function in this file.
func applyDisputeFaultPay(tx core.Transaction) {
	var data disputeFaultChoiceData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	choice, ok := disputeFaultChoiceStore.Get(data.OfferID)
	if !ok || choice.AtFaultAddress != tx.From || choice.Status != core.DisputeFaultChoicePending || tx.Timestamp > choice.Deadline {
		return
	}

	if choice.FeeAmount > 0 {
		store, err := resolveTokenStore(choice.Symbol)
		if err != nil {
			return
		}
		if !store.Transfer(tx.From, choice.ResolvingModerator, choice.FeeAmount) {
			return
		}
	}
	disputeFaultChoiceStore.Pay(data.OfferID, tx.From, tx.Timestamp)
}

// applyDisputeFaultDecline is the at-fault party explicitly accepting
// the reputation penalty instead of paying - resolves immediately
// rather than making them wait out the full window.
func applyDisputeFaultDecline(tx core.Transaction) {
	var data disputeFaultChoiceData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	if _, ok := disputeFaultChoiceStore.Decline(data.OfferID, tx.From, tx.Timestamp); !ok {
		return
	}

	wallet := walletStore.Get(tx.From)
	decreaseTrust(wallet, core.DimensionDispute, ReputationDeltaP2PFraud, tx.Timestamp)
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: tx.From, Delta: -int64(ReputationDeltaP2PFraud),
		Reason: "Declined to pay a disputed trade's moderator fee", Timestamp: tx.Timestamp, Dimension: core.DimensionDispute.String(),
	})
}

// resolveExpiredDisputeFaultChoices applies BOTH penalties to every
// dispute fault choice whose 72-hour window passed with neither a pay
// nor a decline confirmed - block-timestamp-driven, same determinism
// guarantee as every other periodic sweep in this file (see
// core.DisputeFaultChoiceStore.SweepExpired's own doc comment on why
// its return order is deterministic). The forced fee transfer is
// best-effort - Transfer silently no-ops on insufficient balance, same
// as everywhere else in this codebase; nothing here can force money
// that genuinely isn't there.
func resolveExpiredDisputeFaultChoices(block core.Block) {
	for _, choice := range disputeFaultChoiceStore.SweepExpired(block.Timestamp) {
		wallet := walletStore.Get(choice.AtFaultAddress)
		decreaseTrust(wallet, core.DimensionDispute, ReputationDeltaP2PFraud, block.Timestamp)
		reputationEventStore.Record(&core.ReputationEvent{
			TxID: "", Target: choice.AtFaultAddress, Delta: -int64(ReputationDeltaP2PFraud),
			Reason: "Did not respond to a disputed trade's moderator fee choice in time", Timestamp: block.Timestamp, Dimension: core.DimensionDispute.String(),
		})

		if choice.FeeAmount == 0 {
			continue
		}
		store, err := resolveTokenStore(choice.Symbol)
		if err != nil {
			continue
		}
		store.Transfer(choice.AtFaultAddress, choice.ResolvingModerator, choice.FeeAmount)
	}
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
			releaseEscrowToBuyer(offer, offer.AcceptedBy, offer.AcceptedByPublicKey, "")

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
// position, oETH receipt balance, or real STETH_CCC1 vault balance into
// collateralStore - re-checked here against current balances, not
// trusted from submission time, since any of them could have moved
// (e.g. an unstake, a transfer, or another lock) between submission and
// confirmation.
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

	case "STETH_CCC1":
		// Unlike CCT/OETH (both pure in-memory counters with nothing
		// real backing them until now), STETH_CCC1 is a real vault
		// balance - see api/vault_token.go's stethCcc1Store. Locking it
		// as collateral actually moves it out of the caller's balance
		// into a dedicated escrow address, the same "real balance,
		// really moved" pattern applyStake already uses for ETH_CCC1.
		balanceAddr := resolveBalanceAddress(tx.From, tx.AuthPublicKey, "STETH_CCC1")
		if !stethCcc1TransferAdapter.Transfer(balanceAddr, collateralEscrowAddress("STETH_CCC1"), data.Amount) {
			return
		}

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

	maturesAt := tx.Timestamp + int64(governanceParamsStore.GetOrDefault("UnbondingPeriodSeconds", uint64(core.UnbondingPeriodSeconds)))
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

	// ETHTEST is a legacy symbol (retired from new staking, see
	// applyStake) whose oETH redemption still goes through the real
	// swap round trip - see creditMaturedETHTestViaSwap's doc comment
	// for why (it predates ETHB and was built around ETHTEST being a
	// valueless synthetic faucet token, where swap-based redemption
	// deliberately discourages free-money gaming). Kept exactly as-is,
	// unmodified, purely for backward compatibility with any
	// already-queued unbonding entry from before this migration - see
	// api/legacy_stake_migration.go for how outstanding ETHTEST stake
	// itself gets closed out going forward.
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

	// ETHB is the live stakeable "ETH" asset (see applyStake) and also
	// mints the same oETH receipt ETHTEST used to - burned here on the
	// same "unconditional, keyed by entry.Address" terms as ETHTEST's
	// burn above. Unlike ETHTEST, ETHB is a real, custodially-backed
	// 1:1 representation of bridged Sepolia ETH (see bridgedEthStore's
	// doc comment in api/token.go: deliberately never floated against
	// CCT via an AMM) - so its redemption is the same plain 1:1
	// escrow transfer every other non-ETHTEST symbol already gets
	// below, not a swap round trip. Falls through to the generic path
	// after the burn.
	if symbol == "ETHB" {
		w := walletStore.Get(entry.Address)
		if w.OETHBalance >= entry.Amount {
			w.OETHBalance -= entry.Amount
		} else {
			w.OETHBalance = 0
		}
	}

	if symbol == "CCT" {
		smallest := core.FormatCctSmallest(core.CctWholeToSmallest(entry.Amount))
		cctStore.Transfer(stakeEscrowAddress("CCT"), creditAddress, smallest)
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

		core.ApplyDoubleSign(v, tx.Timestamp >= TrustDimensionsCutoverUnixSeconds)

		// v.Wallet is a value copy (see v.Wallet = *wallet above) - both
		// the flat mirror AND the dimension struct it was derived from
		// need copying back, or a dimensioned penalty would silently be
		// lost (only ApplyDoubleSign's local copy would have it).
		wallet.Reputation = v.Wallet.Reputation
		wallet.Trust = v.Wallet.Trust
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

		core.ApplyMissedBlock(v, tx.Timestamp >= TrustDimensionsCutoverUnixSeconds)

		// See the identical copy-back comment in the double_sign case
		// above - v.Wallet is a value copy, Trust must come back too.
		wallet.Reputation = v.Wallet.Reputation
		wallet.Trust = v.Wallet.Trust
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
	cctStore.Burn(stakeEscrowAddress("CCT"), core.FormatCctSmallest(core.CctWholeToSmallest(seized)))
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
	if depositHeld("BRIDGE_DEPOSIT", tx.ID) {
		return
	}

	var data bridgeDepositReportData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	// Look up the watched address first (the free, self-custodial path
	// every current UI flow uses - see core/eth_address.go's doc
	// comment), falling back to the old paid custodial account only for
	// backward compatibility with anything provisioned before this
	// change. Both share the same field shape needed below, tracked
	// here as plain locals so the rest of this function doesn't need to
	// care which store the account actually lives in.
	var accountPublicKey, accountLastCreditedWei, accountUncreditedExcessWei string
	watched, watchedOK := ethAddressStore.Get(data.CreditChainAddress)
	custodial, custodialOK := ethCustodialStore.Get(data.CreditChainAddress)
	switch {
	case watchedOK:
		accountPublicKey = watched.CreditChainPublicKey
		accountLastCreditedWei = watched.LastCreditedWei
		accountUncreditedExcessWei = watched.UncreditedExcessWei
	case custodialOK:
		accountPublicKey = custodial.CreditChainPublicKey
		accountLastCreditedWei = custodial.LastCreditedWei
		accountUncreditedExcessWei = custodial.UncreditedExcessWei
	default:
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
	if accountLastCreditedWei != "" {
		if _, ok := baseline.SetString(accountLastCreditedWei, 10); !ok {
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

	// Account-total holdings cap (see api/account_holdings_cap.go) -
	// replaces the earlier flat per-transaction native-unit cap with a
	// per-account TOTAL BTCB+ETHB USD-value limit, admin-settable via
	// EthUsdCentsPerEth/BtcUsdCentsPerBtc/AccountHoldingsCapUsdCents
	// (see api/admin_params.go's allowlist) so every node checks the
	// identical replicated rates rather than a locally-polled live
	// price. If the admin hasn't set both USD rates yet, this is a
	// no-op (full credit, today's unlimited behavior) - same fail-open
	// convention the old per-tx cap used. Excess is never dropped -
	// it's credited later once the account has headroom again or the
	// excess is handled manually, tracked in UncreditedExcessWei.
	creditAmount, excessAmount := capEthDepositToAccountHoldingsLimit(
		data.CreditChainAddress, accountPublicKey, delta.Uint64(),
	)
	if excessAmount > 0 {
		existingExcess := new(big.Int)
		if accountUncreditedExcessWei != "" {
			if _, ok := existingExcess.SetString(accountUncreditedExcessWei, 10); !ok {
				existingExcess.SetInt64(0)
			}
		}
		accountUncreditedExcessWei = new(big.Int).Add(existingExcess, new(big.Int).SetUint64(excessAmount)).String()
	}

	ethbAddress := resolveBalanceAddress(data.CreditChainAddress, accountPublicKey, "ETHB")
	bridgedEthStore.Mint(ethbAddress, creditAmount)
	// LastCreditedWei always advances to the FULL observed balance, not
	// just what got credited - it's the baseline for the NEXT deposit's
	// delta, so an over-cap excess is only ever counted once, not
	// re-flagged on every subsequent poll. Written back to whichever
	// store the account actually came from.
	if watchedOK {
		watched.LastCreditedWei = data.NewBalanceWei
		watched.UncreditedExcessWei = accountUncreditedExcessWei
	} else {
		custodial.LastCreditedWei = data.NewBalanceWei
		custodial.UncreditedExcessWei = accountUncreditedExcessWei
	}
}

// applyBridgeBtcDeposit credits BTCB (satoshi-denominated) once a
// TxBridgeBtcDeposit report - see api/btc_bridge.go's
// pollAndReportBtcDeposit - confirms. Same convergence property as
// applyBridgeDeposit: only the node a given address was registered on
// will ever detect and submit reports for it, but this apply function
// runs identically on every node once the report IS confirmed.
func applyBridgeBtcDeposit(tx core.Transaction) {
	if depositHeld("BRIDGE_BTC_DEPOSIT", tx.ID) {
		return
	}

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

	// Account-total holdings cap - same mechanism/reasoning as
	// applyBridgeDeposit's ETH cap check above (see
	// api/account_holdings_cap.go), replacing the earlier flat per-
	// transaction native-sats cap with a per-account TOTAL BTCB+ETHB
	// USD-value limit.
	creditAmount, excessAmount := capBtcDepositToAccountHoldingsLimit(
		data.CreditChainAddress, watched.CreditChainPublicKey, delta,
	)
	if excessAmount > 0 {
		watched.UncreditedExcessSats += excessAmount
	}

	// Uses the DEPOSITOR's public key (captured at registration time,
	// see core.BtcWatchedAddress's doc comment) - NOT tx.AuthPublicKey,
	// which on this transaction type is the REPORTING NODE's own key
	// (see verifyTxAuth's TxBridgeBtcDeposit case), not the wallet's.
	pub, err := hex.DecodeString(watched.CreditChainPublicKey)
	if err != nil {
		return
	}
	btcbAddress := core.DeriveTokenAddress(pub, core.BTCBAddressTag)
	bridgedBtcStore.Mint(btcbAddress, creditAmount)
	// LastCreditedSats always advances to the full observed balance -
	// same "baseline for the next delta, excess counted once" reasoning
	// as applyBridgeDeposit.
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
	if !cctStore.Transfer(tx.From, registrationFeeAddress(), core.FormatCctSmallest(core.CctWholeToSmallest(validatorRegisterFeeForCount(validatorCount)))) {
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
	// Re-checked here too, not just at submission (HandleDelegateToValidator) -
	// state may have changed between the two (e.g. the caller registered
	// as a moderator, or unstaked, after submitting but before this
	// confirms). See activeModeratorStakeShortfall's own doc comment.
	if activeModeratorStakeShortfall(tx.From, tx.Amount) > 0 {
		return
	}
	if !cctStore.Transfer(tx.From, validatorDelegationEscrowAddress, core.FormatCctSmallest(core.CctWholeToSmallest(tx.Amount))) {
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
	maturesAt := tx.Timestamp + int64(governanceParamsStore.GetOrDefault("UnbondingPeriodSeconds", uint64(core.UnbondingPeriodSeconds)))
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
// stakeTokenStore returns wholeUnitStore (not the concrete *core.TokenStore
// it used to) so ETH_CCC1 - a *core.DecimalTokenStore under the hood, see
// ccc1DecimalWholeUnitAdapter's doc comment in api/token.go - can be staked
// through the exact same Transfer-based escrow logic every other symbol
// here already uses, with no separate code path. Every existing caller
// only ever called Transfer/BalanceOf on the result, both part of
// wholeUnitStore, so this widening is a pure type change, not a behavior
// change for CCT/ETHTEST/USDCTEST/ETHB.
func stakeTokenStore(symbol string) (wholeUnitStore, error) {
	if isMainnetBlockedSymbol(symbol) {
		return nil, errTestOnlySymbolBlockedOnMainnet
	}
	switch symbol {
	case "CCT":
		return tokenStore, nil
	case "ETHTEST":
		return ethTestStore, nil
	case "USDCTEST":
		return usdcTestStore, nil
	case "ETHB":
		return bridgedEthStore, nil
	case "ETH_CCC1":
		// Real vault-locked ETH - the mainnet-appropriate stakeable ETH
		// asset (ETHB is the testnet-only mirror-model equivalent, see
		// HandleStake's doc comment). Escrowed the same way as every
		// other symbol here (stakeEscrowAddress), so a staked ETH_CCC1
		// balance is just as real and just as locked as a staked ETHB
		// balance always was.
		return ethCcc1TransferAdapter, nil
	}
	return nil, fmt.Errorf("unsupported stake symbol %q", symbol)
}

// stakeEscrowAddress is where actually-staked balance sits while
// bonded - same synthetic-address convention poolAddress/
// p2pOfferEscrowAddress already use elsewhere in this file.
func stakeEscrowAddress(symbol string) string {
	return "STAKE:" + symbol
}

// collateralEscrowAddress is where a real-balance collateral symbol
// (currently only STETH_CCC1 - CCT/OETH collateral has no underlying
// real balance to escrow, see applyLockCollateral) sits while locked as
// loan collateral. Deliberately a separate prefix from
// stakeEscrowAddress: STETH_CCC1 is not a stakeable symbol today (see
// stakeTokenStore), but keeping the two escrow namespaces distinct
// avoids ever silently conflating "staked" and "locked as collateral"
// if that ever changes.
func collateralEscrowAddress(symbol string) string {
	return "COLLATERAL:" + symbol
}

func applyStake(tx core.Transaction) {
	symbol := stakeSymbolFromData(tx.Data)

	// ETHTEST/USDCTEST are retired as stakeable symbols - ETHB (real
	// bridged ETH) replaces ETHTEST for the "stake ETH, get an oETH
	// collateral receipt" mechanic, and USDCTEST is repurposed as a
	// co-payment asset alongside YEN-e rather than something staked.
	// Checked here (not just by omitting them from the frontend
	// dropdown, api/staking.go's stakeableSymbols) since tx.Data is
	// client-supplied and a stale/crafted request could still name the
	// old symbol - same "never trust submission-time state" discipline
	// every other re-checked condition in this file follows. Existing
	// legacy stakes already on the books are unaffected by this check
	// (it only blocks NEW stakes) - see
	// api/legacy_stake_migration.go for how those get redeemed out.
	if symbol == "ETHTEST" || symbol == "USDCTEST" {
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
	// those tokens are moved. CCT stakes move through cctStore (see
	// core/staking_sync.go's doc comment for why staking accounting
	// itself still stays whole-unit even though CCT's general ledger
	// is now big-int) - the escrow amount is a whole-CCT quantity
	// converted at the boundary via CctWholeToSmallest.
	if symbol == "CCT" {
		smallest := core.FormatCctSmallest(core.CctWholeToSmallest(tx.Amount))
		if !cctStore.Transfer(tx.From, stakeEscrowAddress("CCT"), smallest) {
			return
		}
	} else {
		store, err := stakeTokenStore(symbol)
		if err != nil {
			return
		}
		balanceAddr := resolveBalanceAddress(tx.From, tx.AuthPublicKey, symbol)
		if !store.Transfer(balanceAddr, stakeEscrowAddress(symbol), tx.Amount) {
			return
		}
	}

	stakingStore.StakeETH(tx.From, symbol, tx.Amount)
	stakingStore.SyncWallet(walletStore.Get(tx.From), tx.From)

	// No reward and no oETH receipt is minted here anymore - staking is
	// now a pure locking mechanism (mining/block rewards are what pay
	// validators, per PoSR; see core/consensus.go). Previously this
	// block minted a flat 10% CCT reward (core.RewardForStake) plus, for
	// ETHB specifically, a 1:1 oETH collateral receipt. Removing both is
	// a deliberate economic change: existing oETH balances and their use
	// as loan collateral are completely unaffected (nothing here touches
	// or reduces a balance that already exists), only NEW oETH minting
	// from future stake actions stops - see audit/KNOWN_GAPS.md for the
	// disclosed narrowing of new oETH supply this causes for the
	// collateral-backed borrow feature going forward.
}

type borrowData struct {
	Interest uint64 `json:"interest"`
}

// stethCcc1WeiNeededForYenValue is the inverse of collateralValueInYen's
// STETH_CCC1 case (via usdCentsToWei, the same inverse
// account_holdings_cap.go's own partial-credit logic already uses) -
// given a YEN-e amount to cover, returns how much real STETH_CCC1 (wei)
// is needed at the current EthUsdCentsPerEth rate. Unlike CCT/OETH -
// whose raw locked amount happens to already be a roughly
// loan-amount-comparable uint64 quantity - a raw STETH_CCC1 wei amount
// is off by up to 18 orders of magnitude from a YEN-e loan amount, so
// hasEnoughCollateral/reserveCollateralForLoan/peekCollateralSymbolForLoan
// below must convert through this rather than comparing raw magnitudes
// directly (comparing raw magnitudes would make the check nearly always
// true - a dust-sized wei amount vastly exceeds any realistic YEN-e
// loan amount as a bare integer). ok=false (no live price) must never
// be treated as "0 needed" - callers must fail closed exactly like
// collateralValueInYen's own ok=false convention.
// asOf is the same deterministic timestamp basis collateralValueInYen
// takes - see that function's doc comment.
func stethCcc1WeiNeededForYenValue(yenAmount uint64, asOf int64) (weiNeeded uint64, ok bool) {
	if governanceParamsStore.IsStale("EthUsdCentsPerEth", asOf, EthUsdCentsPerEthMaxAgeSeconds) {
		return 0, false
	}
	centsPerEth := governanceParamsStore.GetOrDefault("EthUsdCentsPerEth", 0)
	if centsPerEth == 0 {
		return 0, false
	}
	ratioBps := governanceParamsStore.GetOrDefault("StethEthRatioBasisPoints", DefaultStethEthRatioBasisPoints)
	if ratioBps == 0 {
		// stETH declared worthless (an admin-set 0% ratio, e.g. total
		// depeg) - no finite amount of it can ever back a nonzero loan.
		// Fail closed rather than divide-by-zero.
		return 0, false
	}
	// Inverse of collateralValueInYen's STETH_CCC1 case: need enough
	// wei that, after the same ratioBps haircut, is worth yenAmount -
	// without this, a reservation sized during a depeg would silently
	// under-collateralize by exactly the haircut percentage.
	adjustedCents := new(big.Int).SetUint64(yenAmount)
	adjustedCents.Mul(adjustedCents, big.NewInt(10_000))
	adjustedCents.Div(adjustedCents, new(big.Int).SetUint64(ratioBps))
	return usdCentsToWei(adjustedCents, centsPerEth), true
}

// hasEnoughCollateral is the non-mutating fast-fail check HTTP
// handlers use before ever recording a transaction - true if amount of
// ANY ONE locked symbol (CCT, oETH, or STETH_CCC1, the last priced via
// stethCcc1WeiNeededForYenValue - see its doc comment) alone would
// cover it (mirroring exactly what reserveCollateralForLoan enforces
// for real at confirmation time, just without actually reserving
// anything yet).
func hasEnoughCollateral(address string, amount uint64, asOf int64) bool {
	if collateralStore.LockedOf(address, "CCT") >= amount || collateralStore.LockedOf(address, "OETH") >= amount {
		return true
	}
	weiNeeded, ok := stethCcc1WeiNeededForYenValue(amount, asOf)
	return ok && collateralStore.LockedOf(address, "STETH_CCC1") >= weiNeeded
}

// reserveCollateralForLoan earmarks exactly enough of the borrower's
// locked collateral to back one specific loan - preferring CCT, then
// OETH, then STETH_CCC1 (priced via stethCcc1WeiNeededForYenValue), but
// always a single symbol per loan (never a mix), so releaseLoanCollateral
// can later return the exact same amount to the exact same source. This
// replaces the old hasCollateral, which only checked "is anything at
// all locked or staked" without ever relating the amount to the loan
// size - the gap that let repayment release the wrong amount (or
// nothing at all) for a loan that was never actually 1:1 backed by
// anything specific. The reservation itself happens here (removed from
// collateralStore's pool immediately), not just checked - so the same
// locked collateral can't be reserved twice for two different loans.
// STETH_CCC1 is tried last since it's the newest/least battle-tested
// collateral path - this ordering has no other significance (a single
// loan is always backed by exactly one symbol regardless of order).
//
// reservedAmount is the amount ACTUALLY reserved - equal to `amount`
// for CCT/OETH (unchanged, 1:1 as always), but a DIFFERENT, much
// smaller wei figure for STETH_CCC1 (priced via
// stethCcc1WeiNeededForYenValue - see that function's doc comment on
// the magnitude mismatch). Every caller MUST use reservedAmount, never
// the original `amount`, for both the resulting loan's CollateralAmount
// field and any refund-on-later-failure path - using `amount` instead
// was a real bug caught in review: it would either under-refund the
// reservation on a failed draw (leaking real STETH_CCC1 into
// collateralStore.Locked with no loan or refund tracking it) or, worse,
// record a wildly wrong CollateralAmount on the loan itself, permanently
// stranding the real difference in the collateral escrow with nothing
// ever able to release it.
func reserveCollateralForLoan(address string, amount uint64, asOf int64) (symbol string, reservedAmount uint64, ok bool) {
	if collateralStore.Release(address, "CCT", amount) {
		return "CCT", amount, true
	}
	if collateralStore.Release(address, "OETH", amount) {
		return "OETH", amount, true
	}
	if weiNeeded, ok := stethCcc1WeiNeededForYenValue(amount, asOf); ok && weiNeeded > 0 && collateralStore.Release(address, "STETH_CCC1", weiNeeded) {
		return "STETH_CCC1", weiNeeded, true
	}
	return "", 0, false
}

// peekCollateralSymbolForLoan is reserveCollateralForLoan's non-
// mutating twin - same CCT-then-OETH-then-STETH_CCC1 preference (and
// the same stethCcc1WeiNeededForYenValue pricing), just a read instead
// of an actual reservation. Used at request time (HandleBorrow) to
// price a loan's interest rate off the collateral it's about to be
// backed by, before the real reservation happens later at block-
// confirmation time (applyBorrow).
func peekCollateralSymbolForLoan(address string, amount uint64, asOf int64) (symbol string, ok bool) {
	if collateralStore.LockedOf(address, "CCT") >= amount {
		return "CCT", true
	}
	if collateralStore.LockedOf(address, "OETH") >= amount {
		return "OETH", true
	}
	if weiNeeded, ok := stethCcc1WeiNeededForYenValue(amount, asOf); ok && weiNeeded > 0 && collateralStore.LockedOf(address, "STETH_CCC1") >= weiNeeded {
		return "STETH_CCC1", true
	}
	return "", false
}

// EthUsdCentsPerEthMaxAgeSeconds bounds how long a governance-set
// EthUsdCentsPerEth value may go without being re-confirmed (via
// TxAdminSetParam or a passed PARAM_CHANGE proposal - see
// core.GovernanceParamsStore.SetAt) before collateralValueInYen and
// stethCcc1WeiNeededForYenValue refuse to price STETH_CCC1 collateral
// at all. 24 hours - generous enough that routine admin cadence
// doesn't trip it, but short enough that a rate nobody has touched in
// days can never silently keep backing real liquidation decisions.
// Fails CLOSED (ok=false, same as "never set") once exceeded - never
// falls back to the last-known value.
const EthUsdCentsPerEthMaxAgeSeconds int64 = 24 * 3600

// DefaultStethEthRatioBasisPoints is the assumed stETH:ETH exchange
// ratio (10000 = 1:1) collateralValueInYen applies as a haircut on top
// of EthUsdCentsPerEth's raw ETH/USD rate - stETH is NOT always worth
// exactly 1 ETH (it has genuinely depegged in real markets, e.g. to
// ~0.94 ETH during the 2022 stress event), so pricing it as a bare 1:1
// proxy for ETH would silently overvalue collateral during exactly the
// kind of stress event where accurate liquidation math matters most.
// Admin-settable (see api/admin_params.go's allowlist) so this can be
// tightened during an observed depeg without a code change/redeploy -
// same "admin-tunable approximation, not a live oracle" precedent
// EthUsdCentsPerEth/BtcUsdCentsPerBtc already established.
const DefaultStethEthRatioBasisPoints uint64 = 10_000

// collateralValueInYen prices collateral in YEN-e-equivalent terms for
// the liquidation engine. Two independent pricing paths exist:
//
//   - OETH reuses applySwapTokenToToken's exact two-hop pattern as a
//     pure quote (no state mutation): OETH -> CCT via cctEthTestPool,
//     then that CCT amount -> USDCTEST via cctUsdcTestPool, treating
//     the USDCTEST-equivalent result as YEN-e's value too (YEN-e has
//     no market of its own - see the approved Phase B design, which
//     deliberately proxies through USDCTEST's existing pool rather
//     than seeding a new one).
//   - STETH_CCC1 (real, mainnet-appropriate collateral - unlike OETH's
//     testnet-only AMM pools) prices off the admin-governed
//     EthUsdCentsPerEth rate (see api/admin_params.go's allowlist and
//     api/account_holdings_cap.go's weiToUsdCents, the same real-USD
//     conversion api/ccc1_transfer_fee.go already uses for ETH_CCC1
//     fees), haircut by StethEthRatioBasisPoints to account for stETH
//     not always trading exactly 1:1 with ETH (see
//     DefaultStethEthRatioBasisPoints's doc comment). The resulting
//     USD-cents figure is then treated AS the YEN-e value directly -
//     an explicit, disclosed design decision (1 YEN-e unit == 1 USD
//     cent) rather than routing through the real CCT<->YENE pool's
//     live rate, since YEN-e's liquidation math elsewhere already
//     assumes a stable, manipulation-resistant unit price the way
//     OETH's own two-hop AMM quote does not fully guarantee. Also
//     fails closed if EthUsdCentsPerEth hasn't been re-confirmed
//     within EthUsdCentsPerEthMaxAgeSeconds - see that constant's doc
//     comment.
//
// asOf must be a deterministic timestamp basis (a transaction/block
// timestamp in consensus-critical Apply* code, time.Now().Unix() in a
// pure read-only HTTP pre-check) - see
// core.GovernanceParamsStore.IsStale's doc comment.
//
// ok=false means "no live price available - fall back to due-date-only
// handling", NEVER "worth zero" or "worth infinite" - callers must
// treat it as "can't assess", not a value. CCT collateral always
// returns ok=false: CCT is this system's pricing numeraire, nothing
// else prices CCT against a single arbitrary pool, so CCT-collateralized
// loans stay due-date-only in this phase, exactly matching today's
// behavior.
func collateralValueInYen(symbol string, amount uint64, asOf int64) (valueInYen uint64, ok bool) {
	if amount == 0 {
		return 0, false
	}

	switch symbol {
	case "OETH":
		quotedCCT, ok := cctEthTestPool.QuoteTokenForCCT(amount)
		if !ok {
			return 0, false
		}
		quotedYen, ok := cctUsdcTestPool.QuoteCCTForToken(quotedCCT)
		if !ok {
			return 0, false
		}
		return quotedYen, true

	case "STETH_CCC1":
		if governanceParamsStore.IsStale("EthUsdCentsPerEth", asOf, EthUsdCentsPerEthMaxAgeSeconds) {
			return 0, false
		}
		centsPerEth := governanceParamsStore.GetOrDefault("EthUsdCentsPerEth", 0)
		if centsPerEth == 0 {
			return 0, false
		}
		ratioBps := governanceParamsStore.GetOrDefault("StethEthRatioBasisPoints", DefaultStethEthRatioBasisPoints)
		usdCents := weiToUsdCents(amount, centsPerEth)
		usdCents.Mul(usdCents, new(big.Int).SetUint64(ratioBps))
		usdCents.Div(usdCents, big.NewInt(10_000))
		if !usdCents.IsUint64() {
			return ^uint64(0), true
		}
		return usdCents.Uint64(), true

	default:
		return 0, false
	}
}

// releaseLoanCollateral returns a repaid loan's earmarked collateral
// to the exact staked/oETH balance it was reserved from -
// collateralStore is deliberately NOT credited back here, since this
// collateral is fully released to the user, not returned to the
// general locked-but-unallocated pool.
//
// Also lazily re-checks the borrower's legacy ETHTEST/USDCTEST stake
// redemption (see api/legacy_stake_migration.go) - releasing oETH
// collateral is exactly the event that can turn a previously-locked
// legacy stake position free, and the one-time migration sweep has no
// other way to revisit an address after its own pass completes.
func releaseLoanCollateral(loan *core.Loan, timestamp int64) {
	creditCollateralTo(loan.Borrower, loan.CollateralSymbol, loan.CollateralAmount)
	if loan.CollateralSymbol == "OETH" {
		redeemFreeLegacyStakeFor(loan.Borrower, timestamp)
	}
}

// creditCollateralTo moves a collateral amount into recipient's real
// staked-CCT/oETH balance, or real STETH_CCC1 vault balance - the same
// destination releaseLoanCollateral and (formerly)
// forfeitLoanCollateralToLender each credited into, just factored out
// so liquidateLoan can split a single loan's collateral between two
// recipients (partial seize + partial refund) without duplicating the
// switch three times over.
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

	case "STETH_CCC1":
		// Unlike CCT/OETH, this is a real balance that must actually
		// move out of the shared collateral escrow - see
		// applyLockCollateral. No pubkey is available in this
		// function's signature (shared with the CCT/OETH cases above,
		// neither of which ever needed one), so resolveBalanceAddress
		// falls back to its empty-pubkey derivation if recipient has
		// never registered a self-custodial ETH address. That's a
		// no-op risk for the borrower side (releaseLoanCollateral) -
		// registration is a prerequisite for ever holding STETH_CCC1
		// to lock in the first place - but is a real, disclosed edge
		// case for the lender side (liquidateLoan's seize path), if the
		// lender has never registered one - see audit/KNOWN_GAPS.md.
		dest := resolveBalanceAddress(recipient, "", "STETH_CCC1")
		stethCcc1TransferAdapter.Transfer(collateralEscrowAddress("STETH_CCC1"), dest, amount)
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
	collateralValue, ok := collateralValueInYen(loan.CollateralSymbol, loan.CollateralAmount, asOf)
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
		// The refunded portion (if OETH) just freed up - same lazy
		// legacy-stake re-check as releaseLoanCollateral, since this is
		// another real "collateral just came back to the borrower"
		// event the one-time migration sweep can't otherwise revisit.
		if loan.CollateralSymbol == "OETH" && refund > 0 {
			redeemFreeLegacyStakeFor(loan.Borrower, asOf)
		}
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

// processCollateralLiquidations sweeps every peer-funded loan
// collateralized by a symbol collateralValueInYen can actually price
// (OETH, and now STETH_CCC1 - see that function's doc comment) - not
// just ones currently failing the threshold, unlike before this
// feature: a loan whose collateral value recovers needs to be
// re-visited so advanceLoanLifecycle can step it back down to Healthy,
// which requires evaluating atRisk fresh on every pass, on every such
// loan, every block. Same block-timestamp-driven determinism as
// processLoanDefaults (sorted iteration - Go map order is randomized,
// every node must reach the identical result). CCT-collateralized
// loans are never scanned here - collateralValueInYen always returns
// ok=false for CCT, so they'd never trigger anyway, but skipping them
// explicitly documents that this phase's proactive liquidation only
// covers priceable collateral by design (see the approved Phase B
// plan). STETH_CCC1's inclusion matters more than OETH's ever did:
// unlike OETH's synthetic AMM-pool price, STETH_CCC1 tracks a REAL
// asset's real price (via EthUsdCentsPerEth) - a real stETH price drop
// with no proactive sweep would leave a lender under-collateralized
// with protection only at the loan's due date, not before.
func processCollateralLiquidations(asOf int64) {
	var priceableLoanIDs []string
	for id, loan := range loanStore.Loans {
		if loan.Status == core.LoanFunded && loan.Lender != "" &&
			(loan.CollateralSymbol == "OETH" || loan.CollateralSymbol == "STETH_CCC1") {
			priceableLoanIDs = append(priceableLoanIDs, id)
		}
	}
	if len(priceableLoanIDs) == 0 {
		return
	}
	sort.Strings(priceableLoanIDs)

	for _, id := range priceableLoanIDs {
		loan := loanStore.Loans[id]

		collateralValue, ok := collateralValueInYen(loan.CollateralSymbol, loan.CollateralAmount, asOf)
		if !ok {
			continue // no live price - can't judge risk, leave this loan's stage untouched
		}
		owed := repayAmountFor(loan)
		if owed == 0 {
			continue
		}

		// collateralValue/owed < threshold/100, rearranged to avoid
		// floating point: collateralValue*100 < threshold*owed.
		// threshold is per-symbol (see core.LiquidationThresholdPercentFor -
		// STETH_CCC1 uses a stricter margin than OETH).
		threshold := core.LiquidationThresholdPercentFor(loan.CollateralSymbol)
		atRisk := collateralValue*100 < threshold*owed
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

		delta := creditEngine.DefaultLoan(loan, shortfall, owed, asOf >= TrustDimensionsCutoverUnixSeconds)
		loan.RiskStage = core.LoanRiskDefaulted
		loan.RiskStageDeadline = 0

		if delta > 0 {
			if reward := increaseTrust(walletStore.Get(loan.Lender), core.DimensionContribution, delta, asOf); reward > 0 {
				mintRept(loan.Lender, reward)
			}

			reputationEventStore.Record(&core.ReputationEvent{
				TxID: "final-redemption-default-" + loan.ID, Target: loan.Borrower, Delta: -int64(delta),
				Reason: "Defaulted on a peer-funded loan after the final redemption window expired", Timestamp: asOf, Dimension: core.DimensionRepayment.String(),
			})
			reputationEventStore.Record(&core.ReputationEvent{
				TxID: "final-redemption-default-" + loan.ID + "-lender", Target: loan.Lender, Delta: int64(delta),
				Reason: "Received a defaulting borrower's collateral and Mining Power", Timestamp: asOf, Dimension: core.DimensionContribution.String(),
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
	collateralSymbol, collateralReserved, ok := reserveCollateralForLoan(tx.To, tx.Amount, tx.Timestamp)
	if !ok {
		return
	}

	var data borrowData
	_ = json.Unmarshal([]byte(tx.Data), &data)

	id := "loan-" + tx.ID[:min(8, len(tx.ID))]

	loanStore.CreateLoan(id, tx.To, tx.Amount, data.Interest, collateralSymbol, collateralReserved, tx.Timestamp)

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
	if !kycStore.IsApproved(tx.From, core.TrustDimensionValue(walletStore.Get(tx.From), core.DimensionRepayment)) {
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
// lenderCanReceiveStethCcc1 reports whether address has a registered
// self-custodial ETH address (or a legacy custodial account) already
// on file - a prerequisite for creditCollateralTo's seizure path to
// ever land real STETH_CCC1 at an address the recipient can actually
// find/spend from, see that function's doc comment and
// audit/KNOWN_GAPS.md's "STETH_CCC1 as loan collateral" entry for the
// full failure mode this closes. A borrower reclaiming their own
// collateral never needs this check (holding STETH_CCC1 to lock in the
// first place already implies registration); only used at the moment a
// STETH_CCC1-collateralized loan is about to gain a real LENDER who
// might later need to receive seized collateral.
func lenderCanReceiveStethCcc1(address string) bool {
	if _, ok := ethAddressStore.Get(address); ok {
		return true
	}
	_, ok := ethCustodialStore.Get(address)
	return ok
}

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
	if data.Amount > core.BorrowLimit(core.TrustDimensionValue(wallet, core.DimensionRepayment)) {
		return
	}

	// Reserved first - if either step below fails, it's refunded back
	// to the pool (same refund-on-later-failure convention every other
	// two-step mutation in this codebase already follows), since
	// there's no single atomic operation covering both this and the
	// offer draw. collateralReserved (NOT data.Amount) is what every
	// refund below must return and what the final loan record's
	// CollateralAmount must be set to - see reserveCollateralForLoan's
	// doc comment on why using data.Amount instead was a real bug for
	// STETH_CCC1 (where the reserved wei amount is a completely
	// different number from the loan amount).
	collateralSymbol, collateralReserved, ok := reserveCollateralForLoan(tx.From, data.Amount, tx.Timestamp)
	if !ok {
		return
	}

	// STETH_CCC1 collateral can end up seized to offer.Lender on
	// default/liquidation (see creditCollateralTo) - that path needs a
	// registered self-custodial ETH address to land anywhere reachable,
	// so refuse to fund rather than create a loan whose forfeiture path
	// is a known dead end. Refund the reservation first, same
	// refund-on-later-failure convention every other step below uses.
	if collateralSymbol == "STETH_CCC1" && !lenderCanReceiveStethCcc1(offer.Lender) {
		collateralStore.Add(tx.From, collateralSymbol, collateralReserved)
		return
	}

	store, err := resolveTokenStore(offer.Symbol)
	if err != nil {
		collateralStore.Add(tx.From, collateralSymbol, collateralReserved)
		return
	}

	if !loanOfferStore.Draw(offer.ID, data.Amount) {
		collateralStore.Add(tx.From, collateralSymbol, collateralReserved)
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
		CollateralAmount: collateralReserved,
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
	if reward := increaseTrust(walletStore.Get(offer.Lender), core.DimensionContribution, core.ReputationDeltaLoanFunded, tx.Timestamp); reward > 0 {
		mintRept(offer.Lender, reward)
	}
	// Target is the LENDER, not tx.From (the borrower drawing this
	// offer) - see core.ReputationEvent's doc comment for why this
	// needs its own explicit target rather than assuming tx.From.
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: offer.Lender, Delta: int64(core.ReputationDeltaLoanFunded),
		Reason: "Funded a loan (peer-funded draw)", Timestamp: tx.Timestamp, Dimension: core.DimensionContribution.String(),
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

// meetsMinimumCollateralRatio enforces "collateral amount is at least
// the borrow amount" using whichever comparison is actually meaningful
// for the given symbol. CCT/OETH stay a raw magnitude comparison - both
// are already roughly YEN-e-comparable-unit quantities in this system,
// the long-standing behavior this preserves exactly. STETH_CCC1 is
// wei-scale (18 decimals) though - a raw magnitude comparison against a
// whole-unit YEN-e loan amount would be meaningless (trivially true for
// almost any nonzero wei amount, since even a dust-sized stETH amount
// is a huge integer once expressed in wei), so it's priced through
// collateralValueInYen instead, same as the real liquidation math uses.
// No live price yet (EthUsdCentsPerEth unset) fails closed - rejected,
// never silently waved through.
func meetsMinimumCollateralRatio(symbol string, collateralAmount, loanAmount uint64, asOf int64) bool {
	if symbol == "STETH_CCC1" {
		valueInYen, ok := collateralValueInYen(symbol, collateralAmount, asOf)
		return ok && valueInYen >= loanAmount
	}
	return collateralAmount >= loanAmount
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
	if data.Amount == 0 || !meetsMinimumCollateralRatio(data.CollateralSymbol, data.CollateralAmount, data.Amount, tx.Timestamp) || data.MaxInterestPercent == 0 {
		return
	}
	if data.CollateralSymbol != "CCT" && data.CollateralSymbol != "OETH" && data.CollateralSymbol != "STETH_CCC1" {
		return
	}
	if len(data.Purpose) > core.MaxLoanRequestPurposeLen {
		return
	}

	wallet := walletStore.Get(tx.From)
	if data.Amount > core.BorrowLimit(core.TrustDimensionValue(wallet, core.DimensionRepayment)) {
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

	// Same registration gate applyLoanOfferDraw enforces - see
	// lenderCanReceiveStethCcc1's doc comment. Checked before any real
	// token transfer happens here, so there's nothing to unwind on
	// rejection (unlike applyLoanOfferDraw, which reserves collateral
	// first and must refund it).
	if request.CollateralSymbol == "STETH_CCC1" && !lenderCanReceiveStethCcc1(tx.From) {
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
	if reward := increaseTrust(walletStore.Get(tx.From), core.DimensionContribution, core.ReputationDeltaLoanFunded, tx.Timestamp); reward > 0 {
		mintRept(tx.From, reward)
	}
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: tx.From, Delta: int64(core.ReputationDeltaLoanFunded),
		Reason: "Funded a loan (borrower request)", Timestamp: tx.Timestamp, Dimension: core.DimensionContribution.String(),
	})
}

func applyModeratorRegister(tx core.Transaction) {
	// The stake requirement may have changed (governance-tunable, see
	// api/moderators.go's moderatorStakeRequirementCCT) or the wallet's
	// own stake may have moved between submission and confirmation -
	// re-checked here, before the fee is even considered, same
	// "check requirements before charging anything" ordering as
	// applyCompanyApply's stake-then-fee fix.
	if availableStakedCct(tx.From) < moderatorStakeRequirementCCT() {
		return
	}

	// The fee may have moved between submission and confirmation -
	// re-checked and only actually deducted here, never trusted from
	// submission time alone (same pattern as applyTokenRegister's
	// ETHTEST creation fee).
	if !cctStore.Transfer(tx.From, registrationFeeAddress(), core.FormatCctSmallest(core.CctWholeToSmallest(moderatorRegisterFeeCCT()))) {
		return
	}

	if !moderatorStore.Register(tx.From, tx.AuthPublicKey, tx.Timestamp) {
		return
	}

	// Lock the required bond out of the moderator's own already-staked
	// CCT into the MODERATION purpose (see core/stake_purpose_store.go)
	// - up to whatever's actually still free, same "lock what's
	// available, don't fail the whole registration over a locking
	// nuance" precedent as applyCompanyReview's hub stake vault.
	required := moderatorStakeRequirementCCT()
	alreadyLocked := stakePurposeStore.TotalLocked(tx.From)
	totalStaked := stakingStore.StakeOf(tx.From, "CCT")
	available := uint64(0)
	if totalStaked > alreadyLocked {
		available = totalStaked - alreadyLocked
	}
	lockAmount := required
	if lockAmount > available {
		lockAmount = available
	}
	if lockAmount > 0 {
		stakePurposeStore.Lock(tx.From, core.StakePurposeModeration, lockAmount)
	}

	wallet := walletStore.Get(tx.From)
	if reward := increaseTrust(wallet, core.DimensionContribution, core.ModeratorReputationBonus, tx.Timestamp); reward > 0 {
		mintRept(tx.From, reward)
	}
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: tx.From, Delta: int64(core.ModeratorReputationBonus),
		Reason: "Registered as a moderator (one-time)", Timestamp: tx.Timestamp, Dimension: core.DimensionContribution.String(),
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
		if reward := increaseTrust(wallet, core.DimensionContribution, core.ModeratorConfirmReputationBonus, tx.Timestamp); reward > 0 {
			mintRept(tx.From, reward)
		}
		reputationEventStore.Record(&core.ReputationEvent{
			TxID: tx.ID, Target: tx.From, Delta: int64(core.ModeratorConfirmReputationBonus),
			Reason: "Confirmed an undisputed trade as moderator", Timestamp: tx.Timestamp, Dimension: core.DimensionContribution.String(),
		})
	}
}

// applyTradeReview re-validates everything HandleSubmitTradeReview
// already checked at submission time (never trusted from there alone -
// the trade's own settlement status, in particular, could have changed
// between the two, e.g. a dispute opened after this was submitted but
// before it confirmed), then records the review and - only for a
// genuinely positive review, and only the first time this specific
// reviewer has ever earned this specific reviewee a bonus - grants the
// reviewee reputation. See core/trade_review.go's doc comment for why
// that pair-cap exists.
func applyTradeReview(tx core.Transaction) {
	var data tradeReviewData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.Stars < 1 || data.Stars > 5 {
		return
	}

	o, ok := p2pOfferStore.Get(data.TradeID)
	if !ok || !tradeReviewEligibleStatuses[o.SettlementStatus] {
		return
	}
	reviewee, isParticipant := counterpartyFor(o, tx.From)
	if !isParticipant {
		return
	}

	if !tradeReviewStore.Submit(&core.TradeReview{
		TradeID: data.TradeID, Reviewer: tx.From, Reviewee: reviewee,
		Stars: data.Stars, Comment: data.Comment, CreatedAt: tx.Timestamp,
	}) {
		return
	}

	if data.Stars < TradeReviewMinStarsForBonus {
		return
	}
	if tradeReviewStore.PairAlreadyRewarded(tx.From, reviewee) {
		return
	}
	tradeReviewStore.MarkRewarded(tx.From, reviewee)

	wallet := walletStore.Get(reviewee)
	if reward := increaseTrust(wallet, core.DimensionTrading, TradeReviewReputationBonus, tx.Timestamp); reward > 0 {
		mintRept(reviewee, reward)
	}
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: reviewee, Delta: int64(TradeReviewReputationBonus),
		Reason: "Received a positive trade review", Timestamp: tx.Timestamp, Dimension: core.DimensionTrading.String(),
	})
}

// VerificationTrustDeltaKYCSubmit is the first (and so far only) real
// event feeding Verification Trust (see trust.go) - a one-time bonus
// the FIRST time a wallet genuinely self-attests KYC. Deliberately
// modest and explicitly labeled "self-attested" throughout this
// codebase (see core.KYCRecord's own doc comment) - this is not real
// identity-document verification, so the bonus reflects "took the
// self-attestation step," not "identity confirmed."
const VerificationTrustDeltaKYCSubmit = 5

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

	// Checked BEFORE Submit (which replaces any existing record) - the
	// bonus is one-time-only, a resubmission (e.g. correcting a typo'd
	// name) must never re-grant it.
	_, alreadySubmitted := kycStore.Get(tx.From)

	kycStore.Submit(tx.From, data.FullName, data.DateOfBirth, data.Country, data.AgreedTerms, tx.Timestamp)

	if alreadySubmitted {
		return
	}
	wallet := walletStore.Get(tx.From)
	if reward := increaseTrust(wallet, core.DimensionVerification, VerificationTrustDeltaKYCSubmit, tx.Timestamp); reward > 0 {
		mintRept(tx.From, reward)
	}
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: tx.From, Delta: int64(VerificationTrustDeltaKYCSubmit),
		Reason: "Submitted KYC self-attestation (one-time)", Timestamp: tx.Timestamp, Dimension: core.DimensionVerification.String(),
	})
}

func applyCompanyApply(tx core.Transaction) {
	var data companyApplyData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if len(data.Name) == 0 || len(data.Name) > 200 {
		return
	}
	// Re-checked here too, not just at submission (HandleApplyCompany) -
	// never trusted from submission time alone, this file's usual
	// discipline. Country not on core.AllowedHubCountries (which has no
	// United States entry at all - see that list's own doc comment)
	// means this application can never be approved (core.CompanyStore.
	// Review enforces that too), so it's rejected outright here rather
	// than created and left permanently unapprovable.
	if !core.IsAllowedHubCountry(data.Country) {
		return
	}

	// The fee may have moved between submission and confirmation -
	// re-checked and only actually deducted here, never trusted from
	// submission time alone (same pattern as applyTokenRegister's
	// ETHTEST creation fee).
	// Checked before the fee is actually deducted below - re-verified
	// here (not just at submission) since the applicant's real staked
	// CCT may have changed between submission and confirmation, and
	// checking it first avoids charging the fee for an application
	// that's about to be rejected anyway.
	if stakingStore.StakeOf(tx.From, "CCT") < HubMinimumStakeCCT {
		return
	}

	// Assigned here (block-confirmation time), not in the HTTP handler,
	// for the same determinism reasoning as every other moderator
	// assignment (see core.ModeratorStore.AssignNext) - also needed a
	// step earlier than before now, since the fee split below pays this
	// moderator directly.
	assignedModerator := moderatorStore.AssignNext()

	// HubApplyFeeCCT (25 CCT) is now the ONLY fee for the whole hub
	// registration flow, request through approval - this session's fee
	// review replaced the old two-fee model (this flat cost, PLUS a
	// separate 500 CCT fee charged later in applyCompanyReview) with
	// one consolidated cost, split 40% Foundation Treasury (admin) /
	// 40% the assigned moderator / 20% into the CCT validator pool
	// (cctTransferFeePoolAddress, swept to whichever validator produces
	// the next block - see applyBlockReward). The moderator is paid
	// here regardless of how they eventually decide, same as before
	// (compensation for doing the review, not a reward for a specific
	// outcome).
	adminShare := uint64(HubApplyFeeCCT) * 40 / 100
	moderatorShare := uint64(HubApplyFeeCCT) * 40 / 100
	validatorShare := uint64(HubApplyFeeCCT) - adminShare - moderatorShare // remainder covers rounding, currently exactly 20%

	// Checked as one whole-fee amount BEFORE any of the three legs
	// below moves anything - there's no atomic multi-transfer primitive
	// in this codebase (same disclosed limitation as e.g.
	// settleHubTradeOnChain's own refund-on-second-leg-failure
	// handling), so without this upfront check, an applicant who can
	// afford SOME but not all three legs could end up having paid a
	// partial fee with no application ever created to show for it.
	if cctAdapter.BalanceOf(tx.From) < HubApplyFeeCCT {
		return
	}
	if !transferCctWhole(tx.From, treasuryStore.Treasury.FoundationAddress, adminShare) {
		return
	}
	if !transferCctWhole(tx.From, assignedModerator, moderatorShare) {
		return
	}
	if !transferCctWhole(tx.From, cctTransferFeePoolAddress(), validatorShare) {
		return
	}

	companyStore.Apply(&core.CompanyApplication{
		Address:          tx.From,
		PublicKey:        tx.AuthPublicKey,
		Name:             data.Name,
		Description:      data.Description,
		DocumentNote:     data.DocumentNote,
		ContactEmail:     data.ContactEmail,
		Country:          data.Country,
		Status:           core.CompanyPending,
		ModeratorAddress: assignedModerator,
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

// applyWalletConnectHub is the authoritative check behind
// HandleConnectHub - re-verifies the target is still a verified hub at
// confirmation time (never trusting the HTTP-layer check alone, this
// file's usual discipline), then sets the signer's own
// core.Wallet.ConnectedHub. tx.AuthAddress == tx.From is already
// enforced by verifyTxAuth before this ever runs, so tx.From is
// genuinely the wallet consenting to connect itself - nobody else's.
func applyWalletConnectHub(tx core.Transaction) {
	var data walletConnectHubData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.Hub == "" || data.Hub == tx.From || !companyStore.IsVerified(data.Hub) {
		return
	}

	walletStore.Get(tx.From).ConnectedHub = data.Hub
}

// applyCompanyUploadCertificate re-decodes/re-validates the certificate
// exactly like HandleUploadCertificate's fast-fail did (never trusting
// that pass alone), writes the real file to data/certificates/, and
// records its path + SHA-256 hash against the still-PENDING
// application. The stored filename is server-generated
// (address-hash.ext), never derived from the caller-supplied FileName
// beyond its extension, so nothing here is a path-traversal risk.
func applyCompanyUploadCertificate(tx core.Transaction) {
	var data companyUploadCertificateData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	app, ok := companyStore.Get(tx.From)
	if !ok || app.Status != core.CompanyPending {
		return
	}

	raw, ext, err := decodeAndValidateCertificate(data.FileName, data.FileBase64)
	if err != nil {
		return
	}

	// The hash is computed over the real plaintext document (what a
	// moderator actually reviewed), never the ciphertext below - so it
	// stays meaningful as an audit fingerprint regardless of how the
	// file happens to be stored at rest.
	hash := sha256.Sum256(raw)
	hashHex := hex.EncodeToString(hash[:])

	// A hub certificate is a real, sensitive uploaded document - same
	// AES-256-GCM-at-rest treatment as api/hub_kyc.go's own records
	// (same trust model: defends against the file leaking if the
	// underlying disk/backup is exposed, not end-to-end encryption the
	// node operator is locked out of). Fails closed - never falls back
	// to writing plaintext - if HUB_KYC_ENCRYPTION_KEY_HEX isn't
	// configured on this node (already checked at the HTTP fast-fail in
	// HandleUploadCertificate, re-checked here since that's what
	// actually matters for consensus-correctness).
	encrypted, err := encryptHubKYC(raw)
	if err != nil {
		return
	}

	certDir := dataPath("certificates")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return
	}
	fileName := fmt.Sprintf("%s-%s%s.enc", tx.From, hashHex, ext)
	filePath := filepath.Join(certDir, fileName)
	if err := os.WriteFile(filePath, encrypted, 0o600); err != nil {
		return
	}

	companyStore.SetCertificate(tx.From, filePath, hashHex, tx.Timestamp)
}

func applyCompanyReview(tx core.Transaction) {
	var data companyReviewData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	app, ok := companyStore.Get(data.Address)
	if !ok || app.ModeratorAddress != tx.From || app.Status != core.CompanyPending {
		return
	}

	// Approval requires a certificate already on file (re-checked here
	// too - core.CompanyStore.Review also enforces this, but the fee/
	// stake-lock side effects below only make sense once we know
	// approval will actually succeed).
	if data.Approve && app.CertificateHash == "" {
		return
	}

	if !companyStore.Review(data.Address, tx.From, data.Approve, tx.Timestamp) {
		return
	}

	// Append-only audit ledger of this moderator's decision, readable
	// only by a genuine top-executive (see api/moderator_verification_log.go)
	// - written for both approve and reject outcomes, since the ledger's
	// purpose is to let an executive review a moderator's full decision
	// history, not just their approvals.
	moderatorVerificationLog.Append(&core.ModeratorVerificationEntry{
		ID:               tx.ID,
		ModeratorAddress: tx.From,
		CompanyAddress:   data.Address,
		CertificateHash:  app.CertificateHash,
		Approved:         data.Approve,
		Timestamp:        tx.Timestamp,
	})

	if !data.Approve {
		return
	}

	// No separate approval-time fee anymore - this session's fee review
	// consolidated hub registration down to the single HubApplyFeeCCT
	// (25 CCT) charged at application time (see applyCompanyApply),
	// which already pays the moderator, admin, and validator pool up
	// front regardless of the eventual decision.

	// Lock up to HubStakeVaultMaxCCT of the hub's own already-staked
	// CCT into its dedicated stake vault (core.StakePurposeHub) - a
	// real reservation out of stakingStore's own total, not a new
	// deposit. A hub with less staked than the max locks whatever it
	// actually has; TotalLocked already accounts for anything this
	// address has locked for other purposes (moderation, validator
	// pool) so this can never double-lock CCT that isn't really free.
	totalStaked := stakingStore.StakeOf(data.Address, "CCT")
	alreadyLocked := stakePurposeStore.TotalLocked(data.Address)
	available := uint64(0)
	if totalStaked > alreadyLocked {
		available = totalStaked - alreadyLocked
	}
	lockAmount := available
	if lockAmount > HubStakeVaultMaxCCT {
		lockAmount = HubStakeVaultMaxCCT
	}
	if lockAmount > 0 {
		stakePurposeStore.Lock(data.Address, core.StakePurposeHub, lockAmount)
	}
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
	} else if core.RejectedOffAppCurrencyReason(data.FiatSymbol) != "" {
		// Re-checked here too, not just at submission (HandleSetHubRate) -
		// never trusted from submission time alone. See
		// core.AllowedOffAppCurrencies' doc comment for why USD
		// specifically can never confirm here.
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

	hubRulesStore.Set(tx.From, data.Text, data.PaymentWindowMinutes, data.MinReputationToApply, data.CannedReplies, data.RequireKYC, data.ServiceProviders, tx.Timestamp)
}

// applySponsoredPostCreate is the confirmation-time counterpart to
// HandleCreateSponsoredPost - re-checks everything (category, RC spend
// cap, validator existence, CCT balance) rather than trusting
// submission-time state alone, this file's usual discipline. Moves the
// real CCT via splitAdFee (api/ad_fee_split.go) BEFORE creating the
// record, so a Sponsored Post never exists without its fee having
// actually been paid and split.
func applySponsoredPostCreate(tx core.Transaction) {
	var data sponsoredPostCreateData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if len(data.Text) == 0 || len(data.Text) > 500 {
		return
	}
	if !core.IsAllowedSponsoredPostCategory(data.Category) {
		return
	}
	if data.ValidatorID != "" {
		if _, ok := validatorStore.Get(data.ValidatorID); !ok {
			return
		}
	}
	if data.CCTAmount < minSponsoredPostCCT {
		return
	}
	if data.CCTAmount > core.MaxAdSpendForRept(reptStore.BalanceOf(tx.From)) {
		return
	}

	store, err := resolveTokenStore("CCT")
	if err != nil {
		return
	}
	if store.BalanceOf(tx.From) < data.CCTAmount {
		return
	}

	assignedModerator := moderatorStore.AssignNext()

	splitAdFee(store, tx.From, data.ValidatorID, data.CCTAmount)

	sponsoredPostStore.Create(&core.SponsoredPost{
		ID:                  tx.ID,
		Advertiser:          tx.From,
		AdvertiserPublicKey: tx.AuthPublicKey,
		ValidatorID:         data.ValidatorID,
		Text:                data.Text,
		Category:            data.Category,
		CCTPaid:             data.CCTAmount,
		Status:              core.SponsoredPostPending,
		ModeratorAddress:    assignedModerator,
		CreatedAt:           tx.Timestamp,
	})

	if assignedModerator != "" {
		notifyUser(assignedModerator, "SPONSORED_POST_ASSIGNED", "A Sponsored Post is waiting on your review", tx.ID)
	}
}

// applySponsoredPostReview is the confirmation-time counterpart to
// HandleReviewSponsoredPost - re-verifies assignment and PENDING_REVIEW
// status (core.SponsoredPostStore.Review also enforces both, but this
// mirrors CompanyReview's belt-and-suspenders pattern of checking
// before any side effect).
func applySponsoredPostReview(tx core.Transaction) {
	var data sponsoredPostReviewData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	post, ok := sponsoredPostStore.Get(data.ID)
	if !ok || post.ModeratorAddress != tx.From || post.Status != core.SponsoredPostPending {
		return
	}

	if !sponsoredPostStore.Review(data.ID, tx.From, data.Approve, sponsoredPostRunLengthSeconds, tx.Timestamp) {
		return
	}

	if data.Approve {
		notifyUser(post.Advertiser, "SPONSORED_POST_APPROVED", "Your Sponsored Post is now live", tx.ID)
	} else {
		notifyUser(post.Advertiser, "SPONSORED_POST_REJECTED", "Your Sponsored Post was not approved", tx.ID)
	}
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
		counterAmount = hubTradeFiatAmountSmallest(data.Amount, data.Symbol, rate.SellRate, rate.FiatSymbol)
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
		skimPlatformFee(symbolStore, data.Symbol, userSymbolSide, data.Amount)
		skimPlatformFee(counterStore, rate.FiatSymbol, hubCounterSide, counterAmount)
		creator, creatorPublicKey = rate.InventoryAddress(), rate.HubPublicKey
		acceptedBy, acceptedByPublicKey = tx.From, tx.AuthPublicKey

	case "SELL_TO_HUB":
		// User pays Symbol, receives counterAmount of FiatSymbol from
		// the hub.
		if rate.BuyRate == 0 {
			return
		}
		counterAmount = hubTradeFiatAmountSmallest(data.Amount, data.Symbol, rate.BuyRate, rate.FiatSymbol)
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
		skimPlatformFee(symbolStore, data.Symbol, hubSymbolSide, data.Amount)
		skimPlatformFee(counterStore, rate.FiatSymbol, userCounterSide, counterAmount)
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
		fiatAmount := hubTradeFiatAmountWhole(data.Amount, data.Symbol, rate.SellRate)
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
		fiatAmount := hubTradeFiatAmountWhole(data.Amount, data.Symbol, rate.BuyRate)
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
	// See WalletRecoveryDiscontinuedCutoverUnixSeconds's doc comment -
	// linking a new recovery email is pointless once the migration
	// step itself (applyRecoverWallet, below) can never actually run,
	// so this closes off setup too, not just the final step.
	if tx.Timestamp >= WalletRecoveryDiscontinuedCutoverUnixSeconds {
		return
	}

	var data recoveryEmailRequestData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if len(data.Email) == 0 || len(data.Email) > 200 {
		return
	}

	recoveryStore.SetEmail(tx.From, data.Email)
}

// newContactEmailCode generates a 6-digit numeric code, easy to read
// out of an email and type back into a form - deliberately NOT the
// long-hex-token-in-a-link style api/validator_application.go's
// newConfirmToken uses, since that flow is a one-click link on the
// same device, while this one is commonly read on one device (email)
// and typed on another (the browser tab already open to settings).
// crypto/rand, not math/rand, matching this codebase's convention for
// anything code-like even when (as here) the actual security floor is
// the 15-minute expiry, not brute-force resistance.
func newContactEmailCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "000000"
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// applyRequestContactEmailCode is the fix for a real gap - see
// api/contact_email.go's package doc comment for the full story.
// Deliberately has NO cutover check (unlike applySetRecoveryEmail
// above) - this is a live, ongoing mechanism, not a retired one.
func applyRequestContactEmailCode(tx core.Transaction) {
	var data requestContactEmailCodeData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if len(data.Email) == 0 || len(data.Email) > 200 {
		return
	}

	code := newContactEmailCode()
	expiresAt := tx.Timestamp + contactEmailCodeValiditySeconds
	recoveryStore.SetPendingEmail(tx.From, data.Email, code, expiresAt)

	// Best-effort email, same "purely informational, doesn't gate
	// anything by itself, runs once per node in a multi-node
	// deployment" disclosed pattern as every other apply-time
	// notification in this codebase (see
	// applyValidatorApplicationSupport's identical caveat).
	if smtpConfigured() {
		go func(to, code string) {
			subject := "CreditChain: your verification code"
			body := fmt.Sprintf(
				"Your code to verify this email for CreditChain notifications is:\n\n%s\n\n"+
					"Enter it in the app within 15 minutes. If you didn't request this, you can ignore it.", code)
			if err := sendEmail(to, subject, body); err != nil {
				fmt.Println("email: failed to send contact-email verification code:", err)
			}
		}(data.Email, code)
	}
}

// applyConfirmContactEmail checks the submitted code and, if it
// matches, promotes the pending email to verified - see
// core.RecoveryStore.ConfirmPendingEmail's own doc comment for the
// exact check.
func applyConfirmContactEmail(tx core.Transaction) {
	var data confirmContactEmailData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	recoveryStore.ConfirmPendingEmail(tx.From, data.Code, tx.Timestamp)
}

// migrateWalletAssets moves everything keyed by the raw address
// (CCT, REPT, staked ETH, reputation/tier-progress) from oldAddress to
// newAddress. ETHTEST/USDCTEST live at a derived sub-address keyed by
// PUBLIC KEY, not the raw address (see core.DeriveTokenAddress) - only
// migrated if the caller supplied and proved the old public key (see
// HandleRecoverWallet's doc comment for why that's optional and what
// gets left behind if it's omitted).
func migrateWalletAssets(oldAddress, newAddress, newPublicKey, oldPublicKeyHex string) {
	if bal := cctStore.BalanceOf(oldAddress); bal != "0" {
		cctStore.Transfer(oldAddress, newAddress, bal)
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
	// Trust must move with Reputation, not just the flat mirror - found
	// during self-review of the TrustDimensions rollout: without this,
	// a migrating wallet would lose its real accumulated per-dimension
	// history and silently fall back to newWallet's own pre-seeded
	// all-50 (see NewWallet()), even though its overall Reputation
	// correctly transferred. Straight overwrite, matching the existing
	// Reputation/HighestTierReached transfer above - not a merge, same
	// as those two fields.
	newWallet.Trust = oldWallet.Trust
	oldWallet.Reputation = 0
	oldWallet.HighestTierReached = ""
	// Zeroed to the same "not yet seeded" zero value clearWallet's own
	// convention would produce - the old address is permanently frozen
	// (see core.RecoveryStore's doc comment), so this is purely for
	// consistency with Reputation being forced to 0 above, not because
	// anything will ever read it again.
	oldWallet.Trust = core.TrustDimensions{}
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
	// Permanently disabled on mainnet per audit finding C-04: this
	// mechanism's real security boundary was supposed to be here (the
	// consensus apply layer), but it never actually checked data.Code -
	// only HandleRecoverWallet did, at submission time, which a forged
	// transaction submitted directly to /tx/submit skipped entirely.
	// The remaining checks (linked email, creation year) are both
	// derivable from chain-visible/public data, not a real second
	// factor. No historical mainnet recovery transaction has ever
	// existed, so this can block unconditionally rather than needing a
	// cutover.
	if mainnetMode() {
		log.Println("wallet recovery permanently disabled on mainnet (audit finding C-04 + product decision, see WalletRecoveryDiscontinuedCutoverUnixSeconds): tx", tx.ID)
		return
	}

	// Discontinued on testnet too, as of the cutover - see
	// WalletRecoveryDiscontinuedCutoverUnixSeconds's doc comment. Not
	// just a C-04 security patch: a product decision that this was
	// never real account recovery and shouldn't be offered as an
	// alternative to actually saving your BIP-39 backup phrase.
	// Timestamp-gated (not mode-gated) so a fresh node resyncing
	// testnet's history still correctly replays the real recovery
	// migrations that already happened before this cutover.
	if tx.Timestamp >= WalletRecoveryDiscontinuedCutoverUnixSeconds {
		log.Println("wallet recovery discontinued (product decision, see WalletRecoveryDiscontinuedCutoverUnixSeconds):", tx.ID)
		return
	}

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
	releaseLoanCollateral(loan, tx.Timestamp)

	if data.Mode == "p2p" {
		reward, late := creditEngine.RepayLoan(loan, tx.Timestamp, tx.Timestamp >= TrustDimensionsCutoverUnixSeconds)
		if reward > 0 {
			mintRept(loan.Borrower, reward)
		}
		delta, reason := int64(core.ReputationDeltaLoanRepaidOnTime), "Repaid a peer-funded loan"
		if late {
			delta, reason = int64(core.ReputationDeltaLoanRepaidLate), "Repaid a peer-funded loan after its due date (within grace period)"
		}
		reputationEventStore.Record(&core.ReputationEvent{
			TxID: tx.ID, Target: loan.Borrower, Delta: delta,
			Reason: reason, Timestamp: tx.Timestamp, Dimension: core.DimensionRepayment.String(),
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

	if reward := creditEngine.FundLoan(loan, tx.From, tx.Timestamp >= TrustDimensionsCutoverUnixSeconds); reward > 0 {
		mintRept(tx.From, reward)
	}
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: tx.ID, Target: tx.From, Delta: int64(core.ReputationDeltaLoanFunded),
		Reason: "Funded a loan", Timestamp: tx.Timestamp, Dimension: core.DimensionContribution.String(),
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

	// TokenSymbol/TokenName/TokenInitialSupply/TokenStandard/
	// TokenPegReference are only read when Kind == TOKEN_CREATION - see
	// core.Proposal's matching fields and
	// api/token_creation_governance.go for what a PASSED outcome does.
	TokenSymbol        string `json:"tokenSymbol,omitempty"`
	TokenName          string `json:"tokenName,omitempty"`
	TokenInitialSupply uint64 `json:"tokenInitialSupply,omitempty"`
	TokenStandard      string `json:"tokenStandard,omitempty"`
	TokenPegReference  string `json:"tokenPegReference,omitempty"`

	// PredictionTargetSymbol/PredictionTargetPrice/
	// PredictionDurationSeconds are only read when Kind == PREDICTION -
	// see core.Proposal's matching fields and
	// api/prediction_vote.go's processPredictionResolution.
	// PredictionDurationSeconds lets a prediction run longer than the
	// ordinary 7-day governance window (unlike every other proposal
	// kind, whose Deadline is always the fixed
	// core.ProposalVotingWindowSeconds) - necessary for the "pool
	// active 14+ days" bonus to ever be reachable at all, since a fixed
	// 7-day deadline could never satisfy it.
	PredictionTargetSymbol    string `json:"predictionTargetSymbol,omitempty"`
	PredictionTargetPrice     uint64 `json:"predictionTargetPrice,omitempty"`
	PredictionDurationSeconds uint64 `json:"predictionDurationSeconds,omitempty"`

	// DisputeModeratorAddress/DisputeReferenceType/DisputeReferenceID
	// are only read when Kind == MODERATOR_DISPUTE - see
	// core.ProposalKindModeratorDispute's doc comment and
	// api/moderator_dispute_governance.go.
	DisputeModeratorAddress string `json:"disputeModeratorAddress,omitempty"`
	DisputeReferenceType    string `json:"disputeReferenceType,omitempty"`
	DisputeReferenceID      string `json:"disputeReferenceId,omitempty"`

	// NodeAdmissionPublicKey/NodeAdmissionNodeID are only read when
	// Kind == NODE_ADMISSION - see core.ProposalKindNodeAdmission's
	// doc comment and api/node_admission_governance.go.
	NodeAdmissionPublicKey string `json:"nodeAdmissionPublicKey,omitempty"`
	NodeAdmissionNodeID    string `json:"nodeAdmissionNodeId,omitempty"`

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
		// TOKEN_CREATION proposals cost a real CCT fee, charged here
		// (never trusting the submission-time balance check alone) and
		// never refunded regardless of outcome - a submission cost for
		// using a governance slot, per the user's own "must charge high
		// fee" instruction. Re-validate the token fields too, since
		// HandleCreateProposal's own checks could be stale by the time
		// this confirms (e.g. the symbol got registered by a different
		// proposal in the meantime).
		if core.ProposalKind(data.Kind) == core.ProposalKindTokenCreation {
			if !core.ValidateTokenSymbol(data.TokenSymbol) || !core.ValidateTokenName(data.TokenName) {
				return
			}
			if data.TokenInitialSupply == 0 || data.TokenInitialSupply > core.MaxCustomTokenSupply {
				return
			}
			if _, exists := tokenRegistry.Meta[data.TokenSymbol]; exists {
				return
			}
			if data.TokenStandard == "" {
				data.TokenStandard = "C1"
			}
			if !tokenStandardRegistry.IsCreatable(data.TokenStandard) {
				return
			}
			if reptStore.BalanceOf(tx.From) < TokenCreationMinRC {
				return
			}
			feeSmallest := core.CctWholeToSmallest(TokenCreationProposalFeeCCT)
			if !cctStore.Transfer(tx.From, tokenCreationFeeAddress(), core.FormatCctSmallest(feeSmallest)) {
				return
			}
		}

		// PREDICTION proposals need a real target and a real duration -
		// re-validated here (never trusting HandleCreateProposal's
		// submission-time check alone), same discipline as every other
		// kind above.
		deadline := tx.Timestamp + core.ProposalVotingWindowSeconds
		var predictionDuration int64
		if core.ProposalKind(data.Kind) == core.ProposalKindPrediction {
			if !isValidPredictionTargetSymbol(data.PredictionTargetSymbol) || data.PredictionTargetPrice == 0 {
				return
			}
			predictionDuration = predictionDurationSeconds(data.PredictionDurationSeconds)
			deadline = tx.Timestamp + predictionDuration
		}

		// A MODERATOR_DISPUTE proposal must genuinely reference a real
		// record that DisputeModeratorAddress was actually assigned to
		// - re-validated here (never trusting HandleCreateProposal's
		// submission-time check alone), same discipline as every other
		// kind above.
		if core.ProposalKind(data.Kind) == core.ProposalKindModeratorDispute {
			if !moderatorStore.IsModerator(data.DisputeModeratorAddress) {
				return
			}
			if !moderatorDisputeReferenceValid(data.DisputeReferenceType, data.DisputeReferenceID, data.DisputeModeratorAddress) {
				return
			}
		}

		// A NODE_ADMISSION proposal must name a well-formed, not-yet-
		// admitted Ed25519 public key - re-validated here (never
		// trusting HandleCreateProposal's submission-time check alone),
		// same discipline as every other kind above. See
		// core.ProposalKindNodeAdmission's doc comment for why this
		// governs peer-network Sybil resistance and gets a higher quorum
		// than an ordinary proposal (quorum set just below).
		quorum := core.DefaultProposalQuorum
		if core.ProposalKind(data.Kind) == core.ProposalKindNodeAdmission {
			pub, err := hex.DecodeString(data.NodeAdmissionPublicKey)
			if err != nil || len(pub) != ed25519.PublicKeySize {
				return
			}
			if nodeAdmissionStore.IsAdmitted(data.NodeAdmissionPublicKey) {
				return
			}
			quorum = NodeAdmissionProposalQuorum
		}

		id := "prop-" + tx.ID[:min(8, len(tx.ID))]
		proposalStore.Create(&core.Proposal{
			ID:                        id,
			Title:                     data.Title,
			Description:               data.Description,
			CreatedBy:                 tx.From,
			Open:                      true,
			Quorum:                    quorum,
			Deadline:                  deadline,
			Kind:                      core.ProposalKind(data.Kind),
			ParamKey:                  data.ParamKey,
			ParamValue:                data.ParamValue,
			TargetVersion:             data.TargetVersion,
			TokenSymbol:               data.TokenSymbol,
			TokenName:                 data.TokenName,
			TokenInitialSupply:        data.TokenInitialSupply,
			TokenStandard:             data.TokenStandard,
			TokenPegReference:         data.TokenPegReference,
			PredictionTargetSymbol:    data.PredictionTargetSymbol,
			PredictionTargetPrice:     data.PredictionTargetPrice,
			PredictionDurationSeconds: predictionDuration,
			DisputeModeratorAddress:   data.DisputeModeratorAddress,
			DisputeReferenceType:      data.DisputeReferenceType,
			DisputeReferenceID:        data.DisputeReferenceID,
			NodeAdmissionPublicKey:    data.NodeAdmissionPublicKey,
			NodeAdmissionNodeID:       data.NodeAdmissionNodeID,
			SystemGenerated:           data.SystemGenerated,
		})

	case "vote":
		applyProposalVoteHeadcount(data.ProposalID, tx.From, data.Support, tx.Timestamp)

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

// applyProposalVoteHeadcount registers the ordinary weighted Yes/No
// vote every proposal has, regardless of kind - factored out of
// applyGovernance's "vote" case so applyPredictionVote
// (api/prediction_vote.go) can reuse the exact same logic instead of
// duplicating it, since a PREDICTION-kind proposal's combined
// vote-plus-RC-stake action still needs to register this same
// headcount vote. Returns true if the vote was actually registered
// (false if the wallet has no voting weight, e.g. never activated
// governance).
func applyProposalVoteHeadcount(proposalID string, from string, support bool, timestamp int64) bool {
	// Voting power is read fresh at confirmation time, not at
	// submission time - your vote's weight reflects your
	// reputation/stake when it actually lands in a block. Zero means
	// the wallet never activated governance at all (see votingWeight's
	// doc comment) - the HTTP handler already pre-checks this, but the
	// apply-side check is what's actually enforced, same as every
	// other consensus-applied guard in this file.
	//
	// NODE_ADMISSION votes are weighted by accumulated conviction
	// instead (votingWeightForNodeAdmission) - see that function's own
	// doc comment (api/governance_weight.go) for why this proposal
	// kind specifically doesn't use the general reputation/stake
	// formula every other kind uses.
	power := votingWeight(from)
	if p, ok := proposalStore.Get(proposalID); ok && p.Kind == core.ProposalKindNodeAdmission {
		power = votingWeightForNodeAdmission(from)
	}
	if power == 0 {
		return false
	}

	_, err := proposalStore.Vote(proposalID, from, support, power, timestamp)
	return err == nil
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

// GovernanceTrustDeltaProposerPassed/GovernanceTrustDeltaVoterPassed are
// the FIRST real events feeding Governance Trust (see trust.go) -
// deliberately reusing this exact "genuinely passed" signal rather
// than granting trust for merely casting a vote, which would be
// trivially farmable by voting on everything indiscriminately.
// Asymmetric like ModeratorReputationBonus (5) vs
// ModeratorConfirmReputationBonus (1) - authoring a proposal that
// actually mobilizes real support to pass is a stronger signal than
// being one of many voters.
const (
	GovernanceTrustDeltaProposerPassed = 5
	GovernanceTrustDeltaVoterPassed    = 1
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

	// A PREDICTION's RC-bet resolution (price check + pro-rata payout,
	// see api/prediction_vote.go's processPredictionResolution) is
	// orthogonal to the ordinary Yes/No headcount quorum check below -
	// whether the target price was actually hit doesn't depend on
	// whether enough people also cast the ordinary vote, so this always
	// runs for a PREDICTION proposal, even one that ends up FAILED for
	// quorum purposes.
	if p.Kind == core.ProposalKindPrediction {
		processPredictionResolution(p, asOf)
	}

	totalWeight := p.YesVotes + p.NoVotes
	if totalWeight < p.Quorum || p.YesVotes <= p.NoVotes {
		p.Status = core.ProposalStatusFailed
		return
	}

	p.Status = core.ProposalStatusPassed

	// Real incentives, only on a genuine PASSED outcome - see the
	// constants' doc comment above.
	mintRept(p.CreatedBy, ProposalPassedProposerReward)
	if reward := increaseTrust(walletStore.Get(p.CreatedBy), core.DimensionGovernance, GovernanceTrustDeltaProposerPassed, asOf); reward > 0 {
		mintRept(p.CreatedBy, reward)
	}
	reputationEventStore.Record(&core.ReputationEvent{
		TxID: "proposal-passed-" + p.ID, Target: p.CreatedBy, Delta: int64(GovernanceTrustDeltaProposerPassed),
		Reason: "Authored a proposal that passed", Timestamp: asOf, Dimension: core.DimensionGovernance.String(),
	})

	var voters []string
	for addr := range proposalStore.Voted[p.ID] {
		voters = append(voters, addr)
	}
	sort.Strings(voters)
	for _, addr := range voters {
		mintRept(addr, ProposalPassedVoterReward)
		if reward := increaseTrust(walletStore.Get(addr), core.DimensionGovernance, GovernanceTrustDeltaVoterPassed, asOf); reward > 0 {
			mintRept(addr, reward)
		}
		reputationEventStore.Record(&core.ReputationEvent{
			TxID: "proposal-passed-" + p.ID + "-voter-" + addr, Target: addr, Delta: int64(GovernanceTrustDeltaVoterPassed),
			Reason: "Voted on a proposal that passed", Timestamp: asOf, Dimension: core.DimensionGovernance.String(),
		})
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
			governanceParamsStore.SetAt(p.ParamKey, p.ParamValue, asOf)
			syncTrustWeightIfNeeded(p.ParamKey, p.ParamValue)
		}
	case core.ProposalKindVersionActivation:
		if p.TargetVersion != "" {
			core.ActivatedProtocolVersion = p.TargetVersion
		}
	case core.ProposalKindTokenCreation:
		applyTokenCreationFromProposal(p)
	case core.ProposalKindModeratorDispute:
		applyModeratorDisputePenalty(p)
	case core.ProposalKindNodeAdmission:
		applyNodeAdmissionFromProposal(p, asOf)
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

// CCTTransferFee is a legacy whole-CCT-scale constant kept ONLY for
// pendingReservedBalance/HandleTokenTransfer's pre-submission balance
// check (api/pending_balance.go, api/token_transfer.go) - a
// deliberately conservative, whole-CCT-rounded-UP reservation, not the
// real amount actually charged. Safe to keep coarse: a fast-fail UX
// check can afford to occasionally over-reserve (rejecting a transfer
// slightly earlier than strictly necessary), it just must never
// under-reserve - the real, precise charge happens below via
// CCTTransferFeeSmallestUnit and applyTransfer's own authoritative
// re-check, same "never trust submission-time state" split this
// codebase already applies everywhere else.
const CCTTransferFee = 1

// DefaultCCTTransferFeeSmallestUnit is the REAL fee charged on every
// plain CCT send (0.3 CCT, in CctDecimals-scaled smallest units) - see
// api/governance_params.go's "CCTTransferFeeSmallestUnit" for the
// admin-tunable override. Now representable with real precision
// (unlike this constant's predecessor, back when CCT balances were
// plain whole-number uint64 with no smaller unit - see core/cct_amount.go's
// 18-decimal CctDecimals, added by the later CCT decimal migration)
// - deliberately a SEPARATE governance key from the legacy
// "CCTTransferFee" above rather than reinterpreting its already-seeded
// value of 1, which would have silently meant "charge 1 smallest unit"
// (an inconsequential ~0) instead of a deliberate migration.
const DefaultCCTTransferFeeSmallestUnit uint64 = 300_000_000_000_000_000

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
		// proposal changing "CCTTransferFeeSmallestUnit" takes effect
		// here without a code change - see api/governance_params.go.
		// Charged with full smallest-unit precision directly via
		// cctStore, NOT the whole-unit `store` resolved above (that's
		// cctAdapter, which floor-rounds - see
		// core.CctSmallestToWholeFloor's own doc comment - so a
		// sub-1-CCT fee like the current 0.3 default isn't
		// representable through it at all).
		feeSmallest := new(big.Int).SetUint64(governanceParam("CCTTransferFeeSmallestUnit", DefaultCCTTransferFeeSmallestUnit))
		totalNeededSmallest := new(big.Int).Add(core.CctWholeToSmallest(tx.Amount), feeSmallest)

		fromBalanceSmallest, err := core.ParseCctSmallest(cctStore.BalanceOf(fromSide))
		if err != nil || fromBalanceSmallest.Cmp(totalNeededSmallest) < 0 {
			return
		}
		if !cctStore.Transfer(fromSide, cctTransferFeePoolAddress(), core.FormatCctSmallest(feeSmallest)) {
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
	} else if isCcc1TransferFeeSymbol(symbol) {
		// Own-currency-pays-its-own-fee, same shape as ETHB/BTCB above -
		// approximates what this transfer would really cost on the
		// token's actual chain (see ccc1TransferFeeSmallest), split via
		// splitPlatformFee (see that function's own doc comment for the
		// current burn/validator/admin/Outernet/moderator breakdown)
		// instead of a single fixed bridge-fee address, matching this
		// session's marketplace-fee redesign. fee is 0 (no-op) if the
		// relevant USD price hasn't been admin-set yet. Not a moderated
		// dispute, so the moderator share always pools (see
		// applyPlatformFee's own doc comment on resolvingModerator).
		fee := ccc1TransferFeeSmallest(symbol)
		total := tx.Amount + fee
		if store.BalanceOf(fromSide) < total {
			return
		}
		splitPlatformFee(store, symbol, fromSide, fee, "")
	}

	// tx.To is whatever the SENDER typed in - for CCT/REPT/custom
	// tokens that's already the recipient's real balance key (resolveBalanceAddress's
	// default case returns it unchanged), but for every symbol that
	// unifies with a real external address or a derived sub-address
	// (ETHB/ETH_CCC1/USDC_CCC1's real self-custodial ETH address,
	// BTC_CCC1/ETHTEST/USDCTEST/YENE/BTCB's derived tag-address), the
	// recipient's OWN balance is stored under THAT resolved address,
	// never their bare CreditChain address - crediting tx.To directly
	// would land the transfer at a key the recipient's own balance
	// lookups (which always resolve through this same function) can
	// never find. A real gap caught in review: every UI send flow
	// validates the recipient as a plain CC address (e.g.
	// send-bridged-eth.html's CCT-network mode, vault-withdraw.html's
	// "Send to CreditChain user" mode), so resolving it here is what
	// actually delivers on that contract.
	toSide := resolveBalanceAddress(tx.To, walletStore.Get(tx.To).PublicKeyHex, symbol)
	store.Transfer(fromSide, toSide, tx.Amount)
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
	// Never trust submission-time state: even if a stale client or a
	// malicious relay somehow got a faucet-claim transaction into the
	// mempool, this is the authoritative re-check, identically enforced
	// on every node - see mainnetMode's doc comment.
	if mainnetMode() {
		return
	}
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

	cctStore.Mint(tx.From, core.FormatCctSmallest(core.CctWholeToSmallest(FaucetAmount)))
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
	if isMainnetBlockedSymbol(data.Symbol) {
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
		if !cctStore.Transfer(tx.From, escrow, cost) {
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
	var takerFilled uint64
	takerActualCost := big.NewInt(0)
	for _, trade := range trades {
		buyOrder := orderByID(taker, trade.BuyOrderID)
		sellOrder := orderByID(taker, trade.SellOrderID)
		if buyOrder == nil || sellOrder == nil {
			continue
		}

		cctCost := core.OrderCCTCost(trade.Amount, trade.Price)
		buyerBaseSide := resolveBalanceAddress(buyOrder.Owner, buyOrder.PublicKey, trade.Symbol)

		store.Transfer(orderEscrowAddress(sellOrder.ID), buyerBaseSide, trade.Amount)
		cctStore.Transfer(orderEscrowAddress(buyOrder.ID), sellOrder.Owner, cctCost)

		lastPrice = trade.Price
		if trade.BuyOrderID == taker.ID {
			takerFilled += trade.Amount
			cctCostBig, _ := core.ParseCctSmallest(cctCost)
			takerActualCost.Add(takerActualCost, cctCostBig)
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
		worstCaseCost, _ := core.ParseCctSmallest(core.OrderCCTCost(takerFilled, data.Price))
		if worstCaseCost.Cmp(takerActualCost) > 0 {
			surplus := new(big.Int).Sub(worstCaseCost, takerActualCost)
			cctStore.Transfer(escrow, tx.From, core.FormatCctSmallest(surplus))
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
		cctStore.Transfer(escrow, order.Owner, refund)
	}
}
