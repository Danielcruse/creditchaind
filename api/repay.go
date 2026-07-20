package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

func HandleRepay(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	type Request struct {
		LoanID string
	}

	var req Request

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	loan,
	ok :=
		loanStore.Get(
			req.LoanID,
		)

	if !ok {
		http.Error(
			w,
			"loan not found",
			http.StatusBadRequest,
		)
		return
	}

	if loan.Borrower != address {
		http.Error(w, "only the borrower can repay this loan", http.StatusForbidden)
		return
	}
	if loan.Status != core.LoanFunded {
		http.Error(w, "this loan is not open to repay", http.StatusBadRequest)
		return
	}

	symbol := loanSymbol(loan)
	store, err := resolveTokenStore(symbol)
	if err != nil {
		http.Error(w, "unknown loan symbol", http.StatusInternalServerError)
		return
	}
	borrowerSide := resolveBalanceAddress(address, auth.PublicKey, symbol)
	if store.BalanceOf(borrowerSide) < repayAmountFor(loan) {
		http.Error(w, "insufficient balance to repay principal plus interest", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(repayData{LoanID: loan.ID, Mode: "direct"})

	tx := recordTx(auth, loan.Borrower, "", loan.Amount, core.TxRepay, string(data))

	json.NewEncoder(w).Encode(
		map[string]any{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}
