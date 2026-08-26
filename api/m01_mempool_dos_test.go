package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// resetMempoolDoSTestState gives each test a clean mempool/validator/
// wallet state - reuses resetApplyTestState for everything
// buildSignedTransferTx needs (wallet/cct stores, nodeIdentity, etc.)
// plus a fresh mempool, which resetApplyTestState doesn't touch.
func resetMempoolDoSTestState() {
	resetApplyTestState()
	mempool = core.Mempool{}
}

func postTxTo(t *testing.T, handler http.HandlerFunc, tx core.Transaction, networkSecret string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(tx)
	if err != nil {
		t.Fatalf("marshal tx: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/irrelevant", bytes.NewReader(body))
	if networkSecret != "" {
		req.Header.Set(networkSecretHeader, networkSecret)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// TestHandlePeerTx_RejectsUnauthenticatedTransactionBeforeMempoolAdmission
// is the regression test for audit finding M-01: HandlePeerTx used to
// insert any deserialized transaction from a NETWORK_SECRET holder
// straight into the mempool with zero signature validation - an
// attacker (or a peer relaying something it never validated itself)
// could flood the FIFO mempool with garbage, evicting legitimate
// pending transactions. A transaction with no valid signature must now
// be rejected before it ever reaches the mempool.
func TestHandlePeerTx_RejectsUnauthenticatedTransactionBeforeMempoolAdmission(t *testing.T) {
	resetMempoolDoSTestState()
	t.Setenv("NETWORK_SECRET", "test-secret")

	tx, _ := buildSignedTransferTx(t, "recipient-address", 100, time.Now().Unix())
	tx.AuthSignature = "00" + tx.AuthSignature[2:] // corrupt it

	rec := postTxTo(t, HandlePeerTx, tx, "test-secret")

	if rec.Code == http.StatusOK {
		t.Fatalf("expected a non-200 rejection for an unauthenticated transaction, got %d", rec.Code)
	}
	if mempool.Has(tx.ID) {
		t.Fatal("expected the unauthenticated transaction to NEVER reach the mempool")
	}
}

// TestHandlePeerTx_RejectsImplausibleFutureTimestamp closes the other
// half of M-01: core.Mempool.Prune only expires a transaction once
// `now - tx.Timestamp` exceeds MempoolTxExpiry, so an unvalidated
// future timestamp would never age out at all, letting spam sit
// forever instead of the usual 15 minutes.
func TestHandlePeerTx_RejectsImplausibleFutureTimestamp(t *testing.T) {
	resetMempoolDoSTestState()
	t.Setenv("NETWORK_SECRET", "test-secret")

	farFuture := time.Now().Unix() + int64(core.MaxFutureMempoolTxDrift.Seconds()) + 3600
	tx, _ := buildSignedTransferTx(t, "recipient-address", 100, farFuture)

	rec := postTxTo(t, HandlePeerTx, tx, "test-secret")

	if rec.Code == http.StatusOK {
		t.Fatalf("expected a non-200 rejection for an implausibly-future-timestamped transaction, got %d", rec.Code)
	}
	if mempool.Has(tx.ID) {
		t.Fatal("expected the future-timestamped transaction to NEVER reach the mempool")
	}
}

// TestHandlePeerTx_AcceptsGenuinelyValidTransaction is the sanity
// check that the M-01 fix doesn't also break the honest path.
func TestHandlePeerTx_AcceptsGenuinelyValidTransaction(t *testing.T) {
	resetMempoolDoSTestState()
	t.Setenv("NETWORK_SECRET", "test-secret")

	tx, _ := buildSignedTransferTx(t, "recipient-address", 100, time.Now().Unix())

	rec := postTxTo(t, HandlePeerTx, tx, "test-secret")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected a genuinely valid, freshly-timestamped transaction to be accepted, got %d: %s", rec.Code, rec.Body.String())
	}
	if !mempool.Has(tx.ID) {
		t.Fatal("expected the valid transaction to reach the mempool")
	}
}

// TestHandleSubmitTx_RejectsUnauthenticatedTransactionBeforeMempoolAdmission
// is the sibling regression test for the public, no-secret-required
// /tx/submit endpoint (api/submit_tx.go) - it had the identical gap,
// actually worse than HandlePeerTx's since it has no shared-secret
// gate at all, meaning any anonymous caller on the internet could
// flood the mempool before this fix.
func TestHandleSubmitTx_RejectsUnauthenticatedTransactionBeforeMempoolAdmission(t *testing.T) {
	resetMempoolDoSTestState()

	tx, _ := buildSignedTransferTx(t, "recipient-address", 100, time.Now().Unix())
	tx.AuthSignature = "00" + tx.AuthSignature[2:] // corrupt it

	rec := postTxTo(t, HandleSubmitTx, tx, "")

	if rec.Code == http.StatusOK {
		t.Fatalf("expected a non-200 rejection for an unauthenticated transaction, got %d", rec.Code)
	}
	if mempool.Has(tx.ID) {
		t.Fatal("expected the unauthenticated transaction to NEVER reach the mempool")
	}
}

// TestHandleSubmitTx_RejectsImplausibleFutureTimestamp mirrors the
// HandlePeerTx future-timestamp test for /tx/submit.
func TestHandleSubmitTx_RejectsImplausibleFutureTimestamp(t *testing.T) {
	resetMempoolDoSTestState()

	farFuture := time.Now().Unix() + int64(core.MaxFutureMempoolTxDrift.Seconds()) + 3600
	tx, _ := buildSignedTransferTx(t, "recipient-address", 100, farFuture)

	rec := postTxTo(t, HandleSubmitTx, tx, "")

	if rec.Code == http.StatusOK {
		t.Fatalf("expected a non-200 rejection for an implausibly-future-timestamped transaction, got %d", rec.Code)
	}
	if mempool.Has(tx.ID) {
		t.Fatal("expected the future-timestamped transaction to NEVER reach the mempool")
	}
}

// TestHandleSubmitTx_AcceptsGenuinelyValidTransaction is the honest-
// path sanity check for /tx/submit.
func TestHandleSubmitTx_AcceptsGenuinelyValidTransaction(t *testing.T) {
	resetMempoolDoSTestState()

	tx, _ := buildSignedTransferTx(t, "recipient-address", 100, time.Now().Unix())

	rec := postTxTo(t, HandleSubmitTx, tx, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected a genuinely valid, freshly-timestamped transaction to be accepted, got %d: %s", rec.Code, rec.Body.String())
	}
	if !mempool.Has(tx.ID) {
		t.Fatal("expected the valid transaction to reach the mempool")
	}
}
