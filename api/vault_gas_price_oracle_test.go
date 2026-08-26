package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// resetVaultReleaseGasPriceWeiForTest gives a test a known-clean
// "never set" starting point for this one key - governanceParamsStore
// is package-level state resetApplyTestState does NOT reset (other
// tests, e.g. vault_fee_test.go, rely on their own overrides of this
// exact key surviving only within their own test), so without this a
// test run order where an earlier test last touched
// VaultReleaseGasPriceWei could leave a stale value or a stale SetAt
// timestamp (and thus a stale cooldown window) bleeding into this file's
// tests. Wipes the WHOLE store to its pre-test snapshot on cleanup, not
// just this one key, restoring everything exactly as it was.
func resetVaultReleaseGasPriceWeiForTest(t *testing.T) {
	t.Helper()
	beforeParams := governanceParamsStore.Snapshot()
	beforeSetAt := governanceParamsStore.SnapshotSetAt()
	t.Cleanup(func() {
		governanceParamsStore.Restore(beforeParams)
		governanceParamsStore.RestoreSetAt(beforeSetAt)
	})

	cleanParams := make(map[string]uint64, len(beforeParams))
	for k, v := range beforeParams {
		if k != "VaultReleaseGasPriceWei" {
			cleanParams[k] = v
		}
	}
	cleanSetAt := make(map[string]int64, len(beforeSetAt))
	for k, v := range beforeSetAt {
		if k != "VaultReleaseGasPriceWei" {
			cleanSetAt[k] = v
		}
	}
	governanceParamsStore.Restore(cleanParams)
	governanceParamsStore.RestoreSetAt(cleanSetAt)
}

func TestClampVaultGasPriceOracleUpdate_StepLimitedWithinRange(t *testing.T) {
	// current=20 Gwei, proposed a huge jump (200 Gwei) - must be capped
	// at current + 50%, not allowed straight through.
	current := uint64(20_000_000_000)
	proposed := uint64(200_000_000_000)
	got := clampVaultGasPriceOracleUpdate(current, proposed)
	want := current + current*VaultGasPriceOracleMaxStepBasisPoints/10_000 // +50%
	if got != want {
		t.Fatalf("expected step-limited result %d, got %d", want, got)
	}
}

func TestClampVaultGasPriceOracleUpdate_StepLimitedDownward(t *testing.T) {
	current := uint64(20_000_000_000)
	proposed := uint64(1) // a huge drop
	got := clampVaultGasPriceOracleUpdate(current, proposed)
	want := current - current*VaultGasPriceOracleMaxStepBasisPoints/10_000 // -50%
	if got != want {
		t.Fatalf("expected step-limited result %d, got %d", want, got)
	}
}

func TestClampVaultGasPriceOracleUpdate_WithinStepPassesThrough(t *testing.T) {
	current := uint64(20_000_000_000)
	proposed := uint64(21_000_000_000) // +5%, well within the 50% step
	got := clampVaultGasPriceOracleUpdate(current, proposed)
	if got != proposed {
		t.Fatalf("expected a within-step proposal to pass through unchanged, got %d want %d", got, proposed)
	}
}

func TestClampVaultGasPriceOracleUpdate_NeverExceedsAbsoluteCeiling(t *testing.T) {
	// Even a step-limited result must never cross the absolute
	// ceiling, regardless of how high current already is.
	current := VaultGasPriceOracleMaxWei
	proposed := VaultGasPriceOracleMaxWei * 100
	got := clampVaultGasPriceOracleUpdate(current, proposed)
	if got > VaultGasPriceOracleMaxWei {
		t.Fatalf("expected result capped at the absolute ceiling %d, got %d", VaultGasPriceOracleMaxWei, got)
	}
}

func TestClampVaultGasPriceOracleUpdate_NeverBelowAbsoluteFloor(t *testing.T) {
	current := VaultGasPriceOracleMinWei
	proposed := uint64(0)
	got := clampVaultGasPriceOracleUpdate(current, proposed)
	if got < VaultGasPriceOracleMinWei {
		t.Fatalf("expected result floored at the absolute minimum %d, got %d", VaultGasPriceOracleMinWei, got)
	}
}

func TestClampVaultGasPriceOracleUpdate_CanRecoverFromZeroCurrent(t *testing.T) {
	// A pathological current=0 (shouldn't normally happen, but must
	// never permanently trap the value at 0 via a 0% step) - the
	// fallback minimum step must still let it move.
	got := clampVaultGasPriceOracleUpdate(0, 1_000_000_000)
	if got == 0 {
		t.Fatalf("expected the value to be able to recover from a current of 0, got stuck at 0")
	}
}

// buildOracleUpdateTx builds a genuinely wallet-signed
// TxVaultGasPriceOracleUpdate transaction exactly as
// submitVaultGasPriceOracleUpdate would, but from an arbitrary keypair
// (not necessarily the configured oracle address) - lets tests exercise
// both the authorized and unauthorized address cases.
func buildOracleUpdateTx(t *testing.T, priv []byte, pub []byte, newValueWei uint64, ts int64) core.Transaction {
	t.Helper()
	address := core.GenerateAddress(pub)
	data := vaultGasPriceOracleUpdateData{NewValueWei: newValueWei}
	payloadJSON, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	env := core.SignedEnvelope{
		Address:   address,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: ts,
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(priv, env.CanonicalMessage())
	return core.Transaction{
		ID:            "oracle-update-tx-" + address,
		From:          address,
		Type:          core.TxVaultGasPriceOracleUpdate,
		Data:          string(payloadJSON),
		Timestamp:     ts,
		AuthAddress:   address,
		AuthPublicKey: hex.EncodeToString(pub),
		AuthTimestamp: ts,
		AuthPayload:   payloadJSON,
		AuthSignature: hex.EncodeToString(sig),
	}
}

func TestApplyVaultGasPriceOracleUpdate_RejectsUnauthorizedAddress(t *testing.T) {
	resetApplyTestState()
	resetVaultReleaseGasPriceWeiForTest(t)
	pub, priv, _ := core.GenerateKeyPair()
	authorizedPub, _, _ := core.GenerateKeyPair()
	t.Setenv("VAULT_GAS_PRICE_ORACLE_ADDRESS", "cc"+core.GenerateAddress(authorizedPub))

	tx := buildOracleUpdateTx(t, priv, pub, 5_000_000_000, 1000)
	applyVaultGasPriceOracleUpdate(tx)

	if got := governanceParamsStore.GetOrDefault("VaultReleaseGasPriceWei", DefaultVaultReleaseGasPriceWei); got != DefaultVaultReleaseGasPriceWei {
		t.Fatalf("expected an update from an unauthorized address to be ignored, got %d", got)
	}
}

func TestApplyVaultGasPriceOracleUpdate_AcceptsAuthorizedAddressWithinBounds(t *testing.T) {
	resetApplyTestState()
	resetVaultReleaseGasPriceWeiForTest(t)
	pub, priv, _ := core.GenerateKeyPair()
	address := core.GenerateAddress(pub)
	t.Setenv("VAULT_GAS_PRICE_ORACLE_ADDRESS", "cc"+address)

	tx := buildOracleUpdateTx(t, priv, pub, 21_000_000_000, 1000) // +5% of the 20 Gwei default
	applyVaultGasPriceOracleUpdate(tx)

	if got := governanceParamsStore.GetOrDefault("VaultReleaseGasPriceWei", DefaultVaultReleaseGasPriceWei); got != 21_000_000_000 {
		t.Fatalf("expected the within-bounds update to apply, got %d", got)
	}
}

func TestApplyVaultGasPriceOracleUpdate_ClampsAnOutOfBoundsClaim(t *testing.T) {
	resetApplyTestState()
	resetVaultReleaseGasPriceWeiForTest(t)
	pub, priv, _ := core.GenerateKeyPair()
	address := core.GenerateAddress(pub)
	t.Setenv("VAULT_GAS_PRICE_ORACLE_ADDRESS", "cc"+address)

	// Claims an absurd 10,000 Gwei - must be clamped, never applied as-is.
	tx := buildOracleUpdateTx(t, priv, pub, 10_000_000_000_000, 1000)
	applyVaultGasPriceOracleUpdate(tx)

	got := governanceParamsStore.GetOrDefault("VaultReleaseGasPriceWei", DefaultVaultReleaseGasPriceWei)
	want := clampVaultGasPriceOracleUpdate(DefaultVaultReleaseGasPriceWei, 10_000_000_000_000)
	if got != want {
		t.Fatalf("expected the claim to be re-clamped to %d, got %d (raw claim was never bounds-checked)", want, got)
	}
	if got == 10_000_000_000_000 {
		t.Fatalf("the unbounded claim was applied verbatim - this is a real fund-safety bug")
	}
}

func TestApplyVaultGasPriceOracleUpdate_EnforcesMinimumInterval(t *testing.T) {
	resetApplyTestState()
	resetVaultReleaseGasPriceWeiForTest(t)
	pub, priv, _ := core.GenerateKeyPair()
	address := core.GenerateAddress(pub)
	t.Setenv("VAULT_GAS_PRICE_ORACLE_ADDRESS", "cc"+address)

	first := buildOracleUpdateTx(t, priv, pub, 21_000_000_000, 1000)
	applyVaultGasPriceOracleUpdate(first)
	if got := governanceParamsStore.GetOrDefault("VaultReleaseGasPriceWei", DefaultVaultReleaseGasPriceWei); got != 21_000_000_000 {
		t.Fatalf("test setup invalid: expected the first update to apply, got %d", got)
	}

	// A second update arriving well inside the cooldown window must be
	// rejected outright, regardless of how it's addressed/signed - this
	// is what actually bounds the damage a leaked key (submitted
	// straight to /tx/submit, not through this node's own loop) can do.
	tooSoon := buildOracleUpdateTx(t, priv, pub, 25_000_000_000, 1000+VaultGasPriceOracleMinIntervalSeconds-1)
	applyVaultGasPriceOracleUpdate(tooSoon)
	if got := governanceParamsStore.GetOrDefault("VaultReleaseGasPriceWei", DefaultVaultReleaseGasPriceWei); got != 21_000_000_000 {
		t.Fatalf("expected the too-soon update to be rejected, but the value changed to %d", got)
	}

	// One arriving right at (or past) the cooldown boundary must succeed.
	onTime := buildOracleUpdateTx(t, priv, pub, 22_000_000_000, 1000+VaultGasPriceOracleMinIntervalSeconds)
	applyVaultGasPriceOracleUpdate(onTime)
	if got := governanceParamsStore.GetOrDefault("VaultReleaseGasPriceWei", DefaultVaultReleaseGasPriceWei); got != 22_000_000_000 {
		t.Fatalf("expected the update at the cooldown boundary to apply, got %d", got)
	}
}

func TestVerifyTxAuth_VaultGasPriceOracleUpdate_RejectsTamperedValue(t *testing.T) {
	resetApplyTestState()
	pub, priv, _ := core.GenerateKeyPair()

	tx := buildOracleUpdateTx(t, priv, pub, 21_000_000_000, 1000)
	// Tamper with the claimed Data after signing, exactly the
	// signature-valid-but-effect-altered attack this codebase's own
	// verifyTxAuth split (signed vs claimed) exists to catch.
	tamperedData, _ := json.Marshal(vaultGasPriceOracleUpdateData{NewValueWei: 999_000_000_000})
	tx.Data = string(tamperedData)

	if verifyTxAuth(tx) {
		t.Fatalf("expected verifyTxAuth to reject a transaction whose Data was altered after signing")
	}
}
