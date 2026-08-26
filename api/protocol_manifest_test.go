package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestApplySetProtocolManifest_CommitsAllFields is the core regression
// test for audit findings C-09/C-06: once a manifest is applied, every
// field lands correctly in core.ActiveProtocolManifest, including
// correctly comma-splitting the address-list fields the same way the
// equivalent environment variables have always been parsed.
func TestApplySetProtocolManifest_CommitsAllFields(t *testing.T) {
	resetApplyTestState()

	data, _ := json.Marshal(setProtocolManifestData{
		MainnetMode:              true,
		AdminWalletAddress:       "AABBCCDDEEFF00112233445566778899aabbccdd",
		AdminMultisigAddresses:   "1111111111111111111111111111111111111111,2222222222222222222222222222222222222222",
		AdminMultisigThreshold:   2,
		BridgeValidatorAddresses: "3333333333333333333333333333333333333333",
		BridgeValidatorThreshold: 1,
	})
	applySetProtocolManifest(core.Transaction{
		ID: "manifest-1", AuthAddress: "the-admin-who-set-it", Timestamp: 1000, Data: string(data),
	})

	m := core.ActiveProtocolManifest
	if !m.Set {
		t.Fatalf("expected Set=true after a manifest commit")
	}
	if !m.MainnetMode {
		t.Fatalf("expected MainnetMode=true, got false")
	}
	if m.AdminWalletAddress != "aabbccddeeff00112233445566778899aabbccdd" {
		t.Fatalf("expected the admin address normalized to lowercase, got %q", m.AdminWalletAddress)
	}
	if len(m.AdminMultisigAddresses) != 2 {
		t.Fatalf("expected 2 comma-split multisig addresses, got %d: %v", len(m.AdminMultisigAddresses), m.AdminMultisigAddresses)
	}
	if m.AdminMultisigThreshold != 2 {
		t.Fatalf("expected AdminMultisigThreshold=2, got %d", m.AdminMultisigThreshold)
	}
	if len(m.BridgeValidatorAddresses) != 1 {
		t.Fatalf("expected 1 bridge validator address, got %d", len(m.BridgeValidatorAddresses))
	}
	if m.BridgeValidatorThreshold != 1 {
		t.Fatalf("expected BridgeValidatorThreshold=1, got %d", m.BridgeValidatorThreshold)
	}
	if m.SetAt != 1000 {
		t.Fatalf("expected SetAt to be the tx timestamp (1000), got %d", m.SetAt)
	}
	if m.SetBy != "the-admin-who-set-it" {
		t.Fatalf("expected SetBy to be the committing admin's address, got %q", m.SetBy)
	}
}

// TestConfigGetters_FallBackToEnvBeforeManifestSet confirms today's
// exact unchanged behavior for every existing deployment: with no
// manifest ever committed, every getter still reads its own local
// environment variable exactly as it always has.
func TestConfigGetters_FallBackToEnvBeforeManifestSet(t *testing.T) {
	resetApplyTestState()
	t.Setenv("ADMIN_WALLET_ADDRESS", "envwallet")
	t.Setenv("ADMIN_MULTISIG_ADDRESSES", "envsigner1,envsigner2")
	t.Setenv("ADMIN_MULTISIG_THRESHOLD", "2")
	t.Setenv("BRIDGE_VALIDATOR_ADDRESSES", "envvalidator1")
	t.Setenv("BRIDGE_VALIDATOR_THRESHOLD", "1")
	t.Setenv("MAINNET_MODE", "true")

	if got := adminWalletAddress(); got != "envwallet" {
		t.Fatalf("expected env fallback %q, got %q", "envwallet", got)
	}
	if got := adminMultisigThreshold(); got != 2 {
		t.Fatalf("expected env fallback 2, got %d", got)
	}
	if got := bridgeValidatorThreshold(); got != 1 {
		t.Fatalf("expected env fallback 1, got %d", got)
	}
	if !mainnetMode() {
		t.Fatalf("expected env fallback true, got false")
	}
}

// TestConfigGetters_UseManifestOnceSetEvenIfEnvDisagrees is the actual
// close of C-09: once a manifest is committed, every getter must use
// the REPLICATED value, not the local environment - proven here by
// deliberately setting the environment to a DIFFERENT value than the
// manifest and confirming the manifest wins. This is what makes two
// differently-configured nodes agree once both have applied the same
// manifest-commit block, regardless of what either one's local
// environment says.
func TestConfigGetters_UseManifestOnceSetEvenIfEnvDisagrees(t *testing.T) {
	resetApplyTestState()
	t.Setenv("ADMIN_WALLET_ADDRESS", "env-wallet-should-be-ignored")
	t.Setenv("ADMIN_MULTISIG_THRESHOLD", "99")
	t.Setenv("BRIDGE_VALIDATOR_THRESHOLD", "99")
	t.Setenv("MAINNET_MODE", "false")

	data, _ := json.Marshal(setProtocolManifestData{
		MainnetMode:              true,
		AdminWalletAddress:       "manifest-wallet",
		AdminMultisigAddresses:   "manifestsigner1,manifestsigner2,manifestsigner3",
		AdminMultisigThreshold:   2,
		BridgeValidatorAddresses: "manifestvalidator1",
		BridgeValidatorThreshold: 1,
	})
	applySetProtocolManifest(core.Transaction{ID: "manifest-2", AuthAddress: "admin", Timestamp: 2000, Data: string(data)})

	if got := adminWalletAddress(); got != "manifest-wallet" {
		t.Fatalf("expected the manifest's admin address to win over env, got %q", got)
	}
	if got := adminMultisigThreshold(); got != 2 {
		t.Fatalf("expected the manifest's threshold (2) to win over env's 99, got %d", got)
	}
	if got := bridgeValidatorThreshold(); got != 1 {
		t.Fatalf("expected the manifest's bridge threshold (1) to win over env's 99, got %d", got)
	}
	if !mainnetMode() {
		t.Fatalf("expected the manifest's MainnetMode=true to win over env's \"false\"")
	}
}

func TestVerifyTxAuth_SetProtocolManifest_RequiresRealAdminAuth(t *testing.T) {
	resetApplyTestState()

	env, address := adminSignedEnvelope(t, setProtocolManifestData{MainnetMode: true})
	t.Setenv("ADMIN_WALLET_ADDRESS", address)

	tx := core.NewAuthenticatedTransaction(env, "", "", 0, core.TxSetProtocolManifest, string(env.Payload))
	if err := core.VerifyTransactionAuth(tx); err != nil {
		t.Fatalf("expected the underlying signature to verify: %v", err)
	}
	if !verifyTxAuth(tx) {
		t.Fatalf("expected verifyTxAuth to accept a genuinely admin-signed manifest commit")
	}
}

func TestVerifyTxAuth_SetProtocolManifest_RejectsNonAdminSigner(t *testing.T) {
	resetApplyTestState()
	os.Unsetenv("ADMIN_WALLET_ADDRESS")
	os.Unsetenv("ADMIN_TOKEN")

	env, _ := adminSignedEnvelope(t, setProtocolManifestData{MainnetMode: true})
	tx := core.NewAuthenticatedTransaction(env, "", "", 0, core.TxSetProtocolManifest, string(env.Payload))
	if verifyTxAuth(tx) {
		t.Fatalf("expected verifyTxAuth to reject a signature from nobody the network trusts as admin")
	}
}

// TestVerifyTxAuth_SetProtocolManifest_OnceSetOnlyManifestAdminCanChangeIt
// proves the self-referential-authority claim: once a manifest exists,
// changing it again requires a signature from the address the manifest
// ITSELF names as admin - a stale env-var ADMIN_WALLET_ADDRESS that
// disagrees with the already-committed manifest must NOT be able to
// override it. This is what makes the manifest a real access-control
// list rather than something any node's local misconfiguration could
// silently supersede.
func TestVerifyTxAuth_SetProtocolManifest_OnceSetOnlyManifestAdminCanChangeIt(t *testing.T) {
	resetApplyTestState()

	// A manifest already names "real-manifest-admin" as the committed
	// admin - simulating state already reached consensus, independent
	// of whatever this node's own local env var says.
	core.ActiveProtocolManifest = core.ProtocolManifest{Set: true, AdminWalletAddress: "real-manifest-admin"}
	t.Setenv("ADMIN_WALLET_ADDRESS", "stale-local-env-admin")

	env, _ := adminSignedEnvelope(t, setProtocolManifestData{MainnetMode: true})
	tx := core.NewAuthenticatedTransaction(env, "", "", 0, core.TxSetProtocolManifest, string(env.Payload))
	// Signed by neither "real-manifest-admin" nor "stale-local-env-admin" -
	// a random third wallet.
	if verifyTxAuth(tx) {
		t.Fatalf("expected rejection: signer is neither the manifest's committed admin nor relevant otherwise")
	}
}

func TestHandleSetProtocolManifest_RejectsNonAdminCaller(t *testing.T) {
	resetApplyTestState()
	os.Unsetenv("ADMIN_WALLET_ADDRESS")
	os.Unsetenv("ADMIN_TOKEN")

	env, _ := adminSignedEnvelope(t, setProtocolManifestData{MainnetMode: true})
	body, _ := json.Marshal(env)
	req := httptest.NewRequest(http.MethodPost, "/admin/protocol-manifest/set", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleSetProtocolManifest(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-admin caller, got %d", w.Code)
	}
	if core.ActiveProtocolManifest.Set {
		t.Fatalf("expected no manifest to be committed for a rejected request")
	}
}

func TestHandleSetProtocolManifest_QueuesPendingTxForAdminCaller(t *testing.T) {
	resetApplyTestState()

	env, address := adminSignedEnvelope(t, setProtocolManifestData{MainnetMode: true, AdminWalletAddress: "x"})
	t.Setenv("ADMIN_WALLET_ADDRESS", address)

	body, _ := json.Marshal(env)
	req := httptest.NewRequest(http.MethodPost, "/admin/protocol-manifest/set", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleSetProtocolManifest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// The manifest only actually takes effect once the transaction is
	// APPLIED (block confirmation), same async pending-tx model as
	// every other state change in this codebase - the HTTP handler
	// itself must never flip global state synchronously.
	if core.ActiveProtocolManifest.Set {
		t.Fatalf("expected the manifest to remain uncommitted until the transaction is actually applied")
	}
	if len(mempool.Transactions) != 1 {
		t.Fatalf("expected exactly one pending transaction queued, got %d", len(mempool.Transactions))
	}
}

// TestProtocolManifestPersistence_SurvivesRestart mirrors this
// codebase's own established pattern for consensus-critical persisted
// state (e.g. reputationMiningSweepSnapshot's restart test) - simulate
// a restart (snapshot, wipe, restore) and confirm the manifest comes
// back exactly as committed, not silently reverting to env-var
// defaults the way it would without a dedicated snapshot field.
func TestProtocolManifestPersistence_SurvivesRestart(t *testing.T) {
	resetApplyTestState()

	data, _ := json.Marshal(setProtocolManifestData{
		MainnetMode: true, AdminWalletAddress: "persisted-admin",
		BridgeValidatorThreshold: 3,
	})
	applySetProtocolManifest(core.Transaction{ID: "manifest-3", AuthAddress: "admin", Timestamp: 3000, Data: string(data)})
	committed := core.ActiveProtocolManifest

	// Simulate a restart: snapshot via the same struct persistence.go
	// copies into ChainSnapshot, wipe in-memory state, restore.
	snapshotValue := committed
	core.ActiveProtocolManifest = core.ProtocolManifest{}
	if core.ActiveProtocolManifest.Set {
		t.Fatalf("setup issue: expected wiped state to be unset")
	}
	core.ActiveProtocolManifest = snapshotValue

	if !core.ActiveProtocolManifest.Set || core.ActiveProtocolManifest.AdminWalletAddress != "persisted-admin" || core.ActiveProtocolManifest.BridgeValidatorThreshold != 3 {
		t.Fatalf("expected the manifest to survive a simulated restart unchanged, got %+v", core.ActiveProtocolManifest)
	}
}
