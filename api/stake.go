package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

func HandleStake(
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
	store, err := stakeTokenStore(req.Symbol)
	if err != nil {
		http.Error(w, fmt.Sprintf("unsupported stake symbol %q - must be CCT, ETHTEST, or USDCTEST", req.Symbol), http.StatusBadRequest)
		return
	}

	if stakingStore.StakeOf(address, req.Symbol)+req.Amount > core.MaxTestStakePerAccount {
		http.Error(w, fmt.Sprintf("%s stake is capped at %d per account during the pilot", req.Symbol, core.MaxTestStakePerAccount), http.StatusBadRequest)
		return
	}

	// Fast-fail on the real balance now that staking actually debits
	// it - the same re-check-at-confirmation pattern every other
	// handler in this codebase uses means this is only a courtesy
	// (the authoritative check is applyStake's Transfer call), sparing
	// the caller a pending tx that's certain to be silently dropped.
	balanceAddr := resolveBalanceAddress(address, auth.PublicKey, req.Symbol)
	if store.BalanceOf(balanceAddr) < req.Amount {
		http.Error(w, fmt.Sprintf("insufficient %s balance to stake", req.Symbol), http.StatusBadRequest)
		return
	}

	// The stake only actually lands - and the reward only actually
	// mints - once a block confirms this transaction (see
	// api/apply.go). This is just the estimate a validator would
	// compute if nothing else changes before then.
	estimatedReward := core.RewardForStake(req.Amount)

	data, _ := json.Marshal(stakeData{Symbol: req.Symbol, Amount: req.Amount})
	tx := recordTx(auth, address, "", req.Amount, core.TxStakeETH, string(data))

	json.NewEncoder(w).Encode(
		map[string]any{
			"status":          "pending",
			"txId":            tx.ID,
			"symbol":          req.Symbol,
			"estimatedReward": estimatedReward,
		},
	)
}
