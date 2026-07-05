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

func init() {
	usdcTestStore.Token.Name = "CreditChain Test USDC"
	usdcTestStore.Token.Symbol = "USDCTEST"

	reptStore.Token.Name = "CreditChain Reputation Token"
	reptStore.Token.Symbol = "REPT"

	ethTestStore.Token.Name = "CreditChain Test ETH"
	ethTestStore.Token.Symbol = "ETHTEST"
}