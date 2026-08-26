package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// resetOrderBookTestState clears every store an order-book settlement
// test touches. orderBookStore/tradeHistoryStore/ethTestStore aren't
// covered by resetApplyTestState (see apply_test.go) - resting orders
// left over from a previous test would otherwise still be in the book
// and could unexpectedly cross with a later test's orders on the same
// symbol.
func resetOrderBookTestState() {
	resetApplyTestState()
	ethTestStore = core.NewTokenStore()
	orderBookStore = core.NewOrderBookStore()
	tradeHistoryStore = core.NewTradeHistoryStore()
}

type testWallet struct {
	address string
	pubHex  string
}

func newTestWallet(t *testing.T) testWallet {
	t.Helper()
	pub, _, err := core.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	return testWallet{
		address: core.GenerateAddress(pub),
		pubHex:  hex.EncodeToString(pub),
	}
}

func orderTxData(symbol, side string, price, amount uint64) string {
	b, _ := json.Marshal(orderPlaceData{Symbol: symbol, Side: side, Price: price, Amount: amount})
	return string(b)
}

func TestApplyOrderPlace_BuyMatchesRestingSellSettlesBothLegs(t *testing.T) {
	resetOrderBookTestState()

	seller := newTestWallet(t)
	buyer := newTestWallet(t)

	sellerETHSide := resolveBalanceAddress(seller.address, seller.pubHex, "ETHTEST")
	ethTestStore.Mint(sellerETHSide, 10)

	sellTx := core.Transaction{
		ID:            "sell-1",
		From:          seller.address,
		Type:          core.TxOrderPlace,
		Timestamp:     100,
		AuthPublicKey: seller.pubHex,
		Data:          orderTxData("ETHTEST", "SELL", 2_000_000, 10),
	}
	applyOrderPlace(sellTx)

	if got := ethTestStore.BalanceOf(sellerETHSide); got != 0 {
		t.Fatalf("expected seller's ETHTEST fully escrowed, got %d remaining", got)
	}
	if got := ethTestStore.BalanceOf(orderEscrowAddress("sell-1")); got != 10 {
		t.Fatalf("expected sell order's escrow to hold 10 ETHTEST, got %d", got)
	}

	tokenStore.Mint(buyer.address, 20) // exact cost at 2 CCT/unit * 10 units

	buyTx := core.Transaction{
		ID:            "buy-1",
		From:          buyer.address,
		Type:          core.TxOrderPlace,
		Timestamp:     200,
		AuthPublicKey: buyer.pubHex,
		Data:          orderTxData("ETHTEST", "BUY", 2_000_000, 10),
	}
	applyOrderPlace(buyTx)

	if got := tokenStore.BalanceOf(buyer.address); got != 0 {
		t.Fatalf("expected buyer's CCT fully spent (exact price match, no surplus), got %d", got)
	}
	if got := tokenStore.BalanceOf(seller.address); got != 20 {
		t.Fatalf("expected seller to receive 20 CCT, got %d", got)
	}
	buyerETHSide := resolveBalanceAddress(buyer.address, buyer.pubHex, "ETHTEST")
	if got := ethTestStore.BalanceOf(buyerETHSide); got != 10 {
		t.Fatalf("expected buyer to receive 10 ETHTEST, got %d", got)
	}
	if got := ethTestStore.BalanceOf(orderEscrowAddress("sell-1")); got != 0 {
		t.Fatalf("expected sell order's escrow drained, got %d", got)
	}
	if got := tokenStore.BalanceOf(orderEscrowAddress("buy-1")); got != 0 {
		t.Fatalf("expected buy order's escrow drained, got %d", got)
	}

	sellOrder, ok := orderBookStore.Get("sell-1")
	if !ok || sellOrder.Status != core.OrderFilled {
		t.Fatalf("expected sell-1 to be FILLED, got %+v ok=%v", sellOrder, ok)
	}
	buyOrder, ok := orderBookStore.Get("buy-1")
	if !ok || buyOrder.Status != core.OrderFilled {
		t.Fatalf("expected buy-1 to be FILLED, got %+v ok=%v", buyOrder, ok)
	}

	trades := tradeHistoryStore.Get("ETHTEST")
	if len(trades) != 1 || trades[0].Amount != 10 || trades[0].Price != 2_000_000 {
		t.Fatalf("expected exactly 1 recorded trade of 10 units at 2000000, got %+v", trades)
	}
}

