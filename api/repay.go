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

	err :=
		json.NewDecoder(
			r.Body,
		).Decode(
			&req,
		)

	if err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
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

	loan.Status=
		core.LoanRepaid

	collateralStore.Unlock(
		loan.Borrower,
		loan.Amount,
		tokenStore,
	)

	json.NewEncoder(w).Encode(
		map[string]bool{
			"success": true,
		},
	)
profile :=
	creditStore.Get(
		loan.Borrower,
	)

core.RecordLoanRepaid(
	profile,
)
creditHistory.Add(
	loan.Borrower,
	core.CreditEvent{
		Type:   "REPAY",
		Amount: loan.Amount,
	},
)
}