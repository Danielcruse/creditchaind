package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)


type CreateLoanRequest struct {
	ID       string `json:"id"`
	Borrower string `json:"borrower"`
	Amount   uint64 `json:"amount"`
	Interest uint64 `json:"interest"`
}

func HandleCreateLoan(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		http.Error(
			w,
			"Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	var req CreateLoanRequest

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(
			w,
			"Invalid JSON",
			http.StatusBadRequest,
		)
		return
	}

	loan := &core.Loan{
		ID:       req.ID,
		Borrower: req.Borrower,
		Amount:   req.Amount,
		Interest: req.Interest,
		Status:   core.LoanPending,
	}

	loanStore.Add(loan)

	json.NewEncoder(w).Encode(loan)
}

func HandleGetLoans(
	w http.ResponseWriter,
	r *http.Request,
) {
	json.NewEncoder(w).Encode(
		loanStore.Loans,
	)
}