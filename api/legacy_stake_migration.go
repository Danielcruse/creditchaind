package api

import (
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/creditchain/creditchaind/core"
)

// DefaultYenPerCCTFallback/DefaultYenPerUSDCTESTFallback are disclosed
// fallback reference rates for the legacy-stake migration's YEN-e
// conversion (see convertLegacyStakeToYen) - used only when a pool has
// no live quote (never traded), same "illustrative starting point,
// not a live market rate" disclosure DefaultCCTPerETH (api/cct_price.go)
// already carries. Deliberately flat, simple judgment calls (both
// USDCTEST and YEN-e are meant to track roughly $1, so a direct 1:1
// is the least-fabricated starting point available) rather than a
// second derived formula - and deliberately NOT sourced from the live
// external price oracle (GET /price/external), which is node-local,
// non-deterministic, and would break cross-node consensus if a
// consensus-critical sweep like this one depended on it.
const DefaultYenPerCCTFallback = 1.0
const DefaultYenPerUSDCTESTFallback = 1.0

// legacyStakeSymbolsToRedeem is ETHTEST and USDCTEST - the two
// retired stakeable symbols (see api/apply.go's applyStake) this
// one-time sweep closes out.
var legacyStakeSymbolsToRedeem = []string{"ETHTEST", "USDCTEST"}

// legacyStakeMigrationBatchSize bounds how many addresses
// processLegacyStakeMigrationSweep redeems in any single block -
// same defensive-from-day-one batching reasoning as
// api/reputation_mining.go's miningBatchSize, even though this is a
// one-time sweep rather than a recurring one (retrofitting batching
// after an unbatched sweep already shipped would be far riskier than
// building it batched from the start).
const legacyStakeMigrationBatchSize = 250

// legacyStakeMigrationSweep holds the in-progress state for the
// ONE-TIME (not recurring, unlike reputationMiningSweep in
// api/reputation_mining.go) legacy-stake redemption sweep - same
// snapshot/batch/persist-across-restart shape, minus the recurring-
// epoch part. Once done is true, every future call is a no-op
// forever.
var legacyStakeMigrationSweep = struct {
	mu             sync.Mutex
	initialized    bool
	done           bool
	pubKeysBackfilled bool
	pendingAddrs   []string
}{}

// processLegacyStakeMigrationSweep is a ONE-TIME protocol migration
// (not a recurring epoch sweep like processReputationMining) that
// closes out every wallet's legacy ETHTEST/USDCTEST stake - both are
// retired as stakeable symbols (see api/apply.go's applyStake),
// replaced going forward by ETHB for the "stake ETH, get an oETH
// collateral receipt" mechanic. Converts any FREE (not locked as an
// open loan's collateral) legacy stake into an equivalent YEN-e
// payout via the real CCT-paired AMM pools (cctEthTestPool/
// cctUsdcTestPool -> cctYenPool), the same "quote both legs before
// touching any balance" discipline creditMaturedETHTestViaSwap
// already uses for maturing ETHTEST unbonds.
//
// Stake that IS locked as an open (FUNDED) loan's oETH collateral is
// deliberately left untouched here - see
// maybeRedeemNowFreeLegacyStake for how that gets picked up once the
// backing loan resolves and the collateral is released.
//
// Same block-timestamp-driven, sorted-iteration determinism every
// other sweep in this codebase uses, and the same batched-across-
// multiple-blocks/persisted-progress shape processReputationMining
// uses - except this sweep only ever runs ONCE: once
// legacyStakeMigrationSweep.done is true, every subsequent call is a
// no-op forever, unlike a recurring epoch.
func processLegacyStakeMigrationSweep(block core.Block) {
	legacyStakeMigrationSweep.mu.Lock()
	if legacyStakeMigrationSweep.done {
		legacyStakeMigrationSweep.mu.Unlock()
		return
	}
	if !legacyStakeMigrationSweep.pubKeysBackfilled {
		// core.Wallet.PublicKeyHex is a NEW field, populated going
		// forward from every signed transaction's AuthPublicKey (see
		// applyTxOnce) - but every wallet that staked ETHTEST/USDCTEST
		// BEFORE this field existed has it empty until it happens to
		// sign something new, which this one-time sweep can't wait on.
		// Backfilling once from the full, already-confirmed chain
		// history (every past transaction already carries its
		// signer's real AuthPublicKey) closes that gap before the
		// redemption pass below ever needs it.
		backfillWalletPublicKeysFromChain()
		legacyStakeMigrationSweep.pubKeysBackfilled = true
	}
	if !legacyStakeMigrationSweep.initialized {
		legacyStakeMigrationSweep.initialized = true
		legacyStakeMigrationSweep.pendingAddrs = collectLegacyStakeAddrs()
	}
	if len(legacyStakeMigrationSweep.pendingAddrs) == 0 {
		legacyStakeMigrationSweep.done = true
		legacyStakeMigrationSweep.mu.Unlock()
		return
	}
	n := legacyStakeMigrationBatchSize
	if n > len(legacyStakeMigrationSweep.pendingAddrs) {
		n = len(legacyStakeMigrationSweep.pendingAddrs)
	}
	batch := legacyStakeMigrationSweep.pendingAddrs[:n]
	legacyStakeMigrationSweep.pendingAddrs = legacyStakeMigrationSweep.pendingAddrs[n:]
	if len(legacyStakeMigrationSweep.pendingAddrs) == 0 {
		legacyStakeMigrationSweep.done = true
	}
	legacyStakeMigrationSweep.mu.Unlock()

	for _, addr := range batch {
		redeemFreeLegacyStakeFor(addr, block.Timestamp)
	}
}

