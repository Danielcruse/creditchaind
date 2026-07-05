package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

func HandleBorrow(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	type Request struct {
		Amount uint64
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

	if !hasEnoughCollateral(address, req.Amount) {
		http.Error(
			w,
			"insufficient locked collateral - lock staked CCT or oETH covering at least this amount first",
			http.StatusBadRequest,
		)
		return
	}

	wallet := walletStore.Get(
		address,
	)

	limit := core.BorrowLimit(
		wallet.Reputation,
	)

	if req.Amount > limit {
		http.Error(
			w,
			"borrow limit exceeded",
			http.StatusBadRequest,
		)
		return
	}

	// The interest rate is locked in now, at the reputation the
	// borrower has at submission time, rather than recomputed when
	// the block confirms - otherwise a reputation change between
	// submission and confirmation would silently change the terms of
	// a loan the borrower already agreed to.
	interest := core.InterestRate(
		wallet.Reputation,
	)

	data, _ := json.Marshal(borrowData{Interest: interest})

	tx := recordTx(auth, "", address, req.Amount, core.TxBorrow, string(data))

	json.NewEncoder(w).Encode(
		map[string]string{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}
