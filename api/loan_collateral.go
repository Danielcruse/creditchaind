package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// loanAddCollateralData is both the client's signed payload shape and
// the transaction's Data shape - same equality-check pattern
// lockCollateralData uses (see api/lock_collateral.go).
type loanAddCollateralData struct {
	LoanID string `json:"loanId"`
	Amount uint64 `json:"amount"`
}

// HandleLoanAddCollateral lets a borrower top up a FUNDED, OETH-
// collateralized loan's earmarked collateral - releasing from their
// already-locked collateral pool (see api/collateral.go) into that
// specific loan's CollateralAmount, mirroring how
// reserveCollateralForLoan earmarks collateral against a brand-new
// loan, just against an existing one instead. Only meaningful for
// OETH collateral (CCT has no live price, see collateralValueInYen -
// adding more of it can't change a health ratio that's never computed
// in the first place), but not hard-rejected for CCT since it's still
// harmless bookkeeping (more collateral is never a bad thing, even if
// this phase's liquidation engine won't use it to compute risk).
func HandleLoanAddCollateral(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	var req loanAddCollateralData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Amount == 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}

	loan, ok := loanStore.Get(req.LoanID)
	if !ok {
		http.Error(w, "loan not found", http.StatusNotFound)
		return
	}
	if loan.Borrower != address {
		http.Error(w, "only the borrower can add collateral to this loan", http.StatusForbidden)
		return
	}
	if loan.Status != core.LoanFunded {
		http.Error(w, "this loan is not open to add collateral to", http.StatusBadRequest)
		return
	}
	if loan.CollateralSymbol == "" {
		http.Error(w, "this loan has no collateral to add to", http.StatusBadRequest)
		return
	}
	if collateralStore.LockedOf(address, loan.CollateralSymbol) < req.Amount {
		http.Error(w, "insufficient locked collateral - lock more first", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", req.Amount, core.TxLoanAddCollateral, string(data))

	json.NewEncoder(w).Encode(
		map[string]any{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}

// loanHealthResponse deliberately separates "we have no price data"
// (PriceAvailable=false) from "collateral is worth zero" - the
// frontend must never render a fabricated ratio when there's simply
// no live quote (e.g. CCT collateral, or an OETH/USDCTEST pool with
// no reserves yet). CollateralValue/RatioPercent are only meaningful
// when PriceAvailable is true.
type loanHealthResponse struct {
	LoanID           string `json:"loanId"`
	CollateralSymbol string `json:"collateralSymbol"`
	CollateralAmount uint64 `json:"collateralAmount"`
	Owed             uint64 `json:"owed"`
	PriceAvailable   bool   `json:"priceAvailable"`
	CollateralValue  uint64 `json:"collateralValue,omitempty"`
	RatioPercent     uint64 `json:"ratioPercent,omitempty"`
	AtRisk           bool   `json:"atRisk"`
}

// HandleLoanHealth reports a loan's current collateral health -
// exactly the same collateralValueInYen/LiquidationThresholdPercent
// math processCollateralLiquidations uses (see api/apply.go), just
// read-only, so the frontend can show "at risk" before the sweep
// actually fires rather than only after the fact.
func HandleLoanHealth(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	loanID := r.URL.Query().Get("loanId")

	loan, ok := loanStore.Get(loanID)
	if !ok {
		http.Error(w, "loan not found", http.StatusNotFound)
		return
	}

	resp := loanHealthResponse{
		LoanID:           loan.ID,
		CollateralSymbol: loan.CollateralSymbol,
		CollateralAmount: loan.CollateralAmount,
		Owed:             repayAmountFor(loan),
	}

	if collateralValue, ok := collateralValueInYen(loan.CollateralSymbol, loan.CollateralAmount); ok {
		resp.PriceAvailable = true
		resp.CollateralValue = collateralValue
		if resp.Owed > 0 {
			resp.RatioPercent = collateralValue * 100 / resp.Owed
			resp.AtRisk = collateralValue*100 < core.LiquidationThresholdPercent*resp.Owed
		}
	}

	json.NewEncoder(w).Encode(resp)
}
