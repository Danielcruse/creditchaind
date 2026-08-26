package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var disputeFaultChoiceStore = core.NewDisputeFaultChoiceStore()

type disputeFaultChoiceData struct {
	OfferID string `json:"offerId"`
}

// HandleDisputeFaultChoicePay lets the at-fault party of a refund-
// resolved dispute pay the moderator's would-be fee directly, avoiding
// the reputation penalty - see core.DisputeFaultChoice's own doc
// comment for the full mechanic.
func HandleDisputeFaultChoicePay(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req disputeFaultChoiceData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	choice, ok := disputeFaultChoiceStore.Get(req.OfferID)
	if !ok || choice.AtFaultAddress != address {
		http.Error(w, "no pending dispute fault choice found for you on this trade", http.StatusNotFound)
		return
	}
	if choice.Status != core.DisputeFaultChoicePending {
		http.Error(w, "this choice has already been resolved", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxDisputeFaultPay, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleDisputeFaultChoiceDecline lets the at-fault party explicitly
// accept the reputation penalty instead of paying - resolves
// immediately, same as paying, rather than leaving them waiting out
// the full window if they already know what they want to do.
func HandleDisputeFaultChoiceDecline(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req disputeFaultChoiceData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	choice, ok := disputeFaultChoiceStore.Get(req.OfferID)
	if !ok || choice.AtFaultAddress != address {
		http.Error(w, "no pending dispute fault choice found for you on this trade", http.StatusNotFound)
		return
	}
	if choice.Status != core.DisputeFaultChoicePending {
		http.Error(w, "this choice has already been resolved", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxDisputeFaultDecline, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type disputeFaultChoiceInfo struct {
	OfferID            string `json:"offerId"`
	AtFaultAddress     string `json:"atFaultAddress"`
	ResolvingModerator string `json:"resolvingModerator"`
	Symbol             string `json:"symbol"`
	FeeAmount          uint64 `json:"feeAmount"`
	CreatedAt          int64  `json:"createdAt"`
	Deadline           int64  `json:"deadline"`
	Status             string `json:"status"`
}

// HandleGetDisputeFaultChoice is a public read (same "nothing here is
// confidential, everyone in the dispute already knows the outcome"
// reasoning as the rest of the P2P dispute machinery) - what the
// at-fault party's own UI polls to show the pending choice, amount,
// and countdown.
func HandleGetDisputeFaultChoice(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	offerID := r.URL.Query().Get("offerId")
	if offerID == "" {
		http.Error(w, "offerId required", http.StatusBadRequest)
		return
	}

	choice, ok := disputeFaultChoiceStore.Get(offerID)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{"exists": false})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"exists": true,
		"choice": disputeFaultChoiceInfo{
			OfferID: choice.OfferID, AtFaultAddress: choice.AtFaultAddress,
			ResolvingModerator: choice.ResolvingModerator, Symbol: choice.Symbol,
			FeeAmount: choice.FeeAmount, CreatedAt: choice.CreatedAt,
			Deadline: choice.Deadline, Status: string(choice.Status),
		},
	})
}
