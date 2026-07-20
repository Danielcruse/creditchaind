package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// loanRedeemShortfallData is both the client's signed payload shape
// and the transaction's Data shape - same equality-check pattern
// loanAddCollateralData uses (see api/loan_collateral.go).
type loanRedeemShortfallData struct {
	LoanID string `json:"loanId"`
}

// HandleLoanRedeemShortfall lets a borrower pay off exactly
// Loan.LiquidatedShortfall during the Final Redemption window
// (RiskStage==AwaitingFinalRedemption) to keep their Mining Power -
// deliberately a separate transaction from TxRepay, which computes a
// full repayAmountFor(loan) assuming collateral is still intact. Here
// collateral is already gone (seized at the GracePeriod deadline, see
// advanceLoanLifecycle in api/apply.go) - only the frozen shortfall
// amount is still owed, and reusing TxRepay's full-amount logic would
// double-pay the lender.
func HandleLoanRedeemShortfall(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	var req loanRedeemShortfallData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	loan, ok := loanStore.Get(req.LoanID)
	if !ok {
		http.Error(w, "loan not found", http.StatusNotFound)
		return
	}
	if loan.Borrower != address {
		http.Error(w, "only the borrower can redeem this loan's shortfall", http.StatusForbidden)
		return
	}
	if loan.RiskStage != core.LoanRiskAwaitingFinalRedemption {
		http.Error(w, "this loan is not awaiting final redemption", http.StatusBadRequest)
		return
	}

	symbol := loanSymbol(loan)
	store, err := resolveTokenStore(symbol)
	if err != nil {
		http.Error(w, "unresolvable loan currency", http.StatusInternalServerError)
		return
	}
	borrowerSide := resolveBalanceAddress(address, auth.PublicKey, symbol)
	if store.BalanceOf(borrowerSide) < loan.LiquidatedShortfall {
		http.Error(w, "insufficient balance to redeem the outstanding shortfall", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", loan.LiquidatedShortfall, core.TxLoanRedeemShortfall, string(data))

	json.NewEncoder(w).Encode(
		map[string]any{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}
