package core

import "testing"

func newTestOrder(id, owner string, side OrderSide, price, amount uint64, createdAt int64) *Order {
	return &Order{
		ID:        id,
		Owner:     owner,
		Symbol:    "ETHTEST",
		Side:      side,
		Price:     price,
		Amount:    amount,
		Remaining: amount,
		Status:    OrderOpen,
		CreatedAt: createdAt,
	}
}

func TestMatch_BestPriceWinsOverWorsePrice(t *testing.T) {
	s := NewOrderBookStore()

	cheapAsk := newTestOrder("ask-cheap", "seller-cheap", OrderSell, 1_000_000, 10, 1)
	pricierAsk := newTestOrder("ask-pricier", "seller-pricier", OrderSell, 2_000_000, 10, 2)
	s.Create(cheapAsk)
	s.Create(pricierAsk)

	taker := newTestOrder("buy-1", "buyer", OrderBuy, 2_000_000, 5, 3)
	trades := s.Match(taker)

	if len(trades) != 1 {
		t.Fatalf("expected exactly 1 trade, got %d", len(trades))
	}
	if trades[0].SellOrderID != "ask-cheap" {
		t.Fatalf("expected the cheaper ask to fill first, got seller order %s", trades[0].SellOrderID)
	}
	if trades[0].Price != 1_000_000 {
		t.Fatalf("expected fill at the resting (maker) price 1000000, got %d", trades[0].Price)
	}
	if cheapAsk.Remaining != 5 {
		t.Fatalf("expected cheap ask to have 5 remaining after a 5-unit fill, got %d", cheapAsk.Remaining)
	}
	if pricierAsk.Remaining != 10 {
		t.Fatalf("expected pricier ask untouched, got remaining %d", pricierAsk.Remaining)
	}
}

func TestMatch_TiesBrokenByTimeThenID(t *testing.T) {
	s := NewOrderBookStore()

	// Same price, different CreatedAt - earlier one should fill first.
	later := newTestOrder("ask-later", "seller-later", OrderSell, 1_000_000, 10, 100)
	earlier := newTestOrder("ask-earlier", "seller-earlier", OrderSell, 1_000_000, 10, 50)
	s.Create(later)
	s.Create(earlier)

	taker := newTestOrder("buy-1", "buyer", OrderBuy, 1_000_000, 5, 200)
	trades := s.Match(taker)

	if len(trades) != 1 || trades[0].SellOrderID != "ask-earlier" {
		t.Fatalf("expected the earlier-created resting order to fill first, got %+v", trades)
	}
}

func TestMatch_TiesBrokenByIDWhenPriceAndTimeEqual(t *testing.T) {
	s := NewOrderBookStore()

	orderB := newTestOrder("ask-b", "seller-b", OrderSell, 1_000_000, 10, 1)
	orderA := newTestOrder("ask-a", "seller-a", OrderSell, 1_000_000, 10, 1)
	s.Create(orderB)
	s.Create(orderA)

	taker := newTestOrder("buy-1", "buyer", OrderBuy, 1_000_000, 5, 1)
	trades := s.Match(taker)

	if len(trades) != 1 || trades[0].SellOrderID != "ask-a" {
		t.Fatalf("expected the lexicographically-smaller ID to win the tie, got %+v", trades)
	}
}

func TestMatch_PartialFillAcrossMultipleRestingOrders(t *testing.T) {
	s := NewOrderBookStore()

	ask1 := newTestOrder("ask-1", "seller-1", OrderSell, 1_000_000, 4, 1)
	ask2 := newTestOrder("ask-2", "seller-2", OrderSell, 1_000_000, 4, 2)
	s.Create(ask1)
	s.Create(ask2)

	taker := newTestOrder("buy-1", "buyer", OrderBuy, 1_000_000, 6, 3)
	trades := s.Match(taker)

	if len(trades) != 2 {
		t.Fatalf("expected 2 trades to fill 6 units across two 4-unit resting orders, got %d", len(trades))
	}
	if ask1.Remaining != 0 || ask1.Status != OrderFilled {
		t.Fatalf("expected ask-1 fully filled, got remaining=%d status=%s", ask1.Remaining, ask1.Status)
	}
	if ask2.Remaining != 2 || ask2.Status != OrderOpen {
		t.Fatalf("expected ask-2 partially filled with 2 remaining and still open, got remaining=%d status=%s", ask2.Remaining, ask2.Status)
	}
	if taker.Remaining != 0 || taker.Status != OrderFilled {
		t.Fatalf("expected taker fully filled, got remaining=%d status=%s", taker.Remaining, taker.Status)
	}
}

