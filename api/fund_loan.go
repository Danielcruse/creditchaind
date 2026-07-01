package api

import (
	"encoding/json"
	"net/http"
)

type FundLoanRequest struct {
	ID     string `json:"id"`
	Lender string `json:"lender"`
}

func HandleFundLoan(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	var req FundLoanRequest

	err :=
		json.NewDecoder(
			r.Body,
		).Decode(
			&req,
		)

	if err != nil {
		http.Error(
			w,
			"Invalid JSON",
			http.StatusBadRequest,
		)
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

	creditEngine.FundLoan(
		loan,
		req.Lender,
	)

	stats :=
		lenderStore.Get(
			req.Lender,
		)

	stats.LoansFunded++

	stats.AmountLent +=
		loan.Amount

	stats.InterestEarned +=
		loan.Interest

	json.NewEncoder(w).Encode(
		loan,
	)
}