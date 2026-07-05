package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var hubRateStore = core.NewHubRateStore()

type hubRateSetData struct {
	// Hub is which hub this rate belongs to - omitted (or equal to the
	// signer's own address) when a hub posts its own rate directly.
	// Set to the hub's address when a delegated worker posts on the
	// hub's behalf (see core.HubWorker's doc comment) - the signer
	// must then be an active worker of that hub, checked independently
	// at both submission (here) and confirmation (applyHubRateSet)
	// time, never trusted from the client alone.
	Hub        string `json:"hub"`
	Symbol     string `json:"symbol"`
	FiatSymbol string `json:"fiatSymbol"`
	SellRate   uint64 `json:"sellRate"`
	BuyRate    uint64 `json:"buyRate"`
	Active     bool   `json:"active"`
	// Mode is "" / "OFFAPP" (default, FiatSymbol is a free-text
	// off-app label) or "ONCHAIN" (FiatSymbol must be a real,
	// resolvable native token symbol, different from Symbol).
	Mode string `json:"mode"`
}

// authorizedHubActor resolves which hub `req.Hub` (defaulting to the
// signer's own address) actually is, and whether address may act on
// its behalf - either because address IS that hub, or because it's
// one of that hub's currently-active delegated workers (see
// core.HubWorker's doc comment). Shared by HandleSetHubRate and
// applyHubRateSet so submission-time and confirmation-time
// authorization can never drift apart.
func authorizedHubActor(reqHub, address string) (hub string, ok bool) {
	hub = reqHub
	if hub == "" {
		hub = address
	}
	if address == hub {
		return hub, companyStore.IsVerified(hub)
	}
	return hub, companyStore.IsVerified(hub) && hubWorkerStore.IsActiveWorker(hub, address)
}

