package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// adminSignedEnvelope mirrors core's own request_auth_test.go helper
// (unexported there, so re-implemented here) - builds a genuinely
// valid SignedEnvelope for an arbitrary keypair/payload.
func adminSignedEnvelope(t *testing.T, payload any) (core.SignedEnvelope, string) {
	t.Helper()

	pub, priv, err := core.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	address := core.GenerateAddress(pub)
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	e := core.SignedEnvelope{
		Address:   address,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: time.Now().Unix(),
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(priv, e.CanonicalMessage())
	e.Signature = hex.EncodeToString(sig)
	return e, address
}

func TestVerifyAdminTxAuth_AcceptsConfiguredAdminWalletSignature(t *testing.T) {
	resetApplyTestState()

	env, address := adminSignedEnvelope(t, map[string]int{"amount": 5})
	t.Setenv("ADMIN_WALLET_ADDRESS", address)

	tx := core.NewAuthenticatedTransaction(env, "", "", 0, core.TxTokenMint, string(env.Payload))
	if err := core.VerifyTransactionAuth(tx); err != nil {
		t.Fatalf("expected the underlying signature to verify, got: %v", err)
	}
	if !verifyAdminTxAuth(tx) {
		t.Fatal("expected verifyAdminTxAuth to accept a genuine signature from the configured admin wallet")
	}
}

func TestVerifyAdminTxAuth_RejectsDifferentValidAddress(t *testing.T) {
	resetApplyTestState()

	env, _ := adminSignedEnvelope(t, map[string]int{"amount": 5})
	// A second, genuinely-valid keypair - self-consistent, just not the
	// one the network is configured to trust as admin.
	_, otherAddress := adminSignedEnvelope(t, map[string]int{"amount": 5})
	t.Setenv("ADMIN_WALLET_ADDRESS", otherAddress)

	tx := core.NewAuthenticatedTransaction(env, "", "", 0, core.TxTokenMint, string(env.Payload))
	if verifyAdminTxAuth(tx) {
		t.Fatal("expected verifyAdminTxAuth to reject a valid signature from a non-configured address")
	}
}

func TestVerifyAdminTxAuth_AcceptsLegacySelfSignedShape(t *testing.T) {
	resetApplyTestState()
	// No ADMIN_WALLET_ADDRESS configured - only the legacy path applies.
	os.Unsetenv("ADMIN_WALLET_ADDRESS")

	data := `{"amount":5}`
	env := selfSignedEnvelope([]byte(data))
	tx := core.NewAuthenticatedTransaction(env, reporterAddress(), "", 0, core.TxTokenMint, data)

	if !verifyAdminTxAuth(tx) {
		t.Fatal("expected verifyAdminTxAuth to accept the legacy node-self-signed shape when no admin wallet is configured")
	}
}

func TestRequireAdminAuthed_SignedRequestFromConfiguredWallet(t *testing.T) {
	resetApplyTestState()

	env, address := adminSignedEnvelope(t, tokenMintData{Address: "somewhere", Amount: 5, Symbol: "CCT"})
	t.Setenv("ADMIN_WALLET_ADDRESS", address)

	body, _ := json.Marshal(env)
	req := httptest.NewRequest("POST", "/token/mint", bytes.NewReader(body))

	var out tokenMintData
	signedAuth, legacy, err := requireAdminAuthed(req, &out)
	if err != nil {
		t.Fatalf("expected a validly signed request to be accepted, got: %v", err)
	}
	if legacy {
		t.Fatal("expected the signed-wallet path to be used, not the legacy token path")
	}
	if signedAuth.Address != address {
		t.Fatalf("expected signedAuth.Address %q, got %q", address, signedAuth.Address)
	}
	if out.Amount != 5 {
		t.Fatalf("expected payload to be unmarshaled into out, got amount=%d", out.Amount)
	}
}

func TestRequireAdminAuthed_FallsBackToLegacyToken(t *testing.T) {
	resetApplyTestState()
	os.Unsetenv("ADMIN_WALLET_ADDRESS")
	t.Setenv("ADMIN_TOKEN", "test-secret")

	body, _ := json.Marshal(tokenMintData{Address: "somewhere", Amount: 5, Symbol: "CCT"})
	req := httptest.NewRequest("POST", "/token/mint", bytes.NewReader(body))
	req.Header.Set("X-Admin-Token", "test-secret")

	var out tokenMintData
	_, legacy, err := requireAdminAuthed(req, &out)
	if err != nil {
		t.Fatalf("expected the legacy token path to be accepted, got: %v", err)
	}
	if !legacy {
		t.Fatal("expected the legacy token path to be reported as used")
	}
	if out.Amount != 5 {
		t.Fatalf("expected payload to be unmarshaled into out, got amount=%d", out.Amount)
	}
}

func TestRequireAdminAuthed_TokenDoesNotWorkOnceAdminWalletConfigured(t *testing.T) {
	resetApplyTestState()

	_, address := adminSignedEnvelope(t, tokenMintData{Address: "somewhere", Amount: 5, Symbol: "CCT"})
	t.Setenv("ADMIN_WALLET_ADDRESS", address)
	t.Setenv("ADMIN_TOKEN", "test-secret")

	// A plain, unsigned body with a valid X-Admin-Token header - the
	// pre-migration credential - must be rejected once an admin wallet
	// is configured, not silently accepted into a transaction that
	// would just no-op at apply time.
	body, _ := json.Marshal(tokenMintData{Address: "somewhere", Amount: 5, Symbol: "CCT"})
	req := httptest.NewRequest("POST", "/token/mint", bytes.NewReader(body))
	req.Header.Set("X-Admin-Token", "test-secret")

	var out tokenMintData
	if _, _, err := requireAdminAuthed(req, &out); err == nil {
		t.Fatal("expected the legacy token credential to be rejected once an admin wallet is configured")
	}
}

func TestRequireAdminAuthed_RejectsNeitherCredential(t *testing.T) {
	resetApplyTestState()
	os.Unsetenv("ADMIN_WALLET_ADDRESS")
	os.Unsetenv("ADMIN_TOKEN")

	body, _ := json.Marshal(tokenMintData{Address: "somewhere", Amount: 5, Symbol: "CCT"})
	req := httptest.NewRequest("POST", "/token/mint", bytes.NewReader(body))

	var out tokenMintData
	if _, _, err := requireAdminAuthed(req, &out); err == nil {
		t.Fatal("expected a request with no admin credential at all to be rejected")
	}
}

func TestApplyGenesisPoolWithdraw_MovesFromKeylessPoolOnly(t *testing.T) {
	resetPoSRTestState()

	tokenStore.Mint(genesisPoolAddress(genesisPoolValidatorReward), 1000)

	data, _ := json.Marshal(genesisPoolWithdrawData{Pool: genesisPoolValidatorReward, To: "some-operator-address", Amount: 400})
	applyGenesisPoolWithdraw(core.Transaction{ID: "genesis-withdraw-1", Data: string(data)})

	if got := tokenStore.BalanceOf(genesisPoolAddress(genesisPoolValidatorReward)); got != 600 {
		t.Fatalf("expected pool balance reduced to 600, got %d", got)
	}
	if got := tokenStore.BalanceOf("some-operator-address"); got != 400 {
		t.Fatalf("expected destination balance 400, got %d", got)
	}
}

func TestApplyGenesisPoolWithdraw_RejectsFoundationTreasuryAndUnknownPools(t *testing.T) {
	resetPoSRTestState()

	treasuryStore.Treasury.FoundationAddress = "foundation-addr"
	tokenStore.Mint("foundation-addr", 1000)

	// Foundation Treasury must never be reachable through this path,
	// even though it's a real address with a real balance - it has its
	// own admin controls (HandleAdminSetTreasuryAddress etc.).
	data, _ := json.Marshal(genesisPoolWithdrawData{Pool: "foundation-addr", To: "attacker", Amount: 1000})
	applyGenesisPoolWithdraw(core.Transaction{ID: "genesis-withdraw-2", Data: string(data)})
	if got := tokenStore.BalanceOf("foundation-addr"); got != 1000 {
		t.Fatalf("expected Foundation Treasury balance untouched, got %d", got)
	}
	if got := tokenStore.BalanceOf("attacker"); got != 0 {
		t.Fatalf("expected no funds reached the attacker address, got %d", got)
	}

	// An arbitrary made-up pool name must also be rejected.
	tokenStore.Mint(genesisPoolAddress("NOT_A_REAL_POOL"), 1000)
	data2, _ := json.Marshal(genesisPoolWithdrawData{Pool: "NOT_A_REAL_POOL", To: "attacker", Amount: 1000})
	applyGenesisPoolWithdraw(core.Transaction{ID: "genesis-withdraw-3", Data: string(data2)})
	if got := tokenStore.BalanceOf(genesisPoolAddress("NOT_A_REAL_POOL")); got != 1000 {
		t.Fatalf("expected unknown-pool balance untouched, got %d", got)
	}
}

func TestApplyGenesisPoolWithdraw_InsufficientBalanceIsNoOp(t *testing.T) {
	resetPoSRTestState()

	tokenStore.Mint(genesisPoolAddress(genesisPoolEcosystemDev), 10)

	data, _ := json.Marshal(genesisPoolWithdrawData{Pool: genesisPoolEcosystemDev, To: "someone", Amount: 1000})
	applyGenesisPoolWithdraw(core.Transaction{ID: "genesis-withdraw-4", Data: string(data)})

	if got := tokenStore.BalanceOf(genesisPoolAddress(genesisPoolEcosystemDev)); got != 10 {
		t.Fatalf("expected insufficient-balance withdrawal to be a no-op, pool balance changed to %d", got)
	}
	if got := tokenStore.BalanceOf("someone"); got != 0 {
		t.Fatalf("expected recipient to receive nothing, got %d", got)
	}
}
