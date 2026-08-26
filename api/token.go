package api

import (
	"math/big"
	"strconv"

	"github.com/creditchain/creditchaind/core"
)

// tokenStore was CCT's ledger before the 18-decimal migration. It is
// left in place (never deleted - "hide, don't delete") but no longer
// carries any live CCT balance after applyTokenomicsDecimalMigration
// runs - see api/tokenomics_migration.go. cctStore (below) is CCT's
// real ledger going forward.
var tokenStore = core.NewTokenStore()

// cctStore is CCT's ledger as of the 18-decimal migration - see
// core/cct_store.go's doc comment for why CCT needed its own
// big-int-backed store instead of reusing TokenStore like every other
// token here still does.
var cctStore = core.NewCctStore()

// wholeUnitStore is the minimal balance-store interface every generic,
// symbol-driven code path already programs against via
// resolveTokenStore (api/p2p_offers.go) - P2P offers, hub trades, loan
// funding/repayment, the order book, buy requests, admin transfer, and
// more (~45 call sites). Satisfied structurally by *core.TokenStore
// (every token except CCT) and by cctWholeUnitAdapter below (CCT,
// wrapping the real big-int cctStore) - this is what lets every one of
// those existing call sites keep working completely unchanged after
// CCT moved off TokenStore: they only ever called
// Mint/Transfer/Burn/BalanceOf, so as long as something satisfying
// those four methods is returned, the caller doesn't need to know or
// care which concrete ledger is actually behind it.
type wholeUnitStore interface {
	Mint(address string, amount uint64)
	Transfer(from, to string, amount uint64) bool
	Burn(address string, amount uint64) bool
	BalanceOf(address string) uint64
}

// cctWholeUnitAdapter lets CCT participate in every generic,
// symbol-driven code path that expects whole-unit uint64 amounts -
// matching the same "the ledger itself is big-int, but staking/fees
// and other generic paths stay whole-CCT at the boundary" pattern
// already established elsewhere in this migration (see
// core/staking_sync.go's doc comment) - by converting via
// CctWholeToSmallest/CctSmallestToWholeFloor. The real, precise ledger
// underneath is always cctStore; BalanceOf here floor-rounds to whole
// CCT, a disclosed simplification for these generic paths (sub-CCT
// precision stays reachable through CCT's own dedicated endpoints/AMM
// code, which talk to cctStore directly in smallest units).
type cctWholeUnitAdapter struct{}

func (cctWholeUnitAdapter) Mint(address string, amount uint64) {
	cctStore.Mint(address, core.FormatCctSmallest(core.CctWholeToSmallest(amount)))
}

func (cctWholeUnitAdapter) Transfer(from, to string, amount uint64) bool {
	return cctStore.Transfer(from, to, core.FormatCctSmallest(core.CctWholeToSmallest(amount)))
}

func (cctWholeUnitAdapter) Burn(address string, amount uint64) bool {
	return cctStore.Burn(address, core.FormatCctSmallest(core.CctWholeToSmallest(amount)))
}

func (cctWholeUnitAdapter) BalanceOf(address string) uint64 {
	return core.CctSmallestToWholeFloor(cctStore.BalanceOf(address))
}

var cctAdapter wholeUnitStore = cctWholeUnitAdapter{}

// ccc1DecimalWholeUnitAdapter lets ETH_CCC1/USDC_CCC1 (both
// *core.DecimalTokenStore, see api/vault_token.go) participate in
// wholeUnitStore's generic uint64 code paths - same adapter pattern as
// cctWholeUnitAdapter above, but with an identity conversion (no
// decimal-shifting) since "whole unit" here already means the store's
// own smallest unit (wei for ETH_CCC1, USDC's 6-decimal smallest unit
// for USDC_CCC1) - the same convention api/swap_fair_value.go's
// fairValueBurn/fairValueMint already established for these two
// stores. BTC_CCC1 needs no adapter - btcCcc1Store is already a plain
// *core.TokenStore, which satisfies wholeUnitStore directly.
type ccc1DecimalWholeUnitAdapter struct {
	store *core.DecimalTokenStore
}

func (a ccc1DecimalWholeUnitAdapter) Mint(address string, amount uint64) {
	a.store.Mint(address, strconv.FormatUint(amount, 10))
}

func (a ccc1DecimalWholeUnitAdapter) Transfer(from, to string, amount uint64) bool {
	return a.store.Transfer(from, to, strconv.FormatUint(amount, 10))
}

func (a ccc1DecimalWholeUnitAdapter) Burn(address string, amount uint64) bool {
	return a.store.Burn(address, strconv.FormatUint(amount, 10))
}

func (a ccc1DecimalWholeUnitAdapter) BalanceOf(address string) uint64 {
	n, ok := new(big.Int).SetString(a.store.BalanceOf(address), 10)
	if !ok || !n.IsUint64() {
		return 0
	}
	return n.Uint64()
}

var ethCcc1TransferAdapter wholeUnitStore = ccc1DecimalWholeUnitAdapter{store: ethCcc1Store}
var usdcCcc1TransferAdapter wholeUnitStore = ccc1DecimalWholeUnitAdapter{store: usdcCcc1Store}
var stethCcc1TransferAdapter wholeUnitStore = ccc1DecimalWholeUnitAdapter{store: stethCcc1Store}

// usdcTestStore is a completely separate ledger from tokenStore (CCT)
// - borrowed funds land here, not in CCT, so "what you earned by
// staking" and "what you borrowed" are visibly distinct balances,
// never commingled. Clearly named/labeled as a test token throughout
// the UI - this has no connection to real USDC and never will unless
// this codebase goes through the real custody + audit + legal work
// described elsewhere in this project's history.
var usdcTestStore = core.NewTokenStore()

