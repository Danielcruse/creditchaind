package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var orderBookStore = core.NewOrderBookStore()
var tradeHistoryStore = core.NewTradeHistoryStore()

// orderBookSymbols is deliberately narrow - the order book replaces
// the AMM as CCT's pricing mechanism only for the two native
// "imported" tokens (see the plan's Context section for why).
// Developer-registered custom tokens keep trading through the
// existing liquidity pool (see api/token_registry.go) - broadening
// this is a separate, later decision, not something this type quietly
// expands into on its own.
var orderBookSymbols = map[string]bool{"ETHTEST": true, "USDCTEST": true}

type orderPlaceData struct {
	Symbol string `json:"symbol"`
	Side   string `json:"side"`
	Price  uint64 `json:"price"`
	Amount uint64 `json:"amount"`
}

// HandlePlaceOrder submits a new limit order - escrowed and matched
// (or rested) at block-confirmation time (applyOrderPlace), never
// here. The balance check below is a fast-fail preview only; the real
// escrow deduction is re-checked and only actually taken at apply
// time, same "never trust submission-time state alone" convention
// every other fee/escrow handler in this codebase already follows.
func HandlePlaceOrder(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req orderPlaceData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !orderBookSymbols[req.Symbol] {
		http.Error(w, "symbol must be ETHTEST or USDCTEST", http.StatusBadRequest)
		return
	}
	side := core.OrderSide(req.Side)
	if side != core.OrderBuy && side != core.OrderSell {
		http.Error(w, "side must be BUY or SELL", http.StatusBadRequest)
		return
	}
	if req.Price == 0 || req.Amount == 0 {
		http.Error(w, "price and amount must both be positive", http.StatusBadRequest)
		return
	}

	if side == core.OrderSell {
		store, err := resolveTokenStore(req.Symbol)
		if err != nil {
			http.Error(w, "unknown symbol", http.StatusNotFound)
			return
		}
		sellSide := resolveBalanceAddress(address, auth.PublicKey, req.Symbol)
		if store.BalanceOf(sellSide) < req.Amount {
			http.Error(w, fmt.Sprintf("insufficient %s balance to sell that amount", req.Symbol), http.StatusBadRequest)
			return
		}
	} else {
		cost := core.OrderCCTCost(req.Amount, req.Price)
		if tokenStore.BalanceOf(address) < cost {
			http.Error(w, fmt.Sprintf("insufficient CCT - this order needs %d CCT at your limit price", cost), http.StatusBadRequest)
			return
		}
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxOrderPlace, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type orderCancelData struct {
	OrderID string `json:"orderId"`
}

// HandleCancelOrder cancels a still-open order, releasing whatever's
// left of its escrow back to its owner (applyOrderCancel).
func HandleCancelOrder(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req orderCancelData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	order, ok := orderBookStore.Get(req.OrderID)
	if !ok || order.Owner != address || order.Status != core.OrderOpen {
		http.Error(w, "no cancellable open order with that ID for this address", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxOrderCancel, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleListOrders is the visible book depth for symbol - every
// currently OPEN order, any owner. The frontend splits this into
// bids/asks client-side by Side.
func HandleListOrders(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	symbol := r.URL.Query().Get("symbol")
	json.NewEncoder(w).Encode(orderBookStore.OpenOrders(symbol))
}

// HandleMyOrders is one address's own orders (any status), optionally
// filtered to one symbol - "My Open Orders" plus history.
func HandleMyOrders(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	symbol := r.URL.Query().Get("symbol")
	json.NewEncoder(w).Encode(orderBookStore.OrdersByOwner(address, symbol))
}

// HandleListTrades is the recent trade tape for symbol.
func HandleListTrades(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	symbol := r.URL.Query().Get("symbol")
	json.NewEncoder(w).Encode(tradeHistoryStore.Get(symbol))
}
