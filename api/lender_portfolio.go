package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// lenderPortfolioResponse is computed LIVE from loanStore.Loans on
// every read, deliberately NOT built on top of core.LenderStats/
// GET /lender/stats (see audit/KNOWN_GAPS.md - that store is only
// ever written by the legacy, no-longer-used-by-any-frontend direct
// /loans/fund path; every real lend today goes through
// applyLoanOfferDraw/applyLoanRequestFulfill, neither of which touch
// it, so it reads all-zero for every real lender). InterestEarned
// here is the actually-collected currency amount (repayAmountFor
// minus principal, summed only for loans that reached REPAID), not
// the nominal rate-at-fund-time LenderStats.InterestEarned confuses
// itself with.
type lenderPortfolioResponse struct {
	Address         string `json:"address"`
	ActiveLoans     int    `json:"activeLoans"`
	CompletedLoans  int    `json:"completedLoans"`
	DefaultedLoans  int    `json:"defaultedLoans"`
	AverageYield    uint64 `json:"averageYieldPercent"`
	DefaultExposure uint64 `json:"defaultExposure"`
	Diversification int    `json:"diversification"`
	InterestEarned  uint64 `json:"interestEarned"`
}

func HandleLenderPortfolio(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "missing address", http.StatusBadRequest)
		return
	}

	resp := lenderPortfolioResponse{Address: address}

	borrowers := make(map[string]bool)
	var interestSum, interestCount uint64

	for _, loan := range loanStore.Loans {
		if loan.Lender != address {
			continue
		}

		borrowers[loan.Borrower] = true
		interestSum += loan.Interest
		interestCount++

		switch loan.Status {
		case core.LoanFunded:
			resp.ActiveLoans++
		case core.LoanRepaid:
			resp.CompletedLoans++
			resp.InterestEarned += repayAmountFor(loan) - loan.Amount
		case core.LoanDefault:
			resp.DefaultedLoans++
			resp.DefaultExposure += loan.Amount
		}
	}

	if interestCount > 0 {
		resp.AverageYield = interestSum / interestCount
	}
	resp.Diversification = len(borrowers)

	json.NewEncoder(w).Encode(resp)
}
