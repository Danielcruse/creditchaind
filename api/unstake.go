package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

func HandleUnstake(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	type Request struct {
		Amount uint64
		Symbol string
	}

	var req Request

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Amount == 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}

	if req.Symbol == "" {
		req.Symbol = "ETHTEST"
	}
	if _, err := stakeTokenStore(req.Symbol); err != nil {
		http.Error(w, fmt.Sprintf("unsupported stake symbol %q - must be CCT, ETHTEST, or USDCTEST", req.Symbol), http.StatusBadRequest)
		return
	}

	if stakingStore.AvailableToUnbond(address, req.Symbol) < req.Amount {
		http.Error(w, fmt.Sprintf("not enough staked %s available (some may already be unbonding)", req.Symbol), http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(stakeData{Symbol: req.Symbol, Amount: req.Amount})
	tx := recordTx(auth, address, "", req.Amount, core.TxUnstakeETH, string(data))

	json.NewEncoder(w).Encode(
		map[string]any{
			"status":              "pending",
			"txId":                tx.ID,
			"symbol":              req.Symbol,
			"unbondingPeriodSecs": core.UnbondingPeriodSeconds,
		},
	)
}