func TestApplyOrderPlace_BuyAtBetterPriceThanRestingAskGetsSurplusRefunded(t *testing.T) {
	resetOrderBookTestState()

	seller := newTestWallet(t)
	buyer := newTestWallet(t)

	sellerETHSide := resolveBalanceAddress(seller.address, seller.pubHex, "ETHTEST")
	ethTestStore.Mint(sellerETHSide, 5)

	sellTx := core.Transaction{
		ID:            "sell-cheap",
		From:          seller.address,
		Type:          core.TxOrderPlace,
		Timestamp:     100,
		AuthPublicKey: seller.pubHex,
		Data:          orderTxData("ETHTEST", "SELL", 1_000_000, 5),
	}
	applyOrderPlace(sellTx)

	// Buyer's limit (2 CCT/unit) is worse than the resting ask (1
	// CCT/unit) - the trade should execute at the maker's (seller's)
	// price, and the buyer should get the difference refunded.
	tokenStore.Mint(buyer.address, 10) // worst-case cost at their own 2 CCT/unit limit

	buyTx := core.Transaction{
		ID:            "buy-pricier",
		From:          buyer.address,
		Type:          core.TxOrderPlace,
		Timestamp:     200,
		AuthPublicKey: buyer.pubHex,
		Data:          orderTxData("ETHTEST", "BUY", 2_000_000, 5),
	}
	applyOrderPlace(buyTx)

	if got := tokenStore.BalanceOf(buyer.address); got != 5 {
		t.Fatalf("expected buyer refunded the 5 CCT price-improvement surplus (10 escrowed - 5 actual cost), got %d", got)
	}
	if got := tokenStore.BalanceOf(seller.address); got != 5 {
		t.Fatalf("expected seller paid at the resting price (5 units * 1 CCT), got %d", got)
	}
	if got := tokenStore.BalanceOf(orderEscrowAddress("buy-pricier")); got != 0 {
		t.Fatalf("expected buy order's escrow fully drained after settlement + refund, got %d", got)
	}
}

func TestApplyOrderCancel_ReleasesExactRemainingEscrow(t *testing.T) {
	resetOrderBookTestState()

	seller := newTestWallet(t)
	buyer := newTestWallet(t)

	sellerETHSide := resolveBalanceAddress(seller.address, seller.pubHex, "ETHTEST")
	ethTestStore.Mint(sellerETHSide, 10)

	sellTx := core.Transaction{
		ID:            "sell-partial",
		From:          seller.address,
		Type:          core.TxOrderPlace,
		Timestamp:     100,
		AuthPublicKey: seller.pubHex,
		Data:          orderTxData("ETHTEST", "SELL", 1_000_000, 10),
	}
	applyOrderPlace(sellTx)

	tokenStore.Mint(buyer.address, 4)
	buyTx := core.Transaction{
		ID:            "buy-partial",
		From:          buyer.address,
		Type:          core.TxOrderPlace,
		Timestamp:     200,
		AuthPublicKey: buyer.pubHex,
		Data:          orderTxData("ETHTEST", "BUY", 1_000_000, 4),
	}
	applyOrderPlace(buyTx)

	sellOrder, ok := orderBookStore.Get("sell-partial")
	if !ok || sellOrder.Remaining != 6 || sellOrder.Status != core.OrderOpen {
		t.Fatalf("expected sell order partially filled with 6 remaining, still open, got %+v ok=%v", sellOrder, ok)
	}

	cancelTx := core.Transaction{
		ID:   "cancel-1",
		From: seller.address,
		Type: core.TxOrderCancel,
		Data: `{"orderId":"sell-partial"}`,
	}
	applyOrderCancel(cancelTx)

	if got := ethTestStore.BalanceOf(sellerETHSide); got != 6 {
		t.Fatalf("expected exactly the 6 remaining units released back to the seller, got %d", got)
	}
	if got := ethTestStore.BalanceOf(orderEscrowAddress("sell-partial")); got != 0 {
		t.Fatalf("expected escrow drained to 0 after cancel, got %d", got)
	}
	cancelled, ok := orderBookStore.Get("sell-partial")
	if !ok || cancelled.Status != core.OrderCancelled {
		t.Fatalf("expected order marked CANCELLED, got %+v ok=%v", cancelled, ok)
	}
}

func TestApplyOrderPlace_InsufficientBalanceMovesNothing(t *testing.T) {
	resetOrderBookTestState()

	seller := newTestWallet(t)
	// Deliberately mint nothing - the seller has 0 ETHTEST.

	sellTx := core.Transaction{
		ID:            "sell-broke",
		From:          seller.address,
		Type:          core.TxOrderPlace,
		Timestamp:     100,
		AuthPublicKey: seller.pubHex,
		Data:          orderTxData("ETHTEST", "SELL", 1_000_000, 5),
	}
	applyOrderPlace(sellTx)

	if _, ok := orderBookStore.Get("sell-broke"); ok {
		t.Fatalf("expected no order to be created for an unfunded sell")
	}
	if got := ethTestStore.BalanceOf(orderEscrowAddress("sell-broke")); got != 0 {
		t.Fatalf("expected nothing escrowed, got %d", got)
	}
}

func TestApplyOrderPlace_NoCrossRestsInBook(t *testing.T) {
	resetOrderBookTestState()

	buyer := newTestWallet(t)
	tokenStore.Mint(buyer.address, 10)

	buyTx := core.Transaction{
		ID:            "buy-resting",
		From:          buyer.address,
		Type:          core.TxOrderPlace,
		Timestamp:     100,
		AuthPublicKey: buyer.pubHex,
		Data:          orderTxData("ETHTEST", "BUY", 1_000_000, 10),
	}
	applyOrderPlace(buyTx)

	if got := tokenStore.BalanceOf(orderEscrowAddress("buy-resting")); got != 10 {
		t.Fatalf("expected the full 10 CCT cost escrowed with no counterparty to match, got %d", got)
	}
	open := orderBookStore.OpenOrders("ETHTEST")
	if len(open) != 1 || open[0].ID != "buy-resting" {
		t.Fatalf("expected the unmatched order to rest as the sole open order, got %+v", open)
	}
}
