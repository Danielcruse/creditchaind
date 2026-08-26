package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestHandleCreateEthCustodialAccount_Retired confirms the custodial
// account-creation endpoint is genuinely retired - no new
// CreditChain-held ETH signing key can ever be provisioned through it,
// regardless of caller/balance/chain - it always refuses with a clear
// disclosed message pointing at the self-custodial ETH + ETHB bridge
// story instead of silently succeeding or 404ing.
func TestHandleCreateEthCustodialAccount_Retired(t *testing.T) {
	resetApplyTestState()
	resetEthRPCTestState(t)

	origSigner := ethSigner
	ethSigner = newLocalDevEthSigner()
	t.Cleanup(func() { ethSigner = origSigner })

	pub, priv, _ := core.GenerateKeyPair()
	address := core.GenerateAddress(pub)
	cctStore.Mint(address, core.FormatCctSmallest(core.CctWholeToSmallest(1000)))

	body := buildSignedEnvelopeJSON(t, pub, priv, map[string]any{})
	req := httptest.NewRequest(http.MethodPost, "/eth-custodial/create", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleCreateEthCustodialAccount(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone for a retired endpoint, got %d: %s", w.Code, w.Body.String())
	}
	if _, ok := ethCustodialStore.Get(address); ok {
		t.Fatal("expected no custodial account to ever be created by this endpoint")
	}
	// No fee should ever be charged - the endpoint refuses before doing
	// anything, so a caller's balance must be completely untouched.
	bal, _ := core.ParseCctSmallest(cctStore.BalanceOf(address))
	if bal.Cmp(core.CctWholeToSmallest(1000)) != 0 {
		t.Fatalf("expected no CCT charged by a retired endpoint, balance changed to %s", bal)
	}
}
