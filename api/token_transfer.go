package api

import (
	"encoding/json"
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
	}

	fromSide := resolveBalanceAddress(from, auth.PublicKey, req.Symbol)
	if store.BalanceOf(fromSide) < required {
		json.NewEncoder(w).Encode(
			map[string]bool{
				"success": false,
			},
		)
		return
	}

	// USDCTEST pays its fee in ETH, from a separate ETHB balance - see
	// applyTransfer's USDCTEST branch, which re-checks this
	// authoritatively.
	if req.Symbol == "USDCTEST" {
		ethbSide := resolveBalanceAddress(from, auth.PublicKey, "ETHB")
		if bridgedEthStore.BalanceOf(ethbSide) < UsdcTestTransferFeeWei {
			json.NewEncoder(w).Encode(
				map[string]bool{
					"success": false,
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
		resp["fee"] = governanceParam("CCTTransferFee", CCTTransferFee)
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
	json.NewEncoder(w).Encode(resp)
}
