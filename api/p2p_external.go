package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// This file covers PaymentMode EXTERNAL settlement only - a trade
// where the buyer pays the seller for the escrowed token outside the
// app (e.g. a bank transfer this chain can't observe). See
// core.SettlementStatus's doc comment for the full state machine:
// accept -> seller claims payment -> buyer agrees (release) or
// disputes (only the assigned moderator can resolve it from there).
// ONCHAIN offers never touch any of this - they still settle
// atomically via HandleAcceptP2POffer, unchanged.

type p2pExternalAcceptData struct {
	OfferID string `json:"offerId"`
}

// HandleAcceptExternalP2POffer is the buyer taking an EXTERNAL-mode
// offer - unlike HandleAcceptP2POffer, no on-chain balance moves yet;
// this just registers the buyer and starts the off-app settlement
// clock (see core.P2POfferStore.BeginExternalSettlement).
func HandleAcceptExternalP2POffer(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req p2pExternalAcceptData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	offer, ok := p2pOfferStore.Get(req.OfferID)
	if !ok {
		http.Error(w, "offer not found", http.StatusNotFound)
		return
	}
	if offer.PaymentMode != core.PaymentModeExternal {
		http.Error(w, "this is an ONCHAIN offer - use /p2p/offers/accept instead", http.StatusBadRequest)
		return
	}
	if offer.Status != core.OfferOpen {
		http.Error(w, "offer is no longer open", http.StatusBadRequest)
		return
	}
	if offer.Creator == address {
		http.Error(w, "cannot accept your own offer", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxP2PExternalAccept, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type p2pSellerConfirmData struct {
	OfferID string `json:"offerId"`
}

// HandleP2PSellerConfirmPaid is the seller (Creator) answering "have
// you received the off-app payment already?" - only they can answer
// this, and only once, while settlement is awaiting it.
func HandleP2PSellerConfirmPaid(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req p2pSellerConfirmData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	offer, ok := p2pOfferStore.Get(req.OfferID)
	if !ok {
		http.Error(w, "offer not found", http.StatusNotFound)
		return
	}
	if offer.Creator != address {
		http.Error(w, "only the seller can confirm payment received", http.StatusForbidden)
		return
	}
	if offer.SettlementStatus != core.SettlementAwaitingPayment {
		http.Error(w, "not awaiting payment confirmation", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxP2PSellerConfirmPaid, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type p2pBuyerMarkPaidData struct {
	OfferID string `json:"offerId"`
}

// HandleP2PBuyerMarkPaid is the buyer's own, non-binding "I've sent
// the payment" signal - visible to the seller in chat, but it does
// NOT advance SettlementStatus (only the seller's own
// HandleP2PSellerConfirmPaid does that). Can only be set once, and
// only while still AWAITING_PAYMENT.
func HandleP2PBuyerMarkPaid(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req p2pBuyerMarkPaidData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	offer, ok := p2pOfferStore.Get(req.OfferID)
	if !ok {
		http.Error(w, "offer not found", http.StatusNotFound)
		return
	}
	if offer.AcceptedBy != address {
		http.Error(w, "only the buyer can mark payment as sent", http.StatusForbidden)
		return
	}
	if offer.SettlementStatus != core.SettlementAwaitingPayment {
		http.Error(w, "not awaiting payment", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxP2PBuyerMarkPaid, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type p2pBuyerConfirmData struct {
	OfferID string `json:"offerId"`
	Agree   bool   `json:"agree"`
}

// HandleP2PBuyerConfirmPaid is the buyer answering "do you confirm
// the seller's result?" - agree releases escrow to them immediately,
// disagree opens a dispute that only the assigned moderator can
// resolve.
func HandleP2PBuyerConfirmPaid(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req p2pBuyerConfirmData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	offer, ok := p2pOfferStore.Get(req.OfferID)
	if !ok {
		http.Error(w, "offer not found", http.StatusNotFound)
		return
	}
	if offer.AcceptedBy != address {
		http.Error(w, "only the buyer can answer this", http.StatusForbidden)
		return
	}
	if offer.SettlementStatus != core.SettlementSellerConfirmed {
		http.Error(w, "the seller hasn't confirmed payment received yet", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxP2PBuyerConfirmPaid, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type p2pModeratorResolveData struct {
	OfferID string `json:"offerId"`
	Release bool   `json:"release"`
}

// HandleResolveP2PDispute is the assigned moderator's final call once
// the buyer has disputed the seller's claim - release=true sides with
// the buyer, release=false sides with the seller. This is a disclosed
// human trust decision (see core/moderator.go's doc comment), not a
// cryptographic verification of what happened off-app.
func HandleResolveP2PDispute(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req p2pModeratorResolveData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	offer, ok := p2pOfferStore.Get(req.OfferID)
	if !ok {
		http.Error(w, "offer not found", http.StatusNotFound)
		return
	}
	if offer.ModeratorAddress != address {
		http.Error(w, "this dispute wasn't assigned to you", http.StatusForbidden)
		return
	}
	if offer.SettlementStatus != core.SettlementDisputed {
		http.Error(w, "this offer isn't in dispute", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxP2PModeratorResolve, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type p2pMessageData struct {
	OfferID string `json:"offerId"`
	Text    string `json:"text"`
}

// HandleP2PMessage lets the creator, acceptor, or assigned moderator
// post a chat message on an EXTERNAL trade - takes effect immediately
// (not queued as a block-confirmed transaction), same reasoning as
// HandleSendMessage for loan negotiations: a chat message moves no
// value and doesn't need consensus-level guarantees.
func HandleP2PMessage(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req p2pMessageData

	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if len(req.Text) == 0 || len(req.Text) > 2000 {
		http.Error(w, "message must be 1-2000 characters", http.StatusBadRequest)
		return
	}

	offer, ok := p2pOfferStore.Get(req.OfferID)
	if !ok {
		http.Error(w, "offer not found", http.StatusNotFound)
		return
	}
	if address != offer.Creator && address != offer.AcceptedBy && address != offer.ModeratorAddress {
		http.Error(w, "not a party to this trade", http.StatusForbidden)
		return
	}

	ok = p2pOfferStore.AddMessage(offer.ID, core.NegotiationMessage{
		Sender:    address,
		Text:      req.Text,
		Timestamp: time.Now().Unix(),
	})
	if !ok {
		http.Error(w, "this trade's chat isn't open", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
}
