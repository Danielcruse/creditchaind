package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// This file is the buy-side counterpart to api/p2p_offers.go's
// EXTERNAL mode - a verified Trading Hub posts a standing bid to BUY
// crypto for real-world payment, and any user who holds that crypto
// can fulfill it. Fulfilling a request creates a real P2POffer under
// the hood (see api/apply.go's applyBuyRequestFulfill) with the
// fulfilling user as the escrow depositor - from there it's the exact
// same seller-confirm/buyer-confirm/moderator-resolve settlement flow
// as any other EXTERNAL P2P offer, just reached via a different entry
// point.

var buyRequestStore = core.NewBuyRequestStore()

type buyRequestCreateData struct {
	WantSymbol string `json:"wantSymbol"`
	WantAmount uint64 `json:"wantAmount"`
	PaySymbol  string `json:"paySymbol"`
	PayAmount  uint64 `json:"payAmount"`
}

// HandleCreateBuyRequest lets a verified Trading Hub post a standing
// bid - only verified hubs, same gate as EXTERNAL P2P offer creation
// (see HandleCreateP2POffer's doc comment for why).
func HandleCreateBuyRequest(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req buyRequestCreateData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !companyStore.IsVerified(address) {
		http.Error(w, "only a verified Trading Hub can post a buy request - apply at /companies.html", http.StatusForbidden)
		return
	}
	if req.WantAmount == 0 || req.PayAmount == 0 {
		http.Error(w, "wantAmount and payAmount must both be positive", http.StatusBadRequest)
		return
	}
	if len(req.WantSymbol) == 0 {
		http.Error(w, "wantSymbol is required", http.StatusBadRequest)
		return
	}
	if len(req.PaySymbol) == 0 || len(req.PaySymbol) > 20 {
		http.Error(w, "paySymbol must be 1-20 characters", http.StatusBadRequest)
		return
	}
	if _, err := resolveTokenStore(req.WantSymbol); err != nil {
		http.Error(w, "unknown wantSymbol", http.StatusNotFound)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxBuyRequestCreate, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleListBuyRequests is a public read of every request regardless
// of status.
func HandleListBuyRequests(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	json.NewEncoder(w).Encode(buyRequestStore.List())
}

type buyRequestActionData struct {
	RequestID string `json:"requestId"`
}

// HandleFulfillBuyRequest is a user (anyone, not just hubs) providing
// the crypto a hub's buy request is bidding for - they must currently
// hold WantAmount of WantSymbol, which locks into escrow exactly like
// creating a normal EXTERNAL P2P offer (see applyBuyRequestFulfill).
func HandleFulfillBuyRequest(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req buyRequestActionData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	request, ok := buyRequestStore.Get(req.RequestID)
	if !ok {
		http.Error(w, "buy request not found", http.StatusNotFound)
		return
	}
	if request.Status != core.BuyRequestOpen {
		http.Error(w, "this buy request is no longer open", http.StatusBadRequest)
		return
	}
	if request.Hub == address {
		http.Error(w, "the hub cannot fulfill its own buy request", http.StatusBadRequest)
		return
	}

	wantStore, err := resolveTokenStore(request.WantSymbol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	side := resolveBalanceAddress(address, auth.PublicKey, request.WantSymbol)
	if wantStore.BalanceOf(side) < request.WantAmount {
		http.Error(w, "insufficient balance to fulfill this request", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxBuyRequestFulfill, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleCancelBuyRequest lets only the hub withdraw an unfulfilled
// request.
func HandleCancelBuyRequest(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req buyRequestActionData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	request, ok := buyRequestStore.Get(req.RequestID)
	if !ok {
		http.Error(w, "buy request not found", http.StatusNotFound)
		return
	}
	if request.Hub != address {
		http.Error(w, "only the hub can cancel this request", http.StatusForbidden)
		return
	}
	if request.Status != core.BuyRequestOpen {
		http.Error(w, "this buy request is no longer open", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxBuyRequestCancel, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}
