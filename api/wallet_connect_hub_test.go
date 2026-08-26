package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func registerVerifiedHub(t *testing.T) (pub, priv []byte, addr string) {
	t.Helper()
	hubPub, hubPriv, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	companyStore.Apply(&core.CompanyApplication{Address: hubAddr, Name: "Test Hub", Status: core.CompanyVerified})
	return hubPub, hubPriv, hubAddr
}

func postCreateWallet(t *testing.T, pub []byte, referredBy, connectedHub string) *httptest.ResponseRecorder {
	t.Helper()
	addr := core.GenerateAddress(pub)
	body, _ := json.Marshal(createWalletRequest{
		Address: addr, PublicKey: hex.EncodeToString(pub),
		ReferredBy: referredBy, ConnectedHub: connectedHub,
	})
	req := httptest.NewRequest(http.MethodPost, "/wallet/create", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleCreateWallet(rec, req)
	return rec
}

func TestHandleCreateWallet_ConnectedHubSetWhenHubVerified(t *testing.T) {
	resetApplyTestState()
	_, _, hubAddr := registerVerifiedHub(t)

	pub, _, _ := core.GenerateKeyPair()
	rec := postCreateWallet(t, pub, "", hubAddr)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	addr := core.GenerateAddress(pub)
	if got := walletStore.Get(addr).ConnectedHub; got != hubAddr {
		t.Fatalf("expected ConnectedHub %q, got %q", hubAddr, got)
	}
}

func TestHandleCreateWallet_ConnectedHubIgnoredWhenHubNotVerified(t *testing.T) {
	resetApplyTestState()

	pub, _, _ := core.GenerateKeyPair()
	rec := postCreateWallet(t, pub, "", "not-a-real-hub")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (an unverified hub is silently ignored, not rejected), got %d: %s", rec.Code, rec.Body.String())
	}

	addr := core.GenerateAddress(pub)
	if got := walletStore.Get(addr).ConnectedHub; got != "" {
		t.Fatalf("expected ConnectedHub to stay empty for an unverified hub, got %q", got)
	}
}

func TestApplyWalletConnectHub_SetsConnectedHubForVerifiedHub(t *testing.T) {
	resetApplyTestState()
	_, _, hubAddr := registerVerifiedHub(t)

	const wallet = "some-wallet-addr"
	data, _ := json.Marshal(walletConnectHubData{Hub: hubAddr})
	applyWalletConnectHub(core.Transaction{From: wallet, Data: string(data)})

	if got := walletStore.Get(wallet).ConnectedHub; got != hubAddr {
		t.Fatalf("expected ConnectedHub %q, got %q", hubAddr, got)
	}
}

func TestApplyWalletConnectHub_NoOpForUnverifiedHub(t *testing.T) {
	resetApplyTestState()

	const wallet = "some-wallet-addr"
	data, _ := json.Marshal(walletConnectHubData{Hub: "not-a-real-hub"})
	applyWalletConnectHub(core.Transaction{From: wallet, Data: string(data)})

	if got := walletStore.Get(wallet).ConnectedHub; got != "" {
		t.Fatalf("expected ConnectedHub to stay empty for an unverified hub, got %q", got)
	}
}

func TestApplyWalletConnectHub_NoOpForSelfConnect(t *testing.T) {
	resetApplyTestState()
	_, _, hubAddr := registerVerifiedHub(t)

	// The hub's own wallet trying to "connect" to itself - nonsensical,
	// must be rejected even though the hub is genuinely verified.
	data, _ := json.Marshal(walletConnectHubData{Hub: hubAddr})
	applyWalletConnectHub(core.Transaction{From: hubAddr, Data: string(data)})

	if got := walletStore.Get(hubAddr).ConnectedHub; got != "" {
		t.Fatalf("expected a hub connecting to itself to be a no-op, got %q", got)
	}
}

func TestHandleConnectHub_RejectsUnverifiedHub(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSON(t, pub, priv, walletConnectHubData{Hub: "not-a-real-hub"})
	req := httptest.NewRequest(http.MethodPost, "/wallet/connect-hub", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleConnectHub(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unverified hub, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleConnectHub_RejectsSelfConnect(t *testing.T) {
	resetApplyTestState()

	hubPub, hubPriv, hubAddr := registerVerifiedHub(t)
	body := buildSignedEnvelopeJSON(t, hubPub, hubPriv, walletConnectHubData{Hub: hubAddr})
	req := httptest.NewRequest(http.MethodPost, "/wallet/connect-hub", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleConnectHub(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a hub connecting to itself, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleConnectHub_AcceptsVerifiedHub(t *testing.T) {
	resetApplyTestState()

	_, _, hubAddr := registerVerifiedHub(t)
	traderPub, traderPriv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSON(t, traderPub, traderPriv, walletConnectHubData{Hub: hubAddr})
	req := httptest.NewRequest(http.MethodPost, "/wallet/connect-hub", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleConnectHub(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a genuinely verified hub, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetConnectedHub_ReturnsHubAndName(t *testing.T) {
	resetApplyTestState()
	_, _, hubAddr := registerVerifiedHub(t)

	const wallet = "some-wallet-addr"
	walletStore.Get(wallet).ConnectedHub = hubAddr

	req := httptest.NewRequest(http.MethodGet, "/wallet/connected-hub?address="+wallet, nil)
	rec := httptest.NewRecorder()
	HandleGetConnectedHub(rec, req)

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}
	if resp["connectedHub"] != hubAddr {
		t.Fatalf("expected connectedHub %q, got %q", hubAddr, resp["connectedHub"])
	}
	if resp["hubName"] != "Test Hub" {
		t.Fatalf("expected hubName %q, got %q", "Test Hub", resp["hubName"])
	}
}

func TestHandleGetConnectedHub_EmptyForWalletWithNoConnectedHub(t *testing.T) {
	resetApplyTestState()

	req := httptest.NewRequest(http.MethodGet, "/wallet/connected-hub?address=some-wallet-addr", nil)
	rec := httptest.NewRecorder()
	HandleGetConnectedHub(rec, req)

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}
	if resp["connectedHub"] != "" {
		t.Fatalf("expected empty connectedHub, got %q", resp["connectedHub"])
	}
}
