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
		req.Symbol = "CCT"
	}

	// ETHTEST/USDCTEST are retired as NEW-stake symbols (see
	// applyStake's identical gate in api/apply.go) - only CCT and ETHB
	// are stakeable now. stakeTokenStore itself still resolves ETHTEST
	// (it has to, for existing legacy unbonding entries maturing via
	// api/unstake.go), so this has to be an explicit reject here, not
	// just "let stakeTokenStore fail" - it wouldn't. Rejecting here
	// (rather than silently accepting a pending tx that applyStake
	// then drops) is what actually surfaces a clear error to the user
	// instead of a stake that looks submitted but never lands.
	if req.Symbol == "ETHTEST" || req.Symbol == "USDCTEST" {
		http.Error(w, fmt.Sprintf("%s is no longer stakeable - ETHB replaced it, CCT and ETHB are the only stakeable symbols now", req.Symbol), http.StatusBadRequest)
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
	//
	// CCT is special-cased to match applyStake's own special-case
	// (api/apply.go): CCT's real, live balance lives in cctStore (the
	// big-int, 18-decimal store) since the tokenomics decimal
	// migration - stakeTokenStore("CCT") still resolves to the OLD,
	// frozen pre-migration tokenStore instance (kept only as a
	// historical snapshot, see core/cct_store.go's doc comment), which
	// is why checking it here made every real CCT stake attempt fail
	// with a false "insufficient balance" even when the account's
	// actual cctStore balance was more than enough.
	if req.Symbol == "CCT" {
		cctBalance, _ := core.ParseCctSmallest(cctStore.BalanceOf(address))
		if cctBalance.Cmp(core.CctWholeToSmallest(req.Amount)) < 0 {
			http.Error(w, "insufficient CCT balance to stake", http.StatusBadRequest)
			return
		}
	} else {
		store, err := stakeTokenStore(req.Symbol)
		if err != nil {
			http.Error(w, fmt.Sprintf("unsupported stake symbol %q - must be CCT, ETHB, or ETH_CCC1", req.Symbol), http.StatusBadRequest)
			return
		}
		balanceAddr := resolveBalanceAddress(address, auth.PublicKey, req.Symbol)
		if store.BalanceOf(balanceAddr) < req.Amount {
			http.Error(w, fmt.Sprintf("insufficient %s balance to stake", req.Symbol), http.StatusBadRequest)
			return
		}
	}

	// The stake only actually lands once a block confirms this
	// transaction (see api/apply.go's applyStake) - staking is a pure
	// locking mechanism now, no reward is minted for it (mining/block
	// rewards are what pays validators, per PoSR).
	data, _ := json.Marshal(stakeData{Symbol: req.Symbol, Amount: req.Amount})
	tx := recordTx(auth, address, "", req.Amount, core.TxStakeETH, string(data))

	json.NewEncoder(w).Encode(
		map[string]any{
			"status": "pending",
			"txId":   tx.ID,
			"symbol": req.Symbol,
		},
	)
}
