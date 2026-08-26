package api

import (
	"encoding/json"
	"math/big"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// tokenTransferData carries which symbol a TxExchange moves - CCT is
// still the default/implicit case for backward compatibility with
// transactions recorded before this field existed (empty Data), but
// any resolvable symbol (ETHTEST/USDCTEST, REPT, or a developer-
// registered token) can now be sent the same way.
type tokenTransferData struct {
	To     string `json:"to"`
	Amount uint64 `json:"amount"`
	Symbol string `json:"symbol"`
}

func HandleTokenTransfer(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req tokenTransferData

	auth, from, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Amount == 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}
	if req.Symbol == "" {
		req.Symbol = "CCT"
	}

	store, err := resolveTokenStore(req.Symbol)
	if err != nil {
		http.Error(w, "unknown symbol", http.StatusNotFound)
		return
	}

	// A CCT send also needs to cover CCTTransferFee on top of the
	// amount itself; an ETHB send similarly needs to cover
	// ETHBCCTNetworkFeeWei on top - both fees now come from the SAME
	// balance as the amount being sent (see applyTransfer, which
	// re-checks this authoritatively). USDCTEST's fee is charged in ETH
	// from a separate ETHB balance, checked below.
	required := req.Amount
	if req.Symbol == "CCT" {
		required += governanceParam("CCTTransferFee", CCTTransferFee)
	} else if req.Symbol == "ETHB" {
		required += ETHBCCTNetworkFeeWei
	} else if req.Symbol == "BTCB" {
		required += BTCBCCTNetworkFeeSats
	} else if isCcc1TransferFeeSymbol(req.Symbol) {
		required += ccc1TransferFeeSmallest(req.Symbol)
	}

	// Available balance is the confirmed balance minus whatever this
	// same address already has reserved in a still-pending (mempool,
	// unconfirmed) transaction - see pendingReservedBalance's own doc
	// comment for why this matters: without it, two transactions
	// submitted back-to-back against the same balance would each pass
	// this check individually, and whichever confirms second would
	// silently no-op with no reason ever surfaced to the caller.
	fromSide := resolveBalanceAddress(from, auth.PublicKey, req.Symbol)
	confirmedBalance := store.BalanceOf(fromSide)
	pending := pendingReservedBalance(from, req.Symbol)
	if pending >= confirmedBalance || confirmedBalance-pending < required {
		reason := "Insufficient balance."
		if pending > 0 {
			reason = "Insufficient available balance - part of it is reserved by a transaction you already submitted that hasn't confirmed yet."
		}
		json.NewEncoder(w).Encode(
			map[string]any{
				"success": false,
				"reason":  reason,
			},
		)
		return
	}

	// USDCTEST pays its fee in ETH, from a separate ETHB balance - see
	// applyTransfer's USDCTEST branch, which re-checks this
	// authoritatively.
	if req.Symbol == "USDCTEST" {
		ethbSide := resolveBalanceAddress(from, auth.PublicKey, "ETHB")
		ethbConfirmed := bridgedEthStore.BalanceOf(ethbSide)
		ethbPending := pendingReservedBalance(from, "ETHB")
		if ethbPending >= ethbConfirmed || ethbConfirmed-ethbPending < UsdcTestTransferFeeWei {
			reason := "Insufficient ETH balance to cover the USDC send fee."
			if ethbPending > 0 {
				reason = "Insufficient available ETH balance to cover the USDC send fee - part of it is reserved by a transaction you already submitted that hasn't confirmed yet."
			}
			json.NewEncoder(w).Encode(
				map[string]any{
					"success": false,
					"reason":  reason,
				},
			)
			return
		}
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, from, req.To, req.Amount, core.TxExchange, string(data))

	resp := map[string]any{
		"status": "pending",
		"txId":   tx.ID,
	}
	if req.Symbol == "CCT" {
		feeSmallest := new(big.Int).SetUint64(governanceParam("CCTTransferFeeSmallestUnit", DefaultCCTTransferFeeSmallestUnit))
		resp["fee"] = core.CctSmallestToDecimalString(feeSmallest)
	}
	if req.Symbol == "ETHB" {
		resp["feeWei"] = ETHBCCTNetworkFeeWei
	}
	if req.Symbol == "USDCTEST" {
		resp["feeWei"] = UsdcTestTransferFeeWei
	}
	if req.Symbol == "BTCB" {
		resp["feeSats"] = BTCBCCTNetworkFeeSats
	}
	if isCcc1TransferFeeSymbol(req.Symbol) {
		resp["fee"] = ccc1TransferFeeSmallest(req.Symbol)
	}
	json.NewEncoder(w).Encode(resp)
}
