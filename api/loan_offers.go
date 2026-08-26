package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

var loanOfferStore = core.NewLoanOfferStore()

// loanOfferEscrowAddress is the same synthetic-holding-address trick
// every other escrow uses (see api/apply.go's poolAddress,
// api/p2p_offers.go's p2pOfferEscrowAddress) - holds exactly the
// lender's undrawn capital for one offer until it's drawn against or
// closed.
func loanOfferEscrowAddress(offerID string) string {
	return "LOANOFFER:" + offerID
}

type loanOfferCreateData struct {
	Symbol      string `json:"symbol"`
	RatePercent uint64 `json:"ratePercent"`
	TotalAmount uint64 `json:"totalAmount"`
	// DurationSeconds is optional - 0 means loans drawn against this
	// offer never get a due date/can never default (see
	// core.LoanOffer.DurationSeconds's doc comment).
	DurationSeconds uint64 `json:"durationSeconds"`
}

// HandleCreateLoanOffer lets a lender post a standing offer to lend any
// resolvable token (CCT, ETHTEST, USDCTEST, REPT, or a developer-
// registered token) at a rate they choose, up to a capital amount they
// lock up front - the missing lender-side half of peer-funded
// borrowing (see core/loan_offer.go's doc comment).
func HandleCreateLoanOffer(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req loanOfferCreateData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if !requireLendingAccess(w, address) {
		return
	}

	if req.TotalAmount == 0 {
		http.Error(w, "totalAmount must be positive", http.StatusBadRequest)
		return
	}

	store, err := resolveTokenStore(req.Symbol)
	if err != nil {
		http.Error(w, "unknown symbol", http.StatusNotFound)
		return
	}

	lenderSide := resolveBalanceAddress(address, auth.PublicKey, req.Symbol)
	if store.BalanceOf(lenderSide) < req.TotalAmount {
		http.Error(w, "insufficient balance to offer that much", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxLoanOfferCreate, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleListLoanOffers is the actual discovery mechanism a borrower
// was missing entirely before - every offer, any status, so a
// borrower can browse and pick one, and a drawn-down/closed offer's
// history stays visible rather than disappearing.
func HandleListLoanOffers(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	json.NewEncoder(w).Encode(loanOfferStore.List())
}

type loanOfferDrawData struct {
	OfferID string `json:"offerId"`
	Amount  uint64 `json:"amount"`
}

// HandleDrawLoanOffer is the borrower choosing how much to draw
// against an open offer, at the rate the lender already set - the
// borrower picks the amount, not the lender.
func HandleDrawLoanOffer(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req loanOfferDrawData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Amount == 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}

	offer, ok := loanOfferStore.Get(req.OfferID)
	if !ok {
		http.Error(w, "offer not found", http.StatusNotFound)
		return
	}
	if offer.Status != core.LoanOfferOpen {
		http.Error(w, "offer is no longer open", http.StatusBadRequest)
		return
	}
	if req.Amount > offer.RemainingAmount {
		http.Error(w, "amount exceeds what's still available on this offer", http.StatusBadRequest)
		return
	}
	if !hasEnoughCollateral(address, req.Amount, time.Now().Unix()) {
		http.Error(w, "insufficient locked collateral - lock staked CCT, oETH, or STETH_CCC1 covering at least this amount first", http.StatusBadRequest)
		return
	}
	// Fast-fail preview, not the security boundary - applyLoanOfferDraw
	// re-checks this for real at apply time (see
	// lenderCanReceiveStethCcc1's doc comment) and refunds the
	// reservation if it fails there instead.
	if symbol, ok := peekCollateralSymbolForLoan(address, req.Amount, time.Now().Unix()); ok && symbol == "STETH_CCC1" && !lenderCanReceiveStethCcc1(offer.Lender) {
		http.Error(w, "this offer's lender hasn't registered a self-custodial ETH address yet, so a STETH_CCC1-collateralized draw against it can't be safely funded right now", http.StatusBadRequest)
		return
	}

	wallet := walletStore.Get(address)
	if limit := core.BorrowLimit(core.TrustDimensionValue(wallet, core.DimensionRepayment)); req.Amount > limit {
		http.Error(w, "borrow limit exceeded", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxLoanOfferDraw, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type loanOfferCloseData struct {
	OfferID string `json:"offerId"`
}

// HandleCloseLoanOffer lets only the lender withdraw whatever capital
// on their offer hasn't been drawn yet.
func HandleCloseLoanOffer(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req loanOfferCloseData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	offer, ok := loanOfferStore.Get(req.OfferID)
	if !ok {
		http.Error(w, "offer not found", http.StatusNotFound)
		return
	}
	if offer.Lender != address {
		http.Error(w, "only the lender can close this offer", http.StatusForbidden)
		return
	}
	if offer.Status != core.LoanOfferOpen {
		http.Error(w, "offer is no longer open", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxLoanOfferClose, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}