// HandleSetHubRate lets a verified Trading Hub - or one of its
// delegated workers, posting on its behalf - post or update its
// standing rate for a symbol - see core.HubRate's doc comment for why
// this replaces re-listing a fixed-amount offer every time a hub's
// price or inventory changes.
func HandleSetHubRate(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubRateSetData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if _, ok := authorizedHubActor(req.Hub, address); !ok {
		http.Error(w, "only a verified Trading Hub or one of its delegated workers can post a rate - apply at /companies.html", http.StatusForbidden)
		return
	}
	if _, err := resolveTokenStore(req.Symbol); err != nil {
		http.Error(w, "unknown symbol", http.StatusNotFound)
		return
	}
	if len(req.FiatSymbol) == 0 || len(req.FiatSymbol) > 20 {
		http.Error(w, "fiatSymbol must be 1-20 characters", http.StatusBadRequest)
		return
	}
	if req.Mode == "ONCHAIN" {
		if req.FiatSymbol == req.Symbol {
			http.Error(w, "counter token must differ from the symbol being quoted", http.StatusBadRequest)
			return
		}
		if _, err := resolveTokenStore(req.FiatSymbol); err != nil {
			http.Error(w, "unknown counter token symbol", http.StatusNotFound)
			return
		}
	} else if req.Mode != "" && req.Mode != "OFFAPP" {
		http.Error(w, "mode must be 'OFFAPP' or 'ONCHAIN'", http.StatusBadRequest)
		return
	}
	if req.SellRate == 0 && req.BuyRate == 0 {
		http.Error(w, "at least one of sellRate/buyRate must be positive", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxHubRateSet, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// hubRateView adds the hub's live, spendable balance of Symbol on top
// of the raw core.HubRate - this is what a buyer's UI needs to know
// the actual current range (see core.HubRate's doc comment: there is
// no separate fixed cap, the range IS this balance), computed fresh at
// read time rather than trusting anything stored on the rate itself.
type hubRateView struct {
	*core.HubRate
	HubSymbolBalance uint64
	// HubCounterBalance is the hub's live balance of FiatSymbol - only
	// meaningful (non-zero-by-construction) when Mode is ONCHAIN, since
	// an OFFAPP rate's FiatSymbol isn't a real token to have a balance
	// of at all.
	HubCounterBalance uint64 `json:"HubCounterBalance,omitempty"`
}

// HandleListHubRates lists every posted rate, optionally filtered to
// one hub (?hub=address) for a hub's own profile page.
func HandleListHubRates(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	filterHub := r.URL.Query().Get("hub")

	rates := hubRateStore.List()
	views := make([]hubRateView, 0, len(rates))
	for _, rate := range rates {
		if filterHub != "" && rate.Hub != filterHub {
			continue
		}
		var bal uint64
		if store, err := resolveTokenStore(rate.Symbol); err == nil {
			hubSide := resolveBalanceAddress(rate.Hub, rate.HubPublicKey, rate.Symbol)
			bal = store.BalanceOf(hubSide)
		}
		view := hubRateView{HubRate: rate, HubSymbolBalance: bal}
		if rate.IsOnChain() {
			if counterStore, err := resolveTokenStore(rate.FiatSymbol); err == nil {
				hubCounterSide := resolveBalanceAddress(rate.Hub, rate.HubPublicKey, rate.FiatSymbol)
				view.HubCounterBalance = counterStore.BalanceOf(hubCounterSide)
			}
		}
		views = append(views, view)
	}

	json.NewEncoder(w).Encode(views)
}

type hubTradeRequestData struct {
	Hub       string `json:"hub"`
	Symbol    string `json:"symbol"`
	Amount    uint64 `json:"amount"`
	Direction string `json:"direction"` // "BUY_FROM_HUB" | "SELL_TO_HUB"
}

// HandleRequestHubTrade is a regular user instantiating an actual
// trade against a hub's posted rate, for whatever amount they choose -
// the fast-fail check here only sanity-checks what's cheap to check
// before confirmation (rate exists/active, direction valid); the real
// live-balance cap is re-checked at apply time (see
// applyHubTradeRequest), same "never trust submission-time state"
// pattern every other handler in this codebase already follows.
func HandleRequestHubTrade(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubTradeRequestData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Amount == 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}
	if req.Hub == address {
		http.Error(w, "cannot trade against your own hub rate", http.StatusBadRequest)
		return
	}
	if req.Direction != "BUY_FROM_HUB" && req.Direction != "SELL_TO_HUB" {
		http.Error(w, "direction must be 'BUY_FROM_HUB' or 'SELL_TO_HUB'", http.StatusBadRequest)
		return
	}

	rate, ok := hubRateStore.Get(req.Hub, req.Symbol)
	if !ok || !rate.Active {
		http.Error(w, "no active rate posted for that hub/symbol", http.StatusNotFound)
		return
	}
	if !companyStore.IsVerified(req.Hub) {
		http.Error(w, "hub is no longer a verified Trading Hub", http.StatusForbidden)
		return
	}

	store, err := resolveTokenStore(req.Symbol)
	if err != nil {
		http.Error(w, "unknown symbol", http.StatusNotFound)
		return
	}

	var counterStore *core.TokenStore
	if rate.IsOnChain() {
		counterStore, err = resolveTokenStore(rate.FiatSymbol)
		if err != nil {
			http.Error(w, "hub's counter token is no longer resolvable", http.StatusInternalServerError)
			return
		}
	}

	switch req.Direction {
	case "BUY_FROM_HUB":
		if rate.SellRate == 0 {
			http.Error(w, "this hub does not sell that symbol", http.StatusBadRequest)
			return
		}
		hubSide := resolveBalanceAddress(rate.Hub, rate.HubPublicKey, req.Symbol)
		if store.BalanceOf(hubSide) < req.Amount {
			http.Error(w, "requested amount exceeds the hub's current available balance", http.StatusBadRequest)
			return
		}
		// ONCHAIN mode also moves the counter token from the requester
		// to the hub atomically at confirmation time - fast-fail here
		// too, same as every other balance check in this handler.
		if rate.IsOnChain() {
			userCounterSide := resolveBalanceAddress(address, auth.PublicKey, rate.FiatSymbol)
			if counterStore.BalanceOf(userCounterSide) < req.Amount*rate.SellRate {
				http.Error(w, fmt.Sprintf("insufficient %s balance to pay for this trade", rate.FiatSymbol), http.StatusBadRequest)
				return
			}
		}

	case "SELL_TO_HUB":
		if rate.BuyRate == 0 {
			http.Error(w, "this hub does not buy that symbol", http.StatusBadRequest)
			return
		}
		userSide := resolveBalanceAddress(address, auth.PublicKey, req.Symbol)
		if store.BalanceOf(userSide) < req.Amount {
			http.Error(w, fmt.Sprintf("insufficient %s balance", req.Symbol), http.StatusBadRequest)
			return
		}
		if rate.IsOnChain() {
			hubCounterSide := resolveBalanceAddress(rate.Hub, rate.HubPublicKey, rate.FiatSymbol)
			if counterStore.BalanceOf(hubCounterSide) < req.Amount*rate.BuyRate {
				http.Error(w, "hub does not currently have enough balance to pay for this trade", http.StatusBadRequest)
				return
			}
		}
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxHubTradeRequest, string(data))

	// Notify the hub right away, at submission time - this is purely
	// informational (like Negotiation/P2POffer chat messages, see
	// core.NegotiationMessage's doc comment), not an economic event, so
	// it doesn't need to wait for block confirmation the way the trade
	// itself does.
	verb := "buy"
	if req.Direction == "SELL_TO_HUB" {
		verb = "sell"
	}
	notificationStore.Add(&core.HubNotification{
		ID:         tx.ID,
		HubAddress: req.Hub,
		Type:       "TRADE_REQUEST",
		Message:    fmt.Sprintf("%s requested to %s %d %s", address, verb, req.Amount, req.Symbol),
		OfferID:    tx.ID,
		CreatedAt:  tx.Timestamp,
	})

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
		// offerId doubles as this trade's P2POffer ID (see
		// applyHubTradeRequest - it reuses tx.ID as the offer ID,
		// exactly like applyBuyRequestFulfill does), so the client can
		// jump straight to the resulting trade's chat/confirm view once
		// the block confirming this lands, without a separate lookup.
		"offerId": tx.ID,
	})
}