// backfillWalletPublicKeysFromChain scans every already-confirmed
// transaction once, populating core.Wallet.PublicKeyHex for any
// address that hasn't had it captured yet by applyTxOnce's normal
// (going-forward) path - see that function's doc comment. Safe to
// call multiple times (idempotent, last-write-wins on a value that
// never actually changes for a given address), but
// legacyStakeMigrationSweep only ever calls it once.
func backfillWalletPublicKeysFromChain() {
	if blockchain == nil {
		return
	}
	for _, block := range blockchain.Blocks {
		for _, tx := range block.Transactions {
			if tx.AuthAddress == "" || tx.AuthPublicKey == "" {
				continue
			}
			w := walletStore.Get(tx.AuthAddress)
			if w.PublicKeyHex == "" {
				w.PublicKeyHex = tx.AuthPublicKey
			}
		}
	}
}

// collectLegacyStakeAddrs snapshots every wallet address with nonzero
// ETHTEST or USDCTEST stake at the moment the sweep starts - sorted
// for the same cross-node-determinism reason every other sweep here
// sorts its iteration order (Go map order is randomized).
func collectLegacyStakeAddrs() []string {
	seen := make(map[string]bool)
	for addr, symbols := range stakingStore.Stakes {
		for _, symbol := range legacyStakeSymbolsToRedeem {
			if symbols[symbol] > 0 {
				seen[addr] = true
			}
		}
	}
	addrs := make([]string, 0, len(seen))
	for addr := range seen {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	return addrs
}

// redeemFreeLegacyStakeFor closes out address's FREE (non-loan-locked)
// legacy ETHTEST/USDCTEST stake into YEN-e. Called both from the main
// sweep above and from maybeRedeemNowFreeLegacyStake once a
// previously-locked position frees up.
func redeemFreeLegacyStakeFor(address string, timestamp int64) {
	redeemLegacyStakeSymbol(address, "ETHTEST", timestamp)
	redeemLegacyStakeSymbol(address, "USDCTEST", timestamp)
}

func redeemLegacyStakeSymbol(address, symbol string, timestamp int64) {
	staked := stakingStore.StakeOf(address, symbol)
	if staked == 0 {
		return
	}

	free := staked
	if symbol == "ETHTEST" {
		// oETH is ETHTEST's collateral receipt. Once oETH is reserved
		// to a specific open loan (reserveCollateralForLoan), it's
		// already removed from BOTH wallet.OETHBalance (moved out at
		// lock time, applyLockCollateral) AND
		// collateralStore.Locked[addr]["OETH"] (removed at reservation
		// time, collateralStore.Release) - these two buckets are
		// mutually exclusive with "reserved to a loan" by construction,
		// not overlapping, so summing them directly gives exactly the
		// free (never committed to any specific loan) amount, with no
		// further subtraction needed. Same trust in this existing
		// accounting invariant hasEnoughCollateral/
		// peekCollateralSymbolForLoan already place in it elsewhere in
		// this file.
		w := walletStore.Get(address)
		availableOETH := w.OETHBalance + collateralStore.LockedOf(address, "OETH")
		if availableOETH < free {
			free = availableOETH
		}
	}
	// USDCTEST has no collateral-lock concept anywhere in this
	// codebase (confirmed: core/collateral_store.go's Locked map is
	// only ever keyed "CCT"/"OETH") - all of it is free.

	if free == 0 {
		return
	}

	w := walletStore.Get(address)
	if w.PublicKeyHex == "" {
		// Can't derive this wallet's YEN-e sub-address without its real
		// public key (see core.Wallet.PublicKeyHex's doc comment) -
		// leave this position alone; it'll be picked up on a later
		// pass once this address signs something and the backfill (or
		// applyTxOnce going forward) captures its key. Extremely rare
		// in practice - any wallet with real stake has necessarily
		// signed at least the original stake transaction.
		return
	}
	pub, err := hex.DecodeString(w.PublicKeyHex)
	if err != nil {
		return
	}

	yenAmount, ok := convertLegacyStakeToYen(symbol, free, timestamp)
	if !ok {
		return
	}

	if !stakingStore.ReduceStakeDirect(address, symbol, free) {
		return
	}
	if symbol == "ETHTEST" {
		if w.OETHBalance >= free {
			w.OETHBalance -= free
		} else {
			w.OETHBalance = 0
		}
	}

	yenStore.Mint(core.DeriveTokenAddress(pub, core.YenAddressTag), yenAmount)

	notifyUser(address, "LEGACY_STAKE_REDEEMED",
		fmt.Sprintf("Your legacy %s stake (%d) has been converted to %d YEN-e as part of the ETH/USDC migration.", symbol, free, yenAmount),
		"")
}

// convertLegacyStakeToYen moves amount of symbol (ETHTEST or
// USDCTEST) out of its stake escrow (see stakeEscrowAddress), through
// the real CCT-paired AMM pools, into a YEN-e amount - the same
// "quote both legs before touching any balance" discipline
// creditMaturedETHTestViaSwap already uses for maturing ETHTEST
// unbonds, just a two-hop (symbol -> CCT -> YEN-e) swap instead of a
// round trip back to the same symbol. Falls back to the disclosed
// DefaultCCTPerETH/DefaultCCTPerYen reference rates if either pool
// has no live quote (e.g. never traded) - the same
// never-silently-lose-funds convention every other swap path here
// follows, so a migration attempted on a pool-less fresh node still
// produces a real, disclosed number instead of silently skipping.
func convertLegacyStakeToYen(symbol string, amount uint64, timestamp int64) (yenAmount uint64, ok bool) {
	var fromPool *core.LiquidityPool
	var fromStore *core.TokenStore
	switch symbol {
	case "ETHTEST":
		fromPool = cctEthTestPool
		fromStore = ethTestStore
	case "USDCTEST":
		fromPool = cctUsdcTestPool
		fromStore = usdcTestStore
	default:
		return 0, false
	}

	escrow := stakeEscrowAddress(symbol)
	fromPoolEscrow := poolAddress(symbol)
	yenPoolEscrow := poolAddress("YENE")

	quotedCCT, cctOk := fromPool.QuoteTokenForCCT(amount)
	yenOk := false
	if cctOk {
		_, yenOk = cctYenPool.QuoteCCTForToken(quotedCCT)
	}

	if cctOk && yenOk {
		if !fromStore.Transfer(escrow, fromPoolEscrow, amount) {
			return 0, false
		}
		intermediateCCT, _ := fromPool.SwapTokenForCCT(amount)
		cctStore.Transfer(fromPoolEscrow, yenPoolEscrow, intermediateCCT)
		finalYen, _ := cctYenPool.SwapCCTForToken(intermediateCCT)
		recordPoolPrice(symbol, fromPool, timestamp)
		recordPoolPrice("YENE", cctYenPool, timestamp)
		return finalYen, true
	}

	// Fallback: no live pool quote for one or both legs - convert
	// directly via the disclosed flat reference rates instead of
	// skipping this wallet's migration entirely (never silently lose
	// funds, matching every other swap path's own convention).
	if symbol == "ETHTEST" {
		// ETHTEST is already treated everywhere else in this app as
		// "test ETH" 1:1 (see cctPriceInETH's own doc comment) - route
		// through the existing disclosed CCT/ETH fallback rate
		// (DefaultCCTPerETH = CCT's price in ETH terms, so dividing by
		// it converts an ETH-denominated amount into CCT), then the
		// flat CCT->YEN-e fallback above, rather than inventing a
		// third, unrelated constant.
		cctAmount := float64(amount) / DefaultCCTPerETH
		return uint64(cctAmount * DefaultYenPerCCTFallback), true
	}
	return uint64(float64(amount) * DefaultYenPerUSDCTESTFallback), true
}

// resetLegacyStakeMigrationSweepForTest clears the in-progress sweep
// state - test-only, mirrors resetReputationMiningSweepForTest
// (api/reputation_mining.go).
func resetLegacyStakeMigrationSweepForTest() {
	legacyStakeMigrationSweep.mu.Lock()
	defer legacyStakeMigrationSweep.mu.Unlock()
	legacyStakeMigrationSweep.initialized = false
	legacyStakeMigrationSweep.done = false
	legacyStakeMigrationSweep.pubKeysBackfilled = false
	legacyStakeMigrationSweep.pendingAddrs = nil
}

// legacyStakeMigrationSweepSnapshot persists the one-time sweep's
// in-progress state across a restart - required for correctness, not
// just durability: without this, a node that restarts mid-sweep would
// lose track of which addresses it already redeemed and re-derive a
// fresh pendingAddrs snapshot from current (already-partially-redeemed)
// state, which happens to be safe here (redeemLegacyStakeSymbol is a
// no-op once an address's legacy stake is already zero) but would
// still silently re-run the pubkey backfill and re-scan the full
// stake map on every restart - persisting `done` short-circuits both
// once the migration has genuinely finished.
type legacyStakeMigrationSweepSnapshot struct {
	Initialized       bool     `json:"initialized"`
	Done              bool     `json:"done"`
	PubKeysBackfilled bool     `json:"pubKeysBackfilled"`
	PendingAddrs      []string `json:"pendingAddrs"`
}

func snapshotLegacyStakeMigrationSweep() legacyStakeMigrationSweepSnapshot {
	legacyStakeMigrationSweep.mu.Lock()
	defer legacyStakeMigrationSweep.mu.Unlock()

	addrs := make([]string, len(legacyStakeMigrationSweep.pendingAddrs))
	copy(addrs, legacyStakeMigrationSweep.pendingAddrs)

	return legacyStakeMigrationSweepSnapshot{
		Initialized:       legacyStakeMigrationSweep.initialized,
		Done:              legacyStakeMigrationSweep.done,
		PubKeysBackfilled: legacyStakeMigrationSweep.pubKeysBackfilled,
		PendingAddrs:      addrs,
	}
}

func restoreLegacyStakeMigrationSweep(snap legacyStakeMigrationSweepSnapshot) {
	legacyStakeMigrationSweep.mu.Lock()
	defer legacyStakeMigrationSweep.mu.Unlock()

	legacyStakeMigrationSweep.initialized = snap.Initialized
	legacyStakeMigrationSweep.done = snap.Done
	legacyStakeMigrationSweep.pubKeysBackfilled = snap.PubKeysBackfilled
	legacyStakeMigrationSweep.pendingAddrs = snap.PendingAddrs
}
