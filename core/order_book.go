package core

import "sort"

// OrderSide is expressed relative to the base token (Order.Symbol),
// the same way a real exchange labels a BTC/USD pair - BUY means
// paying CCT to acquire Symbol, SELL means giving up Symbol to
// receive CCT. CCT is always the quote currency; it never appears as
// its own Symbol in this book.
type OrderSide string

const (
	OrderBuy  OrderSide = "BUY"
	OrderSell OrderSide = "SELL"
)

type OrderStatus string

const (
	OrderOpen      OrderStatus = "OPEN"
	OrderFilled    OrderStatus = "FILLED"
	OrderCancelled OrderStatus = "CANCELLED"
)

// OrderCCTCost is how much CCT a BUY order of amount at price actually
// costs - the same priceScale-based math as the pool-reserve price
// (core/price_history.go), exported here so api/order_book.go never
// needs to know priceScale's value directly, only that this is "the"
// conversion between a scaled Price and a real CCT amount.
func OrderCCTCost(amount, price uint64) uint64 {
	return amount * price / priceScale
}

// Order is a resting (or once-resting) limit order. Price is CCT per
// whole unit of Symbol, scaled by the same priceScale
// core/price_history.go's pool-reserve price already uses (1e6) - so
// order-book prices and the existing price-history/chart numbers stay
// in one consistent unit with no conversion needed anywhere.
//
// Never deleted once created, only status-transitioned - same
// "history stays visible" convention P2POffer/LoanOffer already
// follow. A FILLED or CANCELLED order simply stops being part of the
// visible book depth (see OrderBookStore.OpenOrders).
type Order struct {
	ID        string
	Owner     string
	PublicKey string
	Symbol    string
	Side      OrderSide
	Price     uint64
	Amount    uint64
	Remaining uint64
	Status    OrderStatus
	CreatedAt int64
}

// TradeRecord is one completed match - the unit both the trade tape
// and price history are built from. BuyOrderID/SellOrderID (as
// opposed to just Buyer/Seller addresses) are what let the caller
// (api/apply.go's applyOrderPlace) find each leg's own escrow address
// to settle from - an address alone isn't enough, since the same
// owner can have multiple simultaneous orders on the same symbol.
type TradeRecord struct {
	ID          string
	Symbol      string
	Price       uint64
	Amount      uint64
	Buyer       string
	Seller      string
	BuyOrderID  string
	SellOrderID string
	Timestamp   int64
}

// OrderBookStore holds every order ever placed. No mutex here
// deliberately - like every other store in this package (see
// core/p2p_offer.go), concurrency safety is the caller's
// responsibility (ApplyBlock already serializes all state mutation
// through a single goroutine).
type OrderBookStore struct {
	Orders map[string]*Order
}

func NewOrderBookStore() *OrderBookStore {
	return &OrderBookStore{Orders: make(map[string]*Order)}
}

func (s *OrderBookStore) Get(id string) (*Order, bool) {
	o, ok := s.Orders[id]
	return o, ok
}

// Create adds a brand new order to the book, already having been
// through Match (see Match's doc comment) - Remaining may already be
// less than Amount, or the order may already be OrderFilled, if
// Match consumed some or all of it against resting orders before this
// is called.
func (s *OrderBookStore) Create(o *Order) {
	s.Orders[o.ID] = o
}

// Cancel marks an OPEN order CANCELLED, returning it (with whatever
// Remaining was left, for the caller to release from escrow) if it
// was actually cancellable - false if it doesn't exist, isn't owned
// by the caller, or has already reached a terminal status.
func (s *OrderBookStore) Cancel(id, owner string) (*Order, bool) {
	o, ok := s.Orders[id]
	if !ok || o.Owner != owner || o.Status != OrderOpen {
		return nil, false
	}
	o.Status = OrderCancelled
	return o, true
}

// OpenOrders returns every OrderOpen order for symbol - the visible
// book depth. Sorted deterministically (price then time then ID, see
// sortForMatching's doc comment) purely so two nodes/two callers see
// the same ordering, not because sort order matters for correctness
// here the way it does inside Match.
func (s *OrderBookStore) OpenOrders(symbol string) []*Order {
	var out []*Order
	for _, o := range s.Orders {
		if o.Symbol == symbol && o.Status == OrderOpen {
			out = append(out, o)
		}
	}
	sortForMatching(out, OrderBuy) // arbitrary side for a stable, deterministic display order
	return out
}

// OrdersByOwner returns every order (any status) an address has ever
// placed for symbol - "" symbol means every symbol.
func (s *OrderBookStore) OrdersByOwner(owner, symbol string) []*Order {
	var out []*Order
	for _, o := range s.Orders {
		if o.Owner == owner && (symbol == "" || o.Symbol == symbol) {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// sortForMatching orders candidates by price-time priority for a
// taker on the given side, with a final ID tiebreaker - the exact
// same "never rely on Go map iteration order" determinism convention
// applyBlockReward's delegator payout already uses (sorted slice, not
// a map range) - every node applying the same sequence of orders MUST
// reach an identical match result, so this ordering can never depend
// on anything non-reproducible.
//
// takerSide is the side of the order DOING the matching (the
// aggressor) - it determines which resting price is "best": a BUY
// taker wants the cheapest asks first, a SELL taker wants the
// highest bids first.
func sortForMatching(orders []*Order, takerSide OrderSide) {
	sort.Slice(orders, func(i, j int) bool {
		a, b := orders[i], orders[j]
		if a.Price != b.Price {
			if takerSide == OrderBuy {
				return a.Price < b.Price // cheapest ask first
			}
			return a.Price > b.Price // highest bid first
		}
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt < b.CreatedAt
		}
		return a.ID < b.ID
	})
}

// crosses reports whether a taker at takerPrice would actually trade
// against a resting order at restingPrice.
func crosses(takerSide OrderSide, takerPrice, restingPrice uint64) bool {
	if takerSide == OrderBuy {
		return takerPrice >= restingPrice
	}
	return takerPrice <= restingPrice
}

// Match runs taker against the current resting book for taker.Symbol
// on the opposite side, price-time priority, filling at each resting
// order's own price (maker sets the price - standard convention).
// Mutates taker.Remaining/Status and every resting order it touches
// directly in the store; returns the trades produced so the caller
// (api/apply.go's applyOrderPlace) can move real token balances for
// each leg - this function only ever touches bookkeeping, never a
// token balance.
//
// Self-trading is skipped entirely (an order never matches against
// the same Owner's own resting order) - same "can't be your own
// counterparty" guard P2POffer's accept flow already enforces.
//
// Caller is responsible for calling Create(taker) afterward so it's
// recorded (as a new resting order if Remaining > 0, or purely for
// history if it fully filled) - Match itself never adds taker to
// s.Orders, only resting orders that were already there.
func (s *OrderBookStore) Match(taker *Order) []TradeRecord {
	opposite := OrderSell
	if taker.Side == OrderSell {
		opposite = OrderBuy
	}

	var candidates []*Order
	for _, o := range s.Orders {
		if o.Symbol == taker.Symbol && o.Status == OrderOpen && o.Side == opposite && o.Owner != taker.Owner {
			candidates = append(candidates, o)
		}
	}
	sortForMatching(candidates, taker.Side)

	var trades []TradeRecord
	for _, resting := range candidates {
		if taker.Remaining == 0 {
			break
		}
		if !crosses(taker.Side, taker.Price, resting.Price) {
			break // sorted best-first, so nothing further down the list can cross either
		}

		fill := taker.Remaining
		if resting.Remaining < fill {
			fill = resting.Remaining
		}
		if fill == 0 {
			continue
		}

		taker.Remaining -= fill
		resting.Remaining -= fill
		if resting.Remaining == 0 {
			resting.Status = OrderFilled
		}

		buyer, seller := taker.Owner, resting.Owner
		buyOrderID, sellOrderID := taker.ID, resting.ID
		if taker.Side == OrderSell {
			buyer, seller = resting.Owner, taker.Owner
			buyOrderID, sellOrderID = resting.ID, taker.ID
		}

		trades = append(trades, TradeRecord{
			ID:          taker.ID + "-" + resting.ID,
			Symbol:      taker.Symbol,
			Price:       resting.Price,
			Amount:      fill,
			Buyer:       buyer,
			Seller:      seller,
			BuyOrderID:  buyOrderID,
			SellOrderID: sellOrderID,
			Timestamp:   taker.CreatedAt,
		})
	}

	if taker.Remaining == 0 {
		taker.Status = OrderFilled
	}

	return trades
}

// MaxTradeHistoryPerSymbol bounds memory/storage growth, same "keep
// the most recent N, drop the rest" convention as
// core/price_history.go's MaxPricePointsPerSymbol.
const MaxTradeHistoryPerSymbol = 500

// TradeHistoryStore keeps a bounded, per-symbol, chronological record
// of completed trades - the trade tape. Separate from OrderBookStore
// since a trade outlives the orders that produced it (an order can be
// looked up and re-displayed by ID either way, but the tape itself is
// symbol-indexed and append-only).
type TradeHistoryStore struct {
	Trades map[string][]TradeRecord
}

func NewTradeHistoryStore() *TradeHistoryStore {
	return &TradeHistoryStore{Trades: make(map[string][]TradeRecord)}
}

// Record appends every trade from one Match call, oldest first,
// trimming to MaxTradeHistoryPerSymbol.
func (s *TradeHistoryStore) Record(trades []TradeRecord) {
	for _, t := range trades {
		list := append(s.Trades[t.Symbol], t)
		if len(list) > MaxTradeHistoryPerSymbol {
			list = list[len(list)-MaxTradeHistoryPerSymbol:]
		}
		s.Trades[t.Symbol] = list
	}
}

// Get returns symbol's trade tape, oldest first.
func (s *TradeHistoryStore) Get(symbol string) []TradeRecord {
	return s.Trades[symbol]
}
