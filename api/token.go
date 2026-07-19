package api

import (
	"github.com/creditchain/creditchaind/core"
)

var tokenStore =
	core.NewTokenStore()

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

	yenStore.Token.Name = "CreditChain YEN-e"
	yenStore.Token.Symbol = "YENE"

	bridgedEthStore.Token.Name = "CreditChain Bridged ETH"
	bridgedEthStore.Token.Symbol = "ETHB"
}