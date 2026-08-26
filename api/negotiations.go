package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

var negotiationStore = core.NewNegotiationStore()

// newNegotiationID is a plain random ID - negotiations aren't
// transactions (see core.Negotiation's doc comment on why this state
// isn't part of ApplyBlock), so there's no need for newTxID's
// content-derived scheme, just something unique.
func newNegotiationID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// startNegotiationData covers both negotiation directions - exactly
// one of OfferID (the original borrower-draws-against-a-posted-offer
// direction) or RequestID (the newer lender-funds-a-posted-request
// direction) is ever sent by a real client; the other side's fields
// are simply absent from the JSON body, so decoding a narrower
// direction-specific struct out of a signed envelope that only ever
// contained that direction's fields still round-trips exactly (see
// verifyTxAuth's TxLoanOfferDraw/TxLoanRequestFulfill cases).
type startNegotiationData struct {
	OfferID     string `json:"offerId,omitempty"`
	RequestID   string `json:"requestId,omitempty"`
	Amount      uint64 `json:"amount,omitempty"`
	RatePercent uint64 `json:"ratePercent,omitempty"`
}

// HandleStartNegotiation opens a chat-first negotiation, initiated by
// whichever side is proposing a NEW commitment: for an offer
// (OfferID set), the borrower proposes an amount and their signed
// envelope is captured and held (see core.Negotiation's doc comment)
// so it can be used to submit the real transaction later, once the
// lender actually agrees. For a request (RequestID set), the borrower
// already made their commitment (signed, block-confirmed) when they
// created the LoanRequest itself - here it's the LENDER proposing a
// rate, and nothing is captured/held, since the lender is also the
// one who will sign the actual fund-moving confirmation later (see
// HandleConfirmNegotiation).
func HandleStartNegotiation(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req startNegotiationData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.RequestID != "" {
		if req.RatePercent == 0 {
			http.Error(w, "ratePercent must be positive", http.StatusBadRequest)
			return
		}

		request, ok := loanRequestStore.Get(req.RequestID)
		if !ok {
			http.Error(w, "request not found", http.StatusNotFound)
			return
		}
		if request.Status != core.LoanRequestOpen {
			http.Error(w, "request is no longer open", http.StatusBadRequest)
			return
		}
		if req.RatePercent > request.MaxInterestPercent {
			http.Error(w, "rate exceeds the borrower's maximum", http.StatusBadRequest)
			return
		}

		store, err := resolveTokenStore(request.RequestedSymbol)
		if err != nil {
			http.Error(w, "unknown symbol", http.StatusNotFound)
			return
		}
		lenderSide := resolveBalanceAddress(address, auth.PublicKey, request.RequestedSymbol)
		if store.BalanceOf(lenderSide) < request.Amount {
			http.Error(w, "insufficient balance to fund this request", http.StatusBadRequest)
			return
		}

		n := &core.Negotiation{
			ID:               newNegotiationID(),
			RequestID:        request.ID,
			Lender:           address,
			Borrower:         request.Borrower,
			Symbol:           request.RequestedSymbol,
			Amount:           request.Amount,
			RatePercent:      req.RatePercent,
			Messages:         []core.NegotiationMessage{},
			Status:           core.NegotiationOpen,
			CreatedAt:        time.Now().Unix(),
			ModeratorAddress: moderatorStore.AssignNext(),
			ModerationStatus: core.ModerationPending,
		}
		negotiationStore.Create(n)

		json.NewEncoder(w).Encode(map[string]string{
			"status":        "open",
			"negotiationId": n.ID,
		})
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

	wallet := walletStore.Get(address)
	if limit := core.BorrowLimit(core.TrustDimensionValue(wallet, core.DimensionRepayment)); req.Amount > limit {
		http.Error(w, "borrow limit exceeded", http.StatusBadRequest)
		return
	}

	n := &core.Negotiation{
		ID:           newNegotiationID(),
		OfferID:      offer.ID,
		Lender:       offer.Lender,
		Borrower:     address,
		Symbol:       offer.Symbol,
		Amount:       req.Amount,
		Messages:     []core.NegotiationMessage{},
		BorrowerAuth: auth,
		Status:       core.NegotiationOpen,
		CreatedAt:    time.Now().Unix(),
		// Negotiations are node-local API state, not part of ApplyBlock
		// (see core.Negotiation's doc comment), so unlike the P2P offer
		// assignment this doesn't need to be identical across nodes -
		// it's an oversight assignment, not an economic one.
		ModeratorAddress: moderatorStore.AssignNext(),
		ModerationStatus: core.ModerationPending,
	}
	negotiationStore.Create(n)

	json.NewEncoder(w).Encode(map[string]string{
		"status":        "open",
		"negotiationId": n.ID,
	})
}

// HandleListNegotiations lists negotiations a given address is party
// to (either as lender or borrower) - public read, same as every
// other query in this app; see core.NegotiationMessage's doc comment
// on why this is not a private channel.
func HandleListNegotiations(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")

	all := negotiationStore.List()
	if address == "" {
		json.NewEncoder(w).Encode(all)
		return
	}

	mine := make([]*core.Negotiation, 0, len(all))
	for _, n := range all {
		if n.Lender == address || n.Borrower == address {
			mine = append(mine, n)
		}
	}
	json.NewEncoder(w).Encode(mine)
}

type negotiationMessageData struct {
	NegotiationID string `json:"negotiationId"`
	Text          string `json:"text"`
}

// HandleSendMessage lets either party in a negotiation post a chat
// message - takes effect immediately (not queued as a block-confirmed
// transaction), since a chat message moves no value and doesn't need
// consensus-level guarantees, only proof it really came from one of
// the two participants.
func HandleSendMessage(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req negotiationMessageData

	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if len(req.Text) == 0 || len(req.Text) > 2000 {
		http.Error(w, "message must be 1-2000 characters", http.StatusBadRequest)
		return
	}

	n, ok := negotiationStore.Get(req.NegotiationID)
	if !ok {
		http.Error(w, "negotiation not found", http.StatusNotFound)
		return
	}
	if address != n.Lender && address != n.Borrower {
		http.Error(w, "not a party to this negotiation", http.StatusForbidden)
		return
	}

	ok = negotiationStore.AddMessage(n.ID, core.NegotiationMessage{
		Sender:    address,
		Text:      req.Text,
		Timestamp: time.Now().Unix(),
	})
	if !ok {
		http.Error(w, "negotiation is no longer open", http.StatusBadRequest)
		return
	}

	other := n.Lender
	if address == n.Lender {
		other = n.Borrower
	}
	notifyUser(other, "NEW_MESSAGE", fmt.Sprintf("New message from %s in your negotiation", address), n.ID)

	json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
}

type negotiationActionData struct {
	NegotiationID string `json:"negotiationId"`
}

// HandleConfirmNegotiation is the lender's explicit agreement - the
// one action that actually submits the underlying TxLoanOfferDraw
// transaction, using the borrower's signed request collected back
// when they opened the negotiation. Everything up to this point
// (the offer, the amount, the chat) was ordinary API state; this is
// the single moment the deal actually becomes an on-chain transaction.
func HandleConfirmNegotiation(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req negotiationActionData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	n, ok := negotiationStore.Get(req.NegotiationID)
	if !ok {
		http.Error(w, "negotiation not found", http.StatusNotFound)
		return
	}
	if address != n.Lender {
		http.Error(w, "only the lender can confirm and fund this negotiation", http.StatusForbidden)
		return
	}

	if n.RequestID != "" {
		// Re-check the request still has enough capital/is still open -
		// mirrors the offer-side re-check below, same "never trust
		// submission-time state alone" reasoning. applyLoanRequestFulfill
		// re-checks this again anyway at confirmation time.
		request, ok := loanRequestStore.Get(n.RequestID)
		if !ok || request.Status != core.LoanRequestOpen {
			http.Error(w, "the underlying request is no longer open", http.StatusBadRequest)
			return
		}
		// Fast-fail preview, not the security boundary -
		// applyLoanRequestFulfill re-checks this for real at apply time
		// (see lenderCanReceiveStethCcc1's doc comment).
		if request.CollateralSymbol == "STETH_CCC1" && !lenderCanReceiveStethCcc1(address) {
			http.Error(w, "you haven't registered a self-custodial ETH address yet, so you can't safely fund a STETH_CCC1-collateralized request right now", http.StatusBadRequest)
			return
		}

		if _, ok := negotiationStore.Execute(n.ID); !ok {
			http.Error(w, "negotiation is no longer open", http.StatusBadRequest)
			return
		}

		data, _ := json.Marshal(negotiationActionData{NegotiationID: n.ID})
		tx := recordTx(auth, address, "", 0, core.TxLoanRequestFulfill, string(data))

		notifyUser(n.Borrower, "TRADE_STARTED", fmt.Sprintf("Your loan of %d %s has started", n.Amount, n.Symbol), tx.ID)
		notifyUser(n.Lender, "TRADE_STARTED", fmt.Sprintf("You funded a loan of %d %s", n.Amount, n.Symbol), tx.ID)

		json.NewEncoder(w).Encode(map[string]string{
			"status": "pending",
			"txId":   tx.ID,
		})
		return
	}

	// Re-check the offer still has enough capital left - other draws
	// (via this or another negotiation) may have consumed it since
	// the borrower opened this one. applyLoanOfferDraw re-checks this
	// again anyway at confirmation time, but failing fast here avoids
	// executing a negotiation that's already guaranteed to no-op.
	offer, ok := loanOfferStore.Get(n.OfferID)
	if !ok || offer.Status != core.LoanOfferOpen || n.Amount > offer.RemainingAmount {
		http.Error(w, "the underlying offer no longer has enough capital available", http.StatusBadRequest)
		return
	}
	// Fast-fail preview, not the security boundary - applyLoanOfferDraw
	// re-checks this for real at apply time (see
	// lenderCanReceiveStethCcc1's doc comment).
	if symbol, ok := peekCollateralSymbolForLoan(n.Borrower, n.Amount, time.Now().Unix()); ok && symbol == "STETH_CCC1" && !lenderCanReceiveStethCcc1(n.Lender) {
		http.Error(w, "this offer's lender hasn't registered a self-custodial ETH address yet, so a STETH_CCC1-collateralized draw against it can't be safely funded right now", http.StatusBadRequest)
		return
	}

	if _, ok := negotiationStore.Execute(n.ID); !ok {
		http.Error(w, "negotiation is no longer open", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(loanOfferDrawData{OfferID: n.OfferID, Amount: n.Amount})
	tx := recordTx(n.BorrowerAuth, n.Borrower, "", 0, core.TxLoanOfferDraw, string(data))

	notifyUser(n.Borrower, "TRADE_STARTED", fmt.Sprintf("Your loan of %d %s has started", n.Amount, n.Symbol), tx.ID)
	notifyUser(n.Lender, "TRADE_STARTED", fmt.Sprintf("You funded a loan of %d %s", n.Amount, n.Symbol), tx.ID)

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleCancelNegotiation lets either party withdraw before the
// lender confirms.
func HandleCancelNegotiation(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req negotiationActionData

	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !negotiationStore.Cancel(req.NegotiationID, address) {
		http.Error(w, "negotiation not found, already settled, or you're not a party to it", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
}
