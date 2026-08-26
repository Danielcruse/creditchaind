package api

import (
	"errors"
	"os"

	"github.com/creditchain/creditchaind/core"
)

// mainnetMode reports whether this node is configured as a real
// mainnet deployment (Environment=MAINNET_MODE=true in the systemd
// unit) rather than a testnet/dev instance. Defaults to false (today's
// exact unchanged testnet behavior) so no existing deployment needs to
// set anything to keep working - this must be explicitly opted into,
// not opted out of, matching this project's fail-closed convention for
// every other "is this really production" gate (ETH_CUSTODIAL_KMS_ENABLED,
// ADMIN_WALLET_ADDRESS, etc.).
func mainnetMode() bool {
	if core.ActiveProtocolManifest.Set {
		return core.ActiveProtocolManifest.MainnetMode
	}
	return os.Getenv("MAINNET_MODE") == "true"
}

// errFaucetDisabledOnMainnet / errTestOnlySymbolBlockedOnMainnet are
// the two mainnet-specific refusals this gate produces - see
// mainnetMode's doc comment and this file's own top-level notes.
var (
	errFaucetDisabledOnMainnet        = errors.New("the faucet does not exist on mainnet - it mints free tokens with no real backing, which has no meaning once real value is involved")
	errTestOnlySymbolBlockedOnMainnet = errors.New("USDCTEST is a synthetic testnet faucet token with no real backing - it does not exist on mainnet; a real bridged USDC is a separate, not-yet-built feature")
	// errTokenMintDisabledOnMainnet closes the same gap the faucet
	// gate above already closes for the public faucet - HandleTokenMint's
	// own doc comment already describes it as "an operator-only faucet
	// for testnet seeding," but nothing enforced that until now. Unlike
	// the faucet (unauthenticated, so the risk is anyone), this one is
	// admin-authed - the risk here is an operator using it out of habit
	// (e.g. to fund a validator's stake, exactly as this session did on
	// testnet) without realizing mainnet CCT is supposed to represent
	// real, backed value with nothing to conjure it from.
	errTokenMintDisabledOnMainnet = errors.New("the operator token-mint faucet does not exist on mainnet - it mints tokens with no real backing, which has no meaning once real value is involved")
)

// mainnetBlockedSymbols lists testnet-only synthetic tokens that must
// never be mintable/transferable/tradeable/stakeable on a real mainnet
// deployment - checked from every token-resolution chokepoint
// (lookupCustomToken, stakeTokenStore) so no code path (transfer, pool
// swap/liquidity, P2P, order book, staking, admin mint) can route
// around it. Deliberately scoped to just USDCTEST for now, per the
// user's explicit instruction - ETHTEST/BTCTEST are developer-only
// price-reference pools already de-exposed from every user-facing nav
// (see developers.html's own doc note), a materially different risk
// profile than USDCTEST's still-user-facing "buy/hold/send USDC" role,
// so they're not swept into this same blocklist without a separate,
// deliberate decision.
var mainnetBlockedSymbols = map[string]bool{
	"USDCTEST": true,
}

func isMainnetBlockedSymbol(symbol string) bool {
	return mainnetMode() && mainnetBlockedSymbols[symbol]
}
