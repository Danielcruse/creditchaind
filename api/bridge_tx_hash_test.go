package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// buildSignedBridgeBtcWithdrawTx mirrors buildSignedTransferTx's shape
// exactly (see apply_test.go), but for TxBridgeBtcWithdraw - a genuine
// wallet signature over the withdrawal record, same as
// HandleBtcBroadcast now produces via requireSignedRequest.
func buildSignedBridgeBtcWithdrawTx(t *testing.T, payload bridgeBtcWithdrawData, ts int64) (core.Transaction, string) {
	t.Helper()

	pub, priv, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(pub)

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	env := core.SignedEnvelope{
		Address:   from,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: ts,
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(priv, env.CanonicalMessage())

	tx := core.Transaction{
		ID:            "test-btc-withdraw-" + from,
		From:          from,
		To:            payload.To,
		Amount:        payload.AmountSats,
		Type:          core.TxBridgeBtcWithdraw,
		Timestamp:     ts,
		Data:          string(payloadJSON),
		AuthAddress:   from,
		AuthPublicKey: hex.EncodeToString(pub),
		AuthTimestamp: ts,
		AuthPayload:   payloadJSON,
		AuthSignature: hex.EncodeToString(sig),
	}
	return tx, from
}

func TestRecordBridgeExternalTxHash_RoundTrip(t *testing.T) {
	resetApplyTestState()

	if _, ok := lookupBridgeExternalTxHash("no-such-tx"); ok {
		t.Fatalf("expected no hash recorded yet")
	}

	recordBridgeExternalTxHash("orig-tx-1", "0xabc123")

	hash, ok := lookupBridgeExternalTxHash("orig-tx-1")
	if !ok || hash != "0xabc123" {
		t.Fatalf("expected recorded hash 0xabc123, got %q (ok=%v)", hash, ok)
	}

	// Last-write-wins, no dedup guard needed - re-recording (e.g. a
	// replayed chain) just overwrites with the same or a corrected
	// value, never panics or errors.
	recordBridgeExternalTxHash("orig-tx-1", "0xabc123")
	hash, ok = lookupBridgeExternalTxHash("orig-tx-1")
	if !ok || hash != "0xabc123" {
		t.Fatalf("expected idempotent re-record to leave hash unchanged, got %q (ok=%v)", hash, ok)
	}
}

func TestApplyBridgeTxHashRecord_StoresHash(t *testing.T) {
	resetApplyTestState()
	registerCurrentNodeAsAuthorizedValidator(t)

	data, _ := json.Marshal(bridgeTxHashRecordData{
		OriginalTxID: "withdraw-tx-42",
		ExternalHash: "0xdeadbeef",
	})
	env := selfSignedEnvelope(data)
	tx := core.NewAuthenticatedTransaction(env, reporterAddress(), "", 0, core.TxBridgeTxHashRecord, string(data))

	if !verifyTxAuth(tx) {
		t.Fatalf("expected a genuinely self-signed TxBridgeTxHashRecord to pass verifyTxAuth")
	}

	applyBridgeTxHashRecord(tx)

	hash, ok := lookupBridgeExternalTxHash("withdraw-tx-42")
	if !ok || hash != "0xdeadbeef" {
		t.Fatalf("expected hash 0xdeadbeef recorded for withdraw-tx-42, got %q (ok=%v)", hash, ok)
	}
}

func TestApplyBridgeTxHashRecord_MalformedDataIsNoOp(t *testing.T) {
	resetApplyTestState()

	tx := core.Transaction{
		ID:   "bad-record",
		Type: core.TxBridgeTxHashRecord,
		Data: "not json",
	}

	applyBridgeTxHashRecord(tx) // must not panic

	if _, ok := lookupBridgeExternalTxHash(""); ok {
		t.Fatalf("malformed record should not have stored anything")
	}
}

// TestVerifyTxAuth_BridgeBtcWithdraw_AcceptsRealAsymmetricSignedPayload is
// the BTC-side mirror of the ETH regression test in bridge_test.go
// (TestVerifyTxAuth_BridgeWithdraw_AcceptsRealAsymmetricSignedPayload) -
// a real, previously-shipped bug: the client signs btcBroadcastData
// (rawTxHex/to/amountSats/feeSats) BEFORE the real Bitcoin broadcast
// happens, so BtcTxid can never be part of what's signed - it's only
// known AFTER btcBroadcastRawTx succeeds and only ever appears in
// tx.Data. verifyTxAuth's TxBridgeBtcWithdraw case used to compare the
// FULL bridgeBtcWithdrawData struct (including BtcTxid), so
// signed.BtcTxid (always "", absent from the client's signed JSON)
// never equaled claimed.BtcTxid (the real txid) - silently rejecting
// every genuine BTC-network withdrawal as unauthorized. This test
// builds AuthPayload/Data with that real asymmetry (unlike
// buildSignedBridgeBtcWithdrawTx below, which signs the same BtcTxid on
// both sides and so never exercised this bug) and confirms verifyTxAuth
// now correctly accepts it.
func TestVerifyTxAuth_BridgeBtcWithdraw_AcceptsRealAsymmetricSignedPayload(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(pub)

	// Exactly what the client actually signs (via HandleBtcBroadcast's
	// btcBroadcastData) - rawTxHex plus to/amountSats/feeSats, no txid.
	type btcBroadcastRequestShape struct {
		RawTxHex   string `json:"rawTxHex"`
		To         string `json:"to"`
		AmountSats uint64 `json:"amountSats"`
		FeeSats    uint64 `json:"feeSats"`
	}
	signedPayload, _ := json.Marshal(btcBroadcastRequestShape{
		RawTxHex:   "0200000001deadbeef...",
		To:         "tb1qexampleaddress",
		AmountSats: 50000,
		FeeSats:    500,
	})
	env := core.SignedEnvelope{
		Address:   from,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: 6000,
		Payload:   signedPayload,
	}
	sig := core.SignMessage(priv, env.CanonicalMessage())

	// What the server actually records - same to/amountSats/feeSats plus
	// the real txid, only known after the broadcast already succeeded.
	recordedData, _ := json.Marshal(bridgeBtcWithdrawData{
		bridgeBtcWithdrawRequest: bridgeBtcWithdrawRequest{
			To:         "tb1qexampleaddress",
			AmountSats: 50000,
			FeeSats:    500,
		},
		BtcTxid: "real-broadcast-txid-xyz",
	})

	tx := core.Transaction{
		ID:            "asymmetric-btc-withdraw-1",
		From:          from,
		To:            "tb1qexampleaddress",
		Amount:        50000,
		Type:          core.TxBridgeBtcWithdraw,
		Timestamp:     6000,
		Data:          string(recordedData),
		AuthAddress:   from,
		AuthPublicKey: hex.EncodeToString(pub),
		AuthTimestamp: 6000,
		AuthPayload:   signedPayload,
		AuthSignature: hex.EncodeToString(sig),
	}

	if !verifyTxAuth(tx) {
		t.Fatalf("expected verifyTxAuth to accept a genuine TxBridgeBtcWithdraw whose Data carries a server-added BtcTxid absent from the signed AuthPayload")
	}
}

func TestVerifyTxAuth_BridgeBtcWithdraw_AcceptsGenuineSignedMatch(t *testing.T) {
	resetApplyTestState()

	tx, _ := buildSignedBridgeBtcWithdrawTx(t, bridgeBtcWithdrawData{
		bridgeBtcWithdrawRequest: bridgeBtcWithdrawRequest{
			To:         "tb1qexampleaddress",
			AmountSats: 50000,
			FeeSats:    500,
		},
		BtcTxid: "real-btc-txid-abc",
	}, 1000)

	if !verifyTxAuth(tx) {
		t.Fatalf("expected a genuine wallet-signed TxBridgeBtcWithdraw to pass verifyTxAuth")
	}
}

func TestVerifyTxAuth_BridgeBtcWithdraw_RejectsTamperedData(t *testing.T) {
	resetApplyTestState()

	tx, _ := buildSignedBridgeBtcWithdrawTx(t, bridgeBtcWithdrawData{
		bridgeBtcWithdrawRequest: bridgeBtcWithdrawRequest{
			To:         "tb1qexampleaddress",
			AmountSats: 50000,
			FeeSats:    500,
		},
		BtcTxid: "real-btc-txid-abc",
	}, 1000)

	// Simulate a tampered/corrupted Data field (e.g. someone claiming a
	// larger amount than what was actually signed) - the recorded Data
	// no longer matches what AuthPayload's signature actually covers.
	tampered, _ := json.Marshal(bridgeBtcWithdrawData{
		bridgeBtcWithdrawRequest: bridgeBtcWithdrawRequest{
			To:         "tb1qexampleaddress",
			AmountSats: 999999999,
			FeeSats:    500,
		},
		BtcTxid: "real-btc-txid-abc",
	})
	tx.Data = string(tampered)

	if verifyTxAuth(tx) {
		t.Fatalf("expected verifyTxAuth to reject a TxBridgeBtcWithdraw whose Data no longer matches its signed AuthPayload")
	}
}

func TestApplyBridgeBtcWithdraw_NoOpDoesNotPanic(t *testing.T) {
	resetApplyTestState()

	tx, from := buildSignedBridgeBtcWithdrawTx(t, bridgeBtcWithdrawData{
		bridgeBtcWithdrawRequest: bridgeBtcWithdrawRequest{
			To:         "tb1qexampleaddress",
			AmountSats: 12345,
			FeeSats:    100,
		},
		BtcTxid: "real-btc-txid-xyz",
	}, 1000)

	balanceBefore := bridgedBtcStore.BalanceOf(from)
	ApplyTransaction(tx) // must not panic

	if got := bridgedBtcStore.BalanceOf(from); got != balanceBefore {
		t.Fatalf("expected applyBridgeBtcWithdraw to touch no BTCB balance (spends real UTXOs off-ledger instead), balance changed from %d to %d", balanceBefore, got)
	}
}

func TestIsExplorerVisible_HidesBridgeTxHashRecordButShowsWithdraw(t *testing.T) {
	hashRecordTx := core.Transaction{Type: core.TxBridgeTxHashRecord}
	if isExplorerVisible(hashRecordTx) {
		t.Fatalf("expected TxBridgeTxHashRecord to be hidden from the explorer (pure plumbing, no fund movement of its own)")
	}

	withdrawTx := core.Transaction{Type: core.TxBridgeWithdraw}
	if !isExplorerVisible(withdrawTx) {
		t.Fatalf("expected TxBridgeWithdraw to stay visible - it's the real fund movement")
	}

	btcWithdrawTx := core.Transaction{Type: core.TxBridgeBtcWithdraw}
	if !isExplorerVisible(btcWithdrawTx) {
		t.Fatalf("expected TxBridgeBtcWithdraw to stay visible")
	}
}

func TestViewTransaction_PopulatesExternalLinkForBridgeWithdraw(t *testing.T) {
	resetApplyTestState()

	tx := core.Transaction{ID: "eth-withdraw-1", Type: core.TxBridgeWithdraw, Data: "{}"}
	recordBridgeExternalTxHash("eth-withdraw-1", "0xrealsepoliahash")

	view := viewTransaction(tx)
	if view.ExternalTxHash != "0xrealsepoliahash" {
		t.Fatalf("expected ExternalTxHash 0xrealsepoliahash, got %q", view.ExternalTxHash)
	}
	if view.ExternalTxLink != "https://sepolia.etherscan.io/tx/0xrealsepoliahash" {
		t.Fatalf("unexpected ExternalTxLink: %q", view.ExternalTxLink)
	}
}

func TestViewTransaction_PopulatesExternalLinkForBridgeBtcWithdraw(t *testing.T) {
	resetApplyTestState()

	data, _ := json.Marshal(bridgeBtcWithdrawData{
		bridgeBtcWithdrawRequest: bridgeBtcWithdrawRequest{
			To:         "tb1qexampleaddress",
			AmountSats: 5000,
			FeeSats:    50,
		},
		BtcTxid: "real-btc-txid-999",
	})
	tx := core.Transaction{ID: "btc-withdraw-1", Type: core.TxBridgeBtcWithdraw, Data: string(data)}

	view := viewTransaction(tx)
	if view.ExternalTxHash != "real-btc-txid-999" {
		t.Fatalf("expected ExternalTxHash real-btc-txid-999, got %q", view.ExternalTxHash)
	}
	if view.ExternalTxLink != "https://blockstream.info/testnet/tx/real-btc-txid-999" {
		t.Fatalf("unexpected ExternalTxLink: %q", view.ExternalTxLink)
	}
}

func TestViewTransaction_NoExternalLinkForOrdinaryTransfer(t *testing.T) {
	resetApplyTestState()

	tx := core.Transaction{ID: "plain-transfer", Type: core.TxExchange, Data: "{}"}
	view := viewTransaction(tx)
	if view.ExternalTxHash != "" || view.ExternalTxLink != "" {
		t.Fatalf("expected no external link fields on an ordinary transfer, got hash=%q link=%q", view.ExternalTxHash, view.ExternalTxLink)
	}
}
