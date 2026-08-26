package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

var loanRequestStore = core.NewLoanRequestStore()

type loanRequestCreateData struct {
	RequestedSymbol    string `json:"requestedSymbol"`
	Amount             uint64 `json:"amount"`
	CollateralSymbol   string `json:"collateralSymbol"`
	CollateralAmount   uint64 `json:"collateralAmount"`
	MaxInterestPercent uint64 `json:"maxInterestPercent"`
	DurationSeconds    uint64 `json:"durationSeconds"`
	Purpose            string `json:"purpose"`
}

// HandleCreateLoanRequest lets a borrower post what they want to
// borrow and against what collateral - the borrower-initiated mirror
// of HandleCreateLoanOffer (see core.LoanRequest's doc comment).
// Unlike the legacy self-funded/offer-draw paths, which always
// reserve collateral exactly equal to the loan amount,
// CollateralAmount here is the borrower's own choice - it must be at
// least Amount (never under-collateralized), but a borrower is free
// to lock more for a healthier starting ratio. This is a deliberate,
// disclosed enhancement over the older paths' fixed 1:1 model, not a
// bug - the older paths stay exactly as they are.
func HandleCreateLoanRequest(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req loanRequestCreateData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Amount == 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}
	if req.CollateralSymbol != "CCT" && req.CollateralSymbol != "OETH" && req.CollateralSymbol != "STETH_CCC1" {
		http.Error(w, "collateralSymbol must be CCT, OETH, or STETH_CCC1", http.StatusBadRequest)
		return
	}
	if !meetsMinimumCollateralRatio(req.CollateralSymbol, req.CollateralAmount, req.Amount, time.Now().Unix()) {
		http.Error(w, "collateralAmount must be at least the borrow amount (in real USD/YEN-e terms for STETH_CCC1 - is a live ETH/USD rate set?)", http.StatusBadRequest)
		return
	}
	if req.MaxInterestPercent == 0 {
		http.Error(w, "maxInterestPercent must be positive", http.StatusBadRequest)
		return
	}
	if len(req.Purpose) > core.MaxLoanRequestPurposeLen {
		http.Error(w, "purpose is too long", http.StatusBadRequest)
		return
	}
	if req.RequestedSymbol == "" {
		req.RequestedSymbol = "YENE"
	}

	if collateralStore.LockedOf(address, req.CollateralSymbol) < req.CollateralAmount {
		http.Error(w, "insufficient locked collateral - lock staked CCT, oETH, or STETH_CCC1 covering at least this amount first", http.StatusBadRequest)
		return
	}

	wallet := walletStore.Get(address)
	if limit := core.BorrowLimit(core.TrustDimensionValue(wallet, core.DimensionRepayment)); req.Amount > limit {
		http.Error(w, "borrow limit exceeded", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxLoanRequestCreate, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleListLoanRequests is the discovery mechanism a lender browses -
// every request regardless of status, same reasoning as
// HandleListLoanOffers (a fulfilled/cancelled request's history stays
// visible rather than disappearing).
func HandleListLoanRequests(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	json.NewEncoder(w).Encode(loanRequestStore.List())
}

type loanRequestCancelData struct {
	RequestID string `json:"requestId"`
}

// HandleCancelLoanRequest lets only the borrower withdraw an OPEN
// request - the reserved collateral is released back at apply time
// (see applyLoanRequestCancel).
func HandleCancelLoanRequest(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req loanRequestCancelData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	request, ok := loanRequestStore.Get(req.RequestID)
	if !ok {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}
	if request.Borrower != address {
		http.Error(w, "only the borrower can cancel this request", http.StatusForbidden)
		return
	}
	if request.Status != core.LoanRequestOpen {
		http.Error(w, "request is no longer open", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxLoanRequestCancel, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}
