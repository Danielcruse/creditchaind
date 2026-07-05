package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

type RepayLoanRequest struct {
	ID string `json:"id"`
}

func HandleRepayLoan(
	w http.ResponseWriter,
	r *http.Request,
) {
	var req RepayLoanRequest

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
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

	data, _ := json.Marshal(repayData{LoanID: loan.ID, Mode: "p2p"})

	tx := recordTx(auth, loan.Borrower, loan.Lender, loan.Amount, core.TxRepay, string(data))

	json.NewEncoder(w).Encode(
		map[string]any{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}