func TestMatch_SelfTradeSkipped(t *testing.T) {
	s := NewOrderBookStore()

	ownAsk := newTestOrder("ask-own", "same-owner", OrderSell, 1_000_000, 10, 1)
	s.Create(ownAsk)

	taker := newTestOrder("buy-1", "same-owner", OrderBuy, 1_000_000, 5, 2)
	trades := s.Match(taker)

	if len(trades) != 0 {
		t.Fatalf("expected no trades against the same owner's own resting order, got %d", len(trades))
	}
	if taker.Remaining != 5 || taker.Status != OrderOpen {
		t.Fatalf("expected taker to remain fully open/unfilled, got remaining=%d status=%s", taker.Remaining, taker.Status)
	}
	if ownAsk.Remaining != 10 {
		t.Fatalf("expected the resting order untouched, got remaining %d", ownAsk.Remaining)
	}
}

func TestMatch_NoCrossLeavesBothOrdersResting(t *testing.T) {
	s := NewOrderBookStore()

	ask := newTestOrder("ask-1", "seller", OrderSell, 2_000_000, 10, 1)
	s.Create(ask)

	// Buyer's limit is below the resting ask - no trade should occur.
	taker := newTestOrder("buy-1", "buyer", OrderBuy, 1_000_000, 5, 2)
	trades := s.Match(taker)

	if len(trades) != 0 {
		t.Fatalf("expected no trade when the taker's limit doesn't cross the resting price, got %d", len(trades))
	}
	if taker.Status != OrderOpen || taker.Remaining != 5 {
		t.Fatalf("expected taker to remain open/unfilled, got status=%s remaining=%d", taker.Status, taker.Remaining)
	}
	if ask.Status != OrderOpen || ask.Remaining != 10 {
		t.Fatalf("expected resting ask untouched, got status=%s remaining=%d", ask.Status, ask.Remaining)
	}
}

func TestMatch_FullFillRemovedFromVisibleDepthButKeepsHistory(t *testing.T) {
	s := NewOrderBookStore()

	ask := newTestOrder("ask-1", "seller", OrderSell, 1_000_000, 5, 1)
	s.Create(ask)

	taker := newTestOrder("buy-1", "buyer", OrderBuy, 1_000_000, 5, 2)
	s.Match(taker)
	s.Create(taker)

	open := s.OpenOrders("ETHTEST")
	if len(open) != 0 {
		t.Fatalf("expected 0 open orders after a full fill on both sides, got %d", len(open))
	}

	// Both should still be retrievable by ID for history/audit purposes.
	if o, ok := s.Get("ask-1"); !ok || o.Status != OrderFilled {
		t.Fatalf("expected the filled resting order still retrievable with FILLED status, got %+v, ok=%v", o, ok)
	}
	if o, ok := s.Get("buy-1"); !ok || o.Status != OrderFilled {
		t.Fatalf("expected the filled taker order still retrievable with FILLED status, got %+v, ok=%v", o, ok)
	}
}

func TestMatch_UnfilledRemainderMustBeCreatedByCallerToRest(t *testing.T) {
	s := NewOrderBookStore()

	ask := newTestOrder("ask-1", "seller", OrderSell, 1_000_000, 3, 1)
	s.Create(ask)

	taker := newTestOrder("buy-1", "buyer", OrderBuy, 1_000_000, 10, 2)
	s.Match(taker)

	if taker.Remaining != 7 || taker.Status != OrderOpen {
		t.Fatalf("expected taker partially filled with 7 remaining, still open, got remaining=%d status=%s", taker.Remaining, taker.Status)
	}

	// Per Match's doc comment, it never adds the taker to the store itself.
	if _, ok := s.Get("buy-1"); ok {
		t.Fatalf("expected Match to NOT have added the taker to the store on its own")
	}
	s.Create(taker)
	open := s.OpenOrders("ETHTEST")
	if len(open) != 1 || open[0].ID != "buy-1" {
		t.Fatalf("expected the taker to become the sole resting open order after Create, got %+v", open)
	}
}

func TestOrderBookStore_CancelOnlyOwnerAndOnlyOpen(t *testing.T) {
	s := NewOrderBookStore()
	o := newTestOrder("order-1", "owner-1", OrderSell, 1_000_000, 5, 1)
	s.Create(o)

	if _, ok := s.Cancel("order-1", "not-the-owner"); ok {
		t.Fatalf("expected cancel to fail for a non-owner")
	}
	cancelled, ok := s.Cancel("order-1", "owner-1")
	if !ok || cancelled.Status != OrderCancelled {
		t.Fatalf("expected owner to successfully cancel, got ok=%v status=%v", ok, cancelled)
	}
	if _, ok := s.Cancel("order-1", "owner-1"); ok {
		t.Fatalf("expected a second cancel of an already-cancelled order to fail")
	}
}

func TestOrderCCTCost(t *testing.T) {
	// 10 units at 2 CCT each (price scaled by 1e6) = 20 CCT.
	if got := OrderCCTCost(10, 2_000_000); got != 20 {
		t.Fatalf("expected OrderCCTCost(10, 2_000_000) = 20, got %d", got)
	}
}
