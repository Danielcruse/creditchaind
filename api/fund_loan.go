package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

type FundLoanRequest struct {
	ID string `json:"id"`
}

func HandleFundLoan(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	var req FundLoanRequest

	auth, lender, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	loan, ok :=
		loanStore.Get(
			req.ID,
		)

	if !ok {
		http.Error(
			w,
			"Loan not found",
			http.StatusNotFound,
		)
		return
	}

	data, _ := json.Marshal(lendData{LoanID: loan.ID})

	tx := recordTx(auth, lender, loan.Borrower, loan.Amount, core.TxLend, string(data))

	json.NewEncoder(w).Encode(
		map[string]any{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}
