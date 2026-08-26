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

// TestMainnetGate_FaucetRefusedUnderMainnetMode confirms HandleFaucet
// refuses entirely (never even builds a pending transaction) once
// MAINNET_MODE=true - the faucet must not exist on a real network.
func TestMainnetGate_FaucetRefusedUnderMainnetMode(t *testing.T) {
	resetApplyTestState()
	t.Setenv("MAINNET_MODE", "true")

	pub, priv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSON(t, pub, priv, map[string]any{})
	req := httptest.NewRequest(http.MethodPost, "/faucet", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleFaucet(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 refusing the faucet under MAINNET_MODE, got %d: %s", w.Code, w.Body.String())
	}
}

// TestMainnetGate_FaucetStillWorksWithoutMainnetMode confirms today's
// exact unchanged testnet behavior - the faucet still works normally
// when MAINNET_MODE is unset, so no existing testnet deployment is
// affected by this gate's introduction.
func TestMainnetGate_FaucetStillWorksWithoutMainnetMode(t *testing.T) {
	resetApplyTestState()
	t.Setenv("MAINNET_MODE", "")

	pub, priv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSON(t, pub, priv, map[string]any{})
	req := httptest.NewRequest(http.MethodPost, "/faucet", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleFaucet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for the faucet with MAINNET_MODE unset, got %d: %s", w.Code, w.Body.String())
	}
}

// TestMainnetGate_ApplyFaucetClaimNoOpsUnderMainnetMode confirms the
// authoritative block-confirmation-time check also refuses - never
// trust submission-time state, matching this codebase's blanket
// convention. Even if a faucet-claim transaction somehow reached the
// mempool (e.g. a stale client, a relayed transaction from a
// differently-configured node), it must not mint anything here.
func TestMainnetGate_ApplyFaucetClaimNoOpsUnderMainnetMode(t *testing.T) {
	resetApplyTestState()
	t.Setenv("MAINNET_MODE", "true")

	pub, priv, _ := core.GenerateKeyPair()
	address := core.GenerateAddress(pub)

	payloadJSON := []byte("{}")
	env := core.SignedEnvelope{Address: address, PublicKey: hex.EncodeToString(pub), Timestamp: 1000, Payload: payloadJSON}
	sig := core.SignMessage(priv, env.CanonicalMessage())
	tx := core.Transaction{
		ID: "faucet-mainnet-test-1", From: address, Type: core.TxFaucetClaim,
		Timestamp: 1000, AuthAddress: address, AuthPublicKey: hex.EncodeToString(pub), AuthTimestamp: 1000,
		AuthPayload: payloadJSON, AuthSignature: hex.EncodeToString(sig), Data: string(payloadJSON),
	}

	applyFaucetClaim(tx)

	bal, _ := core.ParseCctSmallest(cctStore.BalanceOf(address))
	if bal.Sign() != 0 {
		t.Fatalf("expected zero CCT minted under MAINNET_MODE, got %s", bal)
	}
}

// TestMainnetGate_USDCTESTBlockedFromLookupCustomToken confirms
// lookupCustomToken - the shared chokepoint every pool/swap/liquidity/
// transfer/P2P/order-book/staking path routes through - refuses the
// USDCTEST symbol once MAINNET_MODE=true, and that this has zero
// effect on any other symbol or on the unset (testnet) case.
func TestMainnetGate_USDCTESTBlockedFromLookupCustomToken(t *testing.T) {
	t.Setenv("MAINNET_MODE", "true")
	if _, _, err := lookupCustomToken("USDCTEST"); err == nil {
		t.Fatal("expected USDCTEST to be blocked under MAINNET_MODE")
	}
	if _, _, err := lookupCustomToken("ETHTEST"); err != nil {
		t.Fatalf("expected ETHTEST to remain unaffected under MAINNET_MODE, got error: %v", err)
	}

	t.Setenv("MAINNET_MODE", "")
	if _, _, err := lookupCustomToken("USDCTEST"); err != nil {
		t.Fatalf("expected USDCTEST to work normally with MAINNET_MODE unset, got error: %v", err)
	}
}

// TestMainnetGate_TokenMintRefusedUnderMainnetMode confirms
// HandleTokenMint refuses entirely (never even reaches the admin-auth
// check) once MAINNET_MODE=true - same "operator-only testnet faucet"
// reasoning as the public faucet above, just admin-gated instead of
// unauthenticated. No valid admin credentials are set up for this
// request at all - the mainnet gate must reject it before auth is even
// evaluated, so an invalid/missing credential can't accidentally be
// what's actually producing the refusal here.
func TestMainnetGate_TokenMintRefusedUnderMainnetMode(t *testing.T) {
	resetApplyTestState()
	t.Setenv("MAINNET_MODE", "true")

	body, _ := json.Marshal(map[string]any{"address": "some-address", "amount": 1000})
	req := httptest.NewRequest(http.MethodPost, "/token/mint", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleTokenMint(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 refusing token mint under MAINNET_MODE, got %d: %s", w.Code, w.Body.String())
	}
}

// TestMainnetGate_ApplyTokenMintNoOpsUnderMainnetMode is the
// authoritative counterpart - even a TxTokenMint transaction that
// somehow reached the mempool (a stale client, a relayed transaction
// from a differently-configured node) must mint nothing once this node
// is actually running as mainnet, mirroring
// TestMainnetGate_ApplyFaucetClaimNoOpsUnderMainnetMode's own reasoning.
func TestMainnetGate_ApplyTokenMintNoOpsUnderMainnetMode(t *testing.T) {
	resetApplyTestState()
	t.Setenv("MAINNET_MODE", "true")

	const address = "43df565874fc3b8415148b4a6e6f01e30cfe1eb8"
	data, _ := json.Marshal(tokenMintData{Address: address, Amount: 1000, Symbol: "CCT"})
	tx := core.Transaction{ID: "mint-mainnet-test-1", From: "admin-addr", Type: core.TxTokenMint, Data: string(data), Timestamp: 1000}

	applyTokenMint(tx)

	if got := core.CctSmallestToWholeFloor(cctStore.BalanceOf(address)); got != 0 {
		t.Fatalf("expected zero CCT minted under MAINNET_MODE, got %d", got)
	}
}

// TestMainnetGate_ApplyTokenMintStillWorksWithoutMainnetMode confirms
// today's exact unchanged testnet behavior - the operator mint still
// works normally when MAINNET_MODE is unset, so no existing testnet
// deployment is affected by this gate's introduction.
func TestMainnetGate_ApplyTokenMintStillWorksWithoutMainnetMode(t *testing.T) {
	resetApplyTestState()
	t.Setenv("MAINNET_MODE", "")

	const address = "43df565874fc3b8415148b4a6e6f01e30cfe1eb8"
	data, _ := json.Marshal(tokenMintData{Address: address, Amount: 1000, Symbol: "CCT"})
	tx := core.Transaction{ID: "mint-testnet-test-1", From: "admin-addr", Type: core.TxTokenMint, Data: string(data), Timestamp: 1000}

	applyTokenMint(tx)

	if got := core.CctSmallestToWholeFloor(cctStore.BalanceOf(address)); got != 1000 {
		t.Fatalf("expected 1000 CCT minted with MAINNET_MODE unset, got %d", got)
	}
}

// TestMainnetGate_USDCTESTBlockedFromStaking confirms the staking
// resolver (stakeTokenStore) independently enforces the same block -
// USDCTEST must not be stakeable on mainnet either.
func TestMainnetGate_USDCTESTBlockedFromStaking(t *testing.T) {
	t.Setenv("MAINNET_MODE", "true")
	if _, err := stakeTokenStore("USDCTEST"); err == nil {
		t.Fatal("expected USDCTEST staking to be blocked under MAINNET_MODE")
	}

	t.Setenv("MAINNET_MODE", "")
	if _, err := stakeTokenStore("USDCTEST"); err != nil {
		t.Fatalf("expected USDCTEST staking to work normally with MAINNET_MODE unset, got error: %v", err)
	}
}
