package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestHandleStake_CCT_AcceptsRealCctStoreBalance is a regression test
// for a real bug: HandleStake's fast-fail balance check called
// stakeTokenStore("CCT"), which resolves to the OLD, frozen
// pre-decimal-migration tokenStore instance - not cctStore, where a
// wallet's real, live CCT balance actually lives since the tokenomics
// decimal migration. Every genuine CCT stake attempt failed with
// "insufficient CCT balance to stake" even with plenty of real CCT,
// because the wrong store was checked. Drives the real HTTP handler
// end-to-end (route -> requireSignedRequest -> HandleStake), the same
// way website/js/staking.js's apiPostSigned call does.
func TestHandleStake_CCT_AcceptsRealCctStoreBalance(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	address := core.GenerateAddress(pub)

	// Seed ONLY cctStore (the real, live CCT ledger) - deliberately
	// leave the legacy tokenStore untouched/zero, so this test fails
	// the old way (false "insufficient balance") if the regression
	// ever comes back.
	cctStore.Mint(address, core.FormatCctSmallest(core.CctWholeToSmallest(500)))

	body := buildSignedEnvelopeJSON(t, pub, priv, map[string]any{
		"Amount": 100,
		"Symbol": "CCT",
	})
	req := httptest.NewRequest(http.MethodPost, "/stake", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleStake(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for a CCT stake backed by a real cctStore balance, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "pending" {
		t.Fatalf("expected status=pending, got %v (body: %s)", resp["status"], w.Body.String())
	}
}

// TestHandleStake_CCT_RejectsInsufficientCctStoreBalance confirms the
// fixed check is a real check, not just "always succeeds" - it must
// still reject a CCT stake the account genuinely can't cover.
func TestHandleStake_CCT_RejectsInsufficientCctStoreBalance(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	address := core.GenerateAddress(pub)
	cctStore.Mint(address, core.FormatCctSmallest(core.CctWholeToSmallest(10)))

	body := buildSignedEnvelopeJSON(t, pub, priv, map[string]any{
		"Amount": 100,
		"Symbol": "CCT",
	})
	req := httptest.NewRequest(http.MethodPost, "/stake", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleStake(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a CCT stake exceeding the real cctStore balance, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleStake_RejectsRetiredETHTESTSymbol confirms ETHTEST is no
// longer accepted for NEW stakes (only CCT/ETHB) - a stale client
// sending the old symbol gets a clear rejection instead of a
// pending-forever transaction that silently no-ops at apply time.
func TestHandleStake_RejectsRetiredETHTESTSymbol(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSON(t, pub, priv, map[string]any{
		"Amount": 10,
		"Symbol": "ETHTEST",
	})
	req := httptest.NewRequest(http.MethodPost, "/stake", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleStake(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a retired ETHTEST stake attempt, got %d: %s", w.Code, w.Body.String())
	}
}
