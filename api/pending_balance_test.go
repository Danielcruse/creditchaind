package api

import (
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func TestPendingReservedBalance_SumsMatchingSendsAndSwaps(t *testing.T) {
	resetApplyTestState()
	mempool.Clear()
	defer mempool.Clear()

	addr := "someaddress"

	sendData, _ := json.Marshal(tokenTransferData{To: "dest1", Amount: 40, Symbol: "ETHB"})
	mempool.Add(core.Transaction{
		From: addr,
		Type: core.TxExchange,
		Data: string(sendData),
	})

	swapData, _ := json.Marshal(fairValueSwapRequest{FromSymbol: "ETHB", ToSymbol: "BTCB", FromAmount: 10})
	mempool.Add(core.Transaction{
		From: addr,
		Type: core.TxFairValueSwap,
		Data: string(swapData),
	})

	// A pending transaction from a DIFFERENT address must not count
	// against this address's own reservation.
	otherSendData, _ := json.Marshal(tokenTransferData{To: "dest1", Amount: 1000, Symbol: "ETHB"})
	mempool.Add(core.Transaction{
		From: "someone-else",
		Type: core.TxExchange,
		Data: string(otherSendData),
	})

	// A pending transaction for a DIFFERENT symbol must not count
	// against this address's ETHB reservation.
	unrelatedSendData, _ := json.Marshal(tokenTransferData{To: "dest1", Amount: 999, Symbol: "BTCB"})
	mempool.Add(core.Transaction{
		From: addr,
		Type: core.TxExchange,
		Data: string(unrelatedSendData),
	})

	got := pendingReservedBalance(addr, "ETHB")
	want := uint64(40) + ETHBCCTNetworkFeeWei + 10
	if got != want {
		t.Fatalf("pendingReservedBalance = %d, want %d", got, want)
	}
}

func TestPendingReservedBalance_UsdcSendReservesEthbFee(t *testing.T) {
	resetApplyTestState()
	mempool.Clear()
	defer mempool.Clear()

	addr := "someaddress"

	sendData, _ := json.Marshal(tokenTransferData{To: "dest1", Amount: 5, Symbol: "USDCTEST"})
	mempool.Add(core.Transaction{
		From: addr,
		Type: core.TxExchange,
		Data: string(sendData),
	})

	got := pendingReservedBalance(addr, "ETHB")
	if got != UsdcTestTransferFeeWei {
		t.Fatalf("pendingReservedBalance(ETHB) after pending USDC send = %d, want %d", got, UsdcTestTransferFeeWei)
	}

	// The USDCTEST balance itself should also be reserved.
	gotUsdc := pendingReservedBalance(addr, "USDCTEST")
	if gotUsdc != 5 {
		t.Fatalf("pendingReservedBalance(USDCTEST) = %d, want 5", gotUsdc)
	}
}

// TestPendingReservedBalance_RejectsSecondPendingSendOfSameBalance
// proves the actual bug this guard closes: two sends submitted
// back-to-back against a balance that can only cover one of them
// used to both pass the handler's own fast-fail check (since neither
// had confirmed yet), with the second one silently no-op-ing once
// applied. The reservation this test checks is what
// HandleTokenTransfer now rejects on, immediately, with a clear
// reason, instead of accepting a doomed second submission.
func TestPendingReservedBalance_RejectsSecondPendingSendOfSameBalance(t *testing.T) {
	resetApplyTestState()
	mempool.Clear()
	defer mempool.Clear()

	pub, _, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(pub)

	bridgedEthStore.Mint(from, 100)

	firstData, _ := json.Marshal(tokenTransferData{To: "dest1", Amount: 90, Symbol: "ETHB"})
	mempool.Add(core.Transaction{
		From: from,
		Type: core.TxExchange,
		Data: string(firstData),
	})

	// A second send for another 90 ETHB should now be rejected -
	// only 100 confirmed, 90+fee already reserved by the first. Guard
	// against underflow the same way the production callers do
	// (pending can legitimately exceed the confirmed balance, e.g.
	// once a fee is folded in) before ever subtracting.
	available := bridgedEthStore.BalanceOf(from)
	pending := pendingReservedBalance(from, "ETHB")
	if pending == 0 {
		t.Fatalf("expected the first pending send to be reserved, got 0")
	}
	sufficientForSecondSend := pending < available && available-pending >= 90
	if sufficientForSecondSend {
		t.Fatalf("expected insufficient available balance after reservation, got %d available after %d pending", available, pending)
	}
}
