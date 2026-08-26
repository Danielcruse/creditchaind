package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// buildRecoverWalletTx builds a well-formed, otherwise-valid
// TxRecoverWallet at the given timestamp - the exact request shape a
// real recovering user (or, per audit finding C-04, an attacker who
// merely knows the target's linked email and account year) would have
// submitted before wallet recovery was discontinued.
func buildRecoverWalletTx(t *testing.T, oldAddress, email string, claimedYear int, code string, timestamp int64) core.Transaction {
	t.Helper()
	newPub, newPriv, _ := core.GenerateKeyPair()
	newAddress := core.GenerateAddress(newPub)

	payload := recoverWalletData{
		OldAddress:  oldAddress,
		Email:       email,
		ClaimedYear: claimedYear,
		Code:        code,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	env := core.SignedEnvelope{
		Address:   newAddress,
		PublicKey: hex.EncodeToString(newPub),
		Timestamp: timestamp,
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(newPriv, env.CanonicalMessage())

	return core.Transaction{
		ID:            "recover-tx-" + oldAddress,
		From:          newAddress,
		Type:          core.TxRecoverWallet,
		Data:          string(payloadJSON),
		Timestamp:     env.Timestamp,
		AuthAddress:   newAddress,
		AuthPublicKey: env.PublicKey,
		AuthTimestamp: env.Timestamp,
		AuthPayload:   payloadJSON,
		AuthSignature: hex.EncodeToString(sig),
	}
}

// setUpRecoverableWallet seeds a wallet + verified recovery email the
// same way a real user would have set up before losing their key -
// deliberately NOT the vulnerability itself, just realistic
// pre-existing state for the test to attack (or legitimately use).
func setUpRecoverableWallet(oldAddress, email string, createdYear int) {
	w := walletStore.Get(oldAddress)
	w.CreatedAt = time.Date(createdYear, 6, 1, 0, 0, 0, 0, time.UTC).Unix()
	recoveryStore.SetEmail(oldAddress, email)
}

func TestApplyRecoverWallet_BlockedOnMainnet(t *testing.T) {
	resetApplyTestState()
	t.Setenv("MAINNET_MODE", "true")

	const oldAddress = "recover-old-1"
	setUpRecoverableWallet(oldAddress, "victim@example.com", 2025)

	// Note: this doesn't even need the real code - that's the whole
	// point of C-04 (the code was never checked at apply time to begin
	// with). Using an obviously-wrong one anyway to show the block
	// doesn't depend on it.
	tx := buildRecoverWalletTx(t, oldAddress, "victim@example.com", 2025, "wrong-code", 5000)
	applyTxOnce(tx)

	if recoveryStore.IsMigrated(oldAddress) {
		t.Fatalf("expected wallet recovery to be blocked on mainnet, but the old address was migrated")
	}
}

// TestApplyRecoverWallet_HistoricalPreCutoverTxStillReplays is the
// backward-compatibility regression test for
// WalletRecoveryDiscontinuedCutoverUnixSeconds: a node resyncing
// testnet's real chain history from genesis must still correctly
// replay recovery migrations that genuinely happened before wallet
// recovery was discontinued, timestamped 5000 - deliberately, absurdly
// far before the cutover, the same way a real testnet transaction from
// mid-2026 is.
func TestApplyRecoverWallet_HistoricalPreCutoverTxStillReplays(t *testing.T) {
	resetApplyTestState()
	// MAINNET_MODE deliberately left unset - default/testnet behavior.

	const oldAddress = "recover-old-2"
	setUpRecoverableWallet(oldAddress, "victim2@example.com", 2024)

	tx := buildRecoverWalletTx(t, oldAddress, "victim2@example.com", 2024, "any-code", 5000)
	applyTxOnce(tx)

	if !recoveryStore.IsMigrated(oldAddress) {
		t.Fatalf("expected a pre-cutover-timestamped recovery transaction to still replay correctly off mainnet")
	}
}

// TestApplyRecoverWallet_DiscontinuedOnTestnetToo is the actual
// regression test for the 2026-08-16 product decision: wallet recovery
// is not just a mainnet security hold (C-04) - it's discontinued
// everywhere. A NEW transaction (timestamped at/after the cutover)
// must be rejected on testnet too, not just mainnet.
func TestApplyRecoverWallet_DiscontinuedOnTestnetToo(t *testing.T) {
	resetApplyTestState()
	// MAINNET_MODE deliberately left unset.

	const oldAddress = "recover-old-3"
	setUpRecoverableWallet(oldAddress, "victim3@example.com", 2024)

	tx := buildRecoverWalletTx(t, oldAddress, "victim3@example.com", 2024, "any-code", WalletRecoveryDiscontinuedCutoverUnixSeconds)
	applyTxOnce(tx)

	if recoveryStore.IsMigrated(oldAddress) {
		t.Fatalf("expected a post-cutover recovery transaction to be rejected on testnet too - wallet recovery is discontinued everywhere, not just held on mainnet")
	}
}

// TestApplySetRecoveryEmail_DiscontinuedOnTestnetToo covers the setup
// step too, not just the final migration - linking a new recovery
// email is pointless (and shouldn't silently appear to work) once the
// migration step can never actually run.
func TestApplySetRecoveryEmail_DiscontinuedOnTestnetToo(t *testing.T) {
	resetApplyTestState()

	const address = "recover-setup-1"
	data, _ := json.Marshal(recoveryEmailRequestData{Email: "new@example.com"})
	tx := core.Transaction{
		ID:        "set-recovery-email-1",
		From:      address,
		Type:      core.TxSetRecoveryEmail,
		Data:      string(data),
		Timestamp: WalletRecoveryDiscontinuedCutoverUnixSeconds,
	}
	applySetRecoveryEmail(tx)

	if _, ok := recoveryStore.Get(address); ok {
		t.Fatalf("expected a post-cutover TxSetRecoveryEmail to be rejected - wallet recovery setup is discontinued too")
	}
}

// postToRecoveryHandler is a small helper for exercising the
// now-uniformly-discontinued HTTP handlers below.
func postToRecoveryHandler(t *testing.T, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/irrelevant", bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// TestRecoveryHandlers_AllReturnDiscontinued is the regression test
// for the 2026-08-16 product decision to stop offering wallet recovery
// entirely: every recovery HTTP endpoint must fail fast with a clear
// 410 Gone and the discontinued-feature explanation, on every mode,
// not just mainnet (see the old, narrower
// TestHandleRecoverWallet_RejectsOnMainnet this replaces).
func TestRecoveryHandlers_AllReturnDiscontinued(t *testing.T) {
	resetApplyTestState()

	handlers := map[string]http.HandlerFunc{
		"HandleRequestEmailCode":    HandleRequestEmailCode,
		"HandleConfirmEmail":        HandleConfirmEmail,
		"HandleRequestRecoveryCode": HandleRequestRecoveryCode,
		"HandleRecoverWallet":       HandleRecoverWallet,
	}

	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			rec := postToRecoveryHandler(t, handler)
			if rec.Code != http.StatusGone {
				t.Fatalf("expected %s to return 410 Gone, got %d: %s", name, rec.Code, rec.Body.String())
			}
			if rec.Body.String() != walletRecoveryDiscontinuedMessage+"\n" {
				t.Fatalf("expected %s to return the discontinued-feature message, got: %s", name, rec.Body.String())
			}
		})
	}
}

// TestRecoveryHandlers_DiscontinuedRegardlessOfMainnetMode confirms
// the discontinuation isn't just C-04's narrower mainnet-only gate -
// it's unconditional, checked before MAINNET_MODE is even read.
func TestRecoveryHandlers_DiscontinuedRegardlessOfMainnetMode(t *testing.T) {
	resetApplyTestState()
	t.Setenv("MAINNET_MODE", "true")

	rec := postToRecoveryHandler(t, HandleRecoverWallet)
	if rec.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone on mainnet too, got %d: %s", rec.Code, rec.Body.String())
	}
}