// reptStore is the reputation-progression token - minted only via
// applyFundLoan/applyRepay's call into core.IncreaseWalletReputation,
// never mintable directly by any handler (see the reward's one-time,
// first-time-per-tier design in core/reputation_reward.go).
var reptStore = core.NewTokenStore()

// tokenRegistry holds every developer-registered custom token and its
// CCT-paired liquidity pool - see core/token_registry.go.
var tokenRegistry = core.NewTokenRegistry()

// ethTestStore is a genuine, holdable/transferable ETHTEST balance -
// deliberately separate from Wallet.StakedETH (which represents
// locked validator/collateral stake, a different concept with its own
// already-disclosed simplifications - see core/staking_logic.go).
// This is "test ETH sitting in your wallet," the same kind of balance
// CCT/USDCTEST/REPT already are, so it can be displayed, transferred,
// and swapped like any of them.
var ethTestStore = core.NewTokenStore()

// cctEthTestPool/cctUsdcTestPool are the built-in CCT-paired liquidity
// pools for the network's own native test tokens - the same
// core.LiquidityPool machinery every developer-registered token's pool
// uses (see core/liquidity_pool.go), just for ETHTEST/USDCTEST instead
// of a custom symbol. This is what makes CCT the universal hub
// currency: every swap in the system, built-in or custom, ultimately
// prices against CCT through one of these pools.
var cctEthTestPool = core.NewLiquidityPool("ETHTEST")
var cctUsdcTestPool = core.NewLiquidityPool("USDCTEST")

// btcTestStore/cctBtcTestPool are BTCTEST's counterpart to ETHTEST -
// a synthetic, CCT-paired, price-discovery token, NOT the same thing
// as bridgedBtcStore (BTCB, a real 1:1 bridged balance that
// deliberately never floats against CCT). BTCTEST/ETHTEST together are
// this network's two developer-accessible price-reference pools (see
// governance's PREDICTION proposal kind, api/prediction_vote.go) -
// deliberately kept out of outernet/swap.html/liquidity.html's nav
// (same "hide, don't delete" precedent already applied to
// ETHTEST/USDCTEST) since CreditChain itself does not want to present
// a direct FX market to end users, only expose the pool endpoints to
// anyone building against the API.
var btcTestStore = core.NewTokenStore()
var cctBtcTestPool = core.NewLiquidityPool("BTCTEST")

// yenStore is YEN-e, the stable settlement currency (lending,
// marketplace payments, transfers) - deliberately separate from CCT
// (network utility/staking/governance) per the approved tokenomics
// split. cctYenPool exists (same pattern as cctEthTestPool/
// cctUsdcTestPool) purely so pool-consuming handlers that dereference
// a symbol's pool unconditionally don't nil-panic on YENE - it starts
// at zero reserves and is deliberately left unseeded/untradeable in
// this phase, since YEN-e is meant to be a fixed-price stable asset,
// not something floating against CCT yet.
var yenStore = core.NewTokenStore()
var cctYenPool = core.NewLiquidityPool("YENE")

// bridgedEthStore is ETHB - the auto-bridge feature's single unified
// ETH balance (api/eth_bridge.go). Unlike YEN-e/ETHTEST/USDCTEST it
// has no CCT-paired liquidity pool at all (see resolveTokenStore's
// comment in api/p2p_offers.go for why) - it's meant to track real
// bridged Sepolia ETH 1:1, not float against CCT. Units are wei.
var bridgedEthStore = core.NewTokenStore()

// bridgedBtcStore is BTCB - the bridged-BTC counterpart to ETHB, same
// "real bridged balance, no CCT-paired AMM pool" shape (see
// bridgedEthStore's doc comment for why). Deliberately self-custodial
// on the deposit side, not KMS-custodial like ETHB - see
// core/btc_address.go's doc comment. Units are satoshis.
var bridgedBtcStore = core.NewTokenStore()

// btcAddressStore tracks which self-custodial BTC testnet address each
// wallet has registered for deposit-watching - see
// core/btc_address.go's doc comment.
var btcAddressStore = core.NewBtcAddressStore()

// priceHistoryStore is a bounded, per-symbol price time series fed by
// recordPoolPrice (api/apply.go) every time a swap or liquidity change
// actually moves a pool's ratio - purely observational, never itself
// consulted by any swap/pricing logic. See core/price_history.go.
var priceHistoryStore = core.NewPriceHistoryStore()

func init() {
	usdcTestStore.Token.Name = "CreditChain Test USDC"
	usdcTestStore.Token.Symbol = "USDCTEST"

	reptStore.Token.Name = "CreditChain Reputation Token"
	reptStore.Token.Symbol = "REPT"

	ethTestStore.Token.Name = "CreditChain Test ETH"
	ethTestStore.Token.Symbol = "ETHTEST"

	btcTestStore.Token.Name = "CreditChain Test BTC"
	btcTestStore.Token.Symbol = "BTCTEST"

	yenStore.Token.Name = "CreditChain YEN-e"
	yenStore.Token.Symbol = "YENE"

	bridgedEthStore.Token.Name = "CreditChain Bridged ETH"
	bridgedEthStore.Token.Symbol = "ETHB"

	bridgedBtcStore.Token.Name = "CreditChain Bridged BTC"
	bridgedBtcStore.Token.Symbol = "BTCB"
}
