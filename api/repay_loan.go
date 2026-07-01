package api

import (
	"encoding/json"
	"net/http"
)

type RepayLoanRequest struct {
	ID string `json:"id"`
}

func HandleRepayLoan(
	w http.ResponseWriter,
	r *http.Request,
) {
	var req RepayLoanRequest

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(
			w,
			"Invalid JSON",
			http.StatusBadRequest,
		)
		return
	}

	loan, ok := loanStore.Get(req.ID)
	if !ok {
		http.Error(
			w,
			"Loan not found",
			http.StatusNotFound,
		)
		return
	}

	creditEngine.RepayLoan(loan)

	json.NewEncoder(w).Encode(loan)
}