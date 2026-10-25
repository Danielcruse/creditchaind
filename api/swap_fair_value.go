package api

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// api/swap_fair_value.go: a fair-value swap between the real internal-
// ledger balances this node actually holds - deliberately NOT a
// floating CCT-paired AMM pool (unlike ETHTEST/USDCTEST/BTCTEST's
// pools). A real bridged asset must not float against an illiquid
// pool - see the earlier, deliberate "no cctEthbPool/cctBtcbPool"
// decision - so this instead converts at the live external
// price-oracle rate (api/price_oracle.go), with CCT used only as the
// accounting unit shown back to the caller for transparency, never as
// a pool a real asset's value depends on.
//
// Covers every "vault" balance a real bridge deposit actually credits -
// BTCB and ETHB (both minted only once a real, detected external
// deposit confirms - see applyBridgeDeposit/applyBridgeBtcDeposit) plus
// YENE (the internal stable token). Any one of these can swap with any
// other, always routed through CCT as the accounting unit (see
// computeFairValueSwap) even though no pool reserves are ever touched.
// ETHB was previously excluded here on the theory that it needed the
// KMS-custodial signer - that's not actually true: this endpoint only
// ever mints/burns the INTERNAL ledger balance (core.TokenStore.Burn/
// Mint), the same as it already does for BTCB/YENE. The KMS signer is
// only ever needed to broadcast a REAL outbound ETH transaction (see
// api/eth_bridge_withdraw.go's HandleBridgedEthWithdraw) - a completely
// separate code path this swap never touches, so there's no real
// dependency on KMS being configured. Self-custodial ETH/USDC/BTC still
// live entirely on their own chains, not in any CreditChain ledger, so
// there is nothing here for this endpoint to move for those (a real
// swap of those would need actual on-chain DEX integration, a
// separate, much larger feature).
//
// ETH_CCC1/BTC_CCC1 (the vault-backed tokens, see api/vault_token.go)
// are deliberately EXCLUDED here, unlike the legacy BTCB/ETHB mirror
// tokens - this is a real fund-safety fix, not a style choice.
// fairValueMint below unconditionally mints the TO symbol with no
// check against real vault reserves; for BTCB/ETHB/YENE that's fine
// (none of them ever claimed hard 1:1 backing by a real locked asset -
// BTCB/ETHB are the old "mirror model" bridge, YENE is a purely
// internal stable token, see this file's own doc comment above). But
// ETH_CCC1/BTC_CCC1 exist specifically to be redeemable 1:1 for a real
// asset genuinely locked in the vault contract - letting this endpoint
// mint one of them on demand (by burning the other) would let
// circulating CCC1 supply exceed what's actually locked, a real
// insolvency path with no real BTC/ETH ever having been deposited to
// back the new tokens. A safe swap between two real vault-backed
// assets needs an actual counterparty who already holds the other
// side - that's exactly what the Hub Rate marketplace's ONCHAIN mode
// already provides (see settleHubTradeOnChain in api/apply.go, which
// only ever moves real balances between two real parties, never
// mints) - website/js/swap-bridged.js now points users there instead
// of running this mint-based logic for these two symbols.
var fairValueSwapSymbols = map[string]bool{
	"BTCB": true, "ETHB": true, "YENE": true,
}

// usdValuePerUnit returns one native unit's USD value: BTCB's native
// unit is a satoshi (BtcUsd/1e8), ETHB's native unit is a wei
// (EthUsd/1e18); YENE has no external oracle price at all, since it's
// a purely internal stable token with no real backing - it's assigned
// a flat 1.0 USD/unit, the same peg assumption website/js/currency.js's
// fiatLine() already makes for USDCTEST/YENE.
func usdValuePerUnit(symbol string) (float64, bool) {
	priceOracleMu.Lock()
	usd := priceOracleCache.Rates["USD"]
	priceOracleMu.Unlock()

	switch symbol {
	case "BTCB":
		if usd.Btc <= 0 {
			return 0, false
		}
		return usd.Btc / 1e8, true
	case "ETHB":
		if usd.Eth <= 0 {
			return 0, false
		}
		return usd.Eth / 1e18, true
	case "YENE":
		return 1.0, true
	default:
		return 0, false
	}
}

// computeFairValueSwap converts fromAmount (native units of
// fromSymbol) into toSymbol's native units at the current live-oracle
// rate, chained through CCT as the accounting unit (per the user's
// "all swaps route through CCT" convention) even though no pool
// reserves are ever touched - cctValue is purely informational,
// included in the response/recorded transaction for transparency.
func computeFairValueSwap(fromSymbol, toSymbol string, fromAmount uint64) (toAmount uint64, usdValue, cctValue float64, err error) {
	priceOracleMu.Lock()
	updatedAt := priceOracleCache.UpdatedAt
	priceOracleMu.Unlock()
	// This is the one place the oracle moves REAL bridged-asset value
	// (see this file's top doc comment) - refuse outright rather than
	// silently swap at a reading that's gone stale (CoinGecko AND its
	// Coinbase fallback both down, or the poll loop never started) -
	// see priceStalenessSeconds's own doc comment.
	if updatedAt == 0 || time.Now().Unix()-updatedAt > priceStalenessSeconds {
		return 0, 0, 0, fmt.Errorf("price data is stale - try again shortly")
	}

	fromUsdPerUnit, ok := usdValuePerUnit(fromSymbol)
	if !ok {
		return 0, 0, 0, fmt.Errorf("no live price available for %s", fromSymbol)
	}
	toUsdPerUnit, ok := usdValuePerUnit(toSymbol)
	if !ok {
		return 0, 0, 0, fmt.Errorf("no live price available for %s", toSymbol)
	}

	usdValue = float64(fromAmount) * fromUsdPerUnit
	if usdValue <= 0 {
		return 0, 0, 0, fmt.Errorf("computed zero value for this swap")
	}

	cctPerEth, _ := cctPriceInETH()
	priceOracleMu.Lock()
	ethUsd := priceOracleCache.Rates["USD"].Eth
	priceOracleMu.Unlock()
	if ethUsd > 0 && cctPerEth > 0 {
		cctUsdPrice := ethUsd / cctPerEth
		cctValue = usdValue / cctUsdPrice
	}

	toAmountFloat := usdValue / toUsdPerUnit
	toAmount = uint64(toAmountFloat) // floor - never round up past what the USD value actually covers
	if toAmount == 0 {
		return 0, 0, 0, fmt.Errorf("swap amount too small to produce any %s", toSymbol)
	}
	return toAmount, usdValue, cctValue, nil
}

// fairValueSwapStore resolves the plain core.TokenStore backing a
// swappable symbol - every symbol still allowed here (BTCB/ETHB/YENE)
// shares this same store shape, unlike ETH_CCC1's core.DecimalTokenStore,
// which is exactly why ETH_CCC1/BTC_CCC1 were excluded rather than
// special-cased (see this file's top doc comment).
func fairValueSwapStore(symbol string) *core.TokenStore {
	switch symbol {
	case "BTCB":
		return bridgedBtcStore
	case "ETHB":
		return bridgedEthStore
	case "YENE":
		return yenStore
	default:
		return nil
	}
}

// fairValueBalanceOf/fairValueBurn/fairValueMint are the type-bridging
// layer letting HandleFairValueSwap/applyFairValueSwap treat every
// swappable symbol uniformly. amount/balance are always expressed as a
// native-unit uint64 (wei for ETHB, satoshis for BTCB) at this layer.
func fairValueBalanceOf(symbol, address string) *big.Int {
	store := fairValueSwapStore(symbol)
	if store == nil {
		return big.NewInt(0)
	}
	return new(big.Int).SetUint64(store.BalanceOf(address))
}

func fairValueBurn(symbol, address string, amount uint64) bool {
	store := fairValueSwapStore(symbol)
	if store == nil {
		return false
	}
	return store.Burn(address, amount)
}

func fairValueMint(symbol, address string, amount uint64) {
	store := fairValueSwapStore(symbol)
	if store == nil {
		return
	}
	store.Mint(address, amount)
}

// fairValueSwapRequest is exactly what the client signs - FromSymbol/
// ToSymbol/FromAmount are the only fields it could possibly know in
// advance. verifyTxAuth (api/apply.go) unmarshals BOTH tx.AuthPayload
// and tx.Data into this same narrow type (json.Unmarshal silently
// ignores tx.Data's extra toAmount/usdValue/cctValue keys), so the
// comparison only ever checks fields the client actually committed to
// - the server-computed fields below are never compared against a
// signature that couldn't have covered them.
type fairValueSwapRequest struct {
	FromSymbol string `json:"fromSymbol"`
	ToSymbol   string `json:"toSymbol"`
	FromAmount uint64 `json:"fromAmount"`
}

// fairValueSwapData is the full recorded tx.Data - the signed request
// fields plus the server-computed snapshot (see computeFairValueSwap).
type fairValueSwapData struct {
	fairValueSwapRequest
	ToAmount uint64  `json:"toAmount"`
	UsdValue float64 `json:"usdValue"`
	CctValue float64 `json:"cctValue"`
}

// HandleFairValueSwap is signed (the caller's own balance is what
// moves). ToAmount/UsdValue/CctValue are computed here, once, and
// carried into the recorded transaction - applyFairValueSwap trusts
// this snapshot rather than re-deriving a live price at apply time, so
// every node credits the identical amount regardless of any price
// movement between submission and block confirmation.
func HandleFairValueSwap(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req fairValueSwapRequest
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !fairValueSwapSymbols[req.FromSymbol] || !fairValueSwapSymbols[req.ToSymbol] {
		http.Error(w, "swap is only available between BTCB, ETHB, or YENE - trade ETH_CCC1/BTC_CCC1 through the Trading Hub marketplace instead", http.StatusBadRequest)
		return
	}
	if req.FromSymbol == req.ToSymbol {
		http.Error(w, "fromSymbol and toSymbol must differ", http.StatusBadRequest)
		return
	}
	if req.FromAmount == 0 {
		http.Error(w, "amount must be greater than zero", http.StatusBadRequest)
		return
	}

	fromAddr := resolveBalanceAddress(address, auth.PublicKey, req.FromSymbol)
	confirmedBalance := fairValueBalanceOf(req.FromSymbol, fromAddr)
	pending := pendingReservedBalance(address, req.FromSymbol)
	need := new(big.Int).SetUint64(req.FromAmount)
	need.Add(need, new(big.Int).SetUint64(pending))
	if confirmedBalance.Cmp(need) < 0 {
		if pending > 0 {
			http.Error(w, fmt.Sprintf("insufficient available %s balance - part of it is reserved by a transaction you already submitted that hasn't confirmed yet", req.FromSymbol), http.StatusBadRequest)
			return
		}
		http.Error(w, fmt.Sprintf("insufficient %s balance", req.FromSymbol), http.StatusBadRequest)
		return
	}

	toAmount, usdValue, cctValue, err := computeFairValueSwap(req.FromSymbol, req.ToSymbol, req.FromAmount)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	data := fairValueSwapData{
		fairValueSwapRequest: fairValueSwapRequest{
			FromSymbol: req.FromSymbol,
			ToSymbol:   req.ToSymbol,
			FromAmount: req.FromAmount,
		},
		ToAmount: toAmount,
		UsdValue: usdValue,
		CctValue: cctValue,
	}
	dataJSON, _ := json.Marshal(data)
	tx := recordTx(auth, address, "", 0, core.TxFairValueSwap, string(dataJSON))

	// fromAmount/toAmount are returned as decimal strings, not bare JSON
	// numbers - ETHB is wei-denominated (up to ~1e18), well past JS's
	// 2^53 safe-integer range, so a plain number here would silently
	// round in the browser before it's ever displayed. The request side
	// avoids this the same way api/token_transfer.go's amount field
	// already does (see website/js/send-bridged-eth.js's apiPostSignedRaw
	// usage) - by keeping FromAmount a real Go uint64 and having the
	// client embed it as a raw, unquoted JSON literal built from a
	// BigInt rather than round-tripping through a JS Number.
	json.NewEncoder(w).Encode(map[string]any{
		"status":     "pending",
		"txId":       tx.ID,
		"fromAmount": fmt.Sprintf("%d", req.FromAmount),
		"toAmount":   fmt.Sprintf("%d", toAmount),
		"usdValue":   usdValue,
		"cctValue":   cctValue,
	})
}

// applyFairValueSwap is the authoritative, block-confirmation-time
// execution - re-checks the balance (never trust submission-time
// state) and, only if it still holds, burns FromAmount from the
// sender's FromSymbol balance and mints the already-snapshotted
// ToAmount into their ToSymbol balance. Burn (not Transfer, since this
// moves value between two DIFFERENT token ledgers, not within one) -
// same "destroy on one side, create on the other" pattern already
// established for applyRepay's supply-correction burn.
func applyFairValueSwap(tx core.Transaction) {
	var data fairValueSwapData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if !fairValueSwapSymbols[data.FromSymbol] || !fairValueSwapSymbols[data.ToSymbol] || data.ToAmount == 0 {
		return
	}

	fromAddr := resolveBalanceAddress(tx.From, tx.AuthPublicKey, data.FromSymbol)
	toAddr := resolveBalanceAddress(tx.From, tx.AuthPublicKey, data.ToSymbol)

	if !fairValueBurn(data.FromSymbol, fromAddr, data.FromAmount) {
		return
	}
	fairValueMint(data.ToSymbol, toAddr, data.ToAmount)
}
