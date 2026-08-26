package api

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestLocalHexEd25519OracleSigner_IdentityMatchesKeypair confirms the
// wrapped-existing-behavior signer derives the same address/public key
// core.GenerateAddress always has for a given Ed25519 keypair - a pure
// refactor regression check (vault_gas_price_oracle_signer.go moved
// this logic out of the old vaultGasPriceOracleSigningKey, it must not
// have changed behavior).
func TestLocalHexEd25519OracleSigner_IdentityMatchesKeypair(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer := &localHexEd25519OracleSigner{priv: priv}

	address, pubHex, err := signer.identity(context.Background())
	if err != nil {
		t.Fatalf("identity: %v", err)
	}

	wantAddress := core.GenerateAddress(pub)
	if address != wantAddress {
		t.Fatalf("expected address %s, got %s", wantAddress, address)
	}
	if pubHex != hex.EncodeToString(pub) {
		t.Fatalf("expected public key %s, got %s", hex.EncodeToString(pub), pubHex)
	}
}

// TestLocalHexEd25519OracleSigner_SignProducesVerifiableSignature
// confirms a signature produced through the signer interface actually
// verifies against the same message and public key - the whole point
// of routing submitVaultGasPriceOracleUpdate through this abstraction
// is that applyVaultGasPriceOracleUpdate's independent signature
// verification must still pass, identically to before this refactor.
func TestLocalHexEd25519OracleSigner_SignProducesVerifiableSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer := &localHexEd25519OracleSigner{priv: priv}

	message := []byte("test message for the gas price oracle signer")
	sig, err := signer.sign(context.Background(), message)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if !core.VerifySignature(pub, message, sig) {
		t.Fatal("expected the signature to verify against the message and public key")
	}
}

// TestResolveVaultGasPriceOracleSigner_FallsBackToHexKeyWhenKMSUnset
// confirms the default (no KMS env vars set) path resolves to the
// original raw-hex-key behavior - existing deployments that only ever
// set VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX must keep working exactly
// as before this KMS option was added.
func TestResolveVaultGasPriceOracleSigner_FallsBackToHexKeyWhenKMSUnset(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	t.Setenv("VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX", hex.EncodeToString(priv))
	t.Setenv("VAULT_GAS_PRICE_ORACLE_KMS_ENABLED", "")

	signer, ok := resolveVaultGasPriceOracleSigner()
	if !ok {
		t.Fatal("expected a signer to resolve from a valid hex key")
	}
	if _, ok := signer.(*localHexEd25519OracleSigner); !ok {
		t.Fatalf("expected a *localHexEd25519OracleSigner, got %T", signer)
	}
}

// TestResolveVaultGasPriceOracleSigner_RejectsInvalidHexKey preserves
// the original behavior of vaultGasPriceOracleSigningKey: an
// unparseable or wrong-length key must not resolve to a signer, not
// silently fall through to something unsafe.
func TestResolveVaultGasPriceOracleSigner_RejectsInvalidHexKey(t *testing.T) {
	t.Setenv("VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX", "not-valid-hex")
	t.Setenv("VAULT_GAS_PRICE_ORACLE_KMS_ENABLED", "")

	if _, ok := resolveVaultGasPriceOracleSigner(); ok {
		t.Fatal("expected an invalid hex key to fail to resolve a signer")
	}
}

// TestResolveVaultGasPriceOracleSigner_NoKeyConfiguredAtAll confirms
// the graceful-degrade posture (no signer, caller no-ops) when neither
// KMS nor the hex env var is set.
func TestResolveVaultGasPriceOracleSigner_NoKeyConfiguredAtAll(t *testing.T) {
	t.Setenv("VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX", "")
	t.Setenv("VAULT_GAS_PRICE_ORACLE_KMS_ENABLED", "")

	if _, ok := resolveVaultGasPriceOracleSigner(); ok {
		t.Fatal("expected no signer to resolve when nothing is configured")
	}
}

// TestResolveVaultGasPriceOracleSigner_KMSEnabledButNoKeyIDFallsBackToHex
// covers the misconfiguration case: an operator sets
// VAULT_GAS_PRICE_ORACLE_KMS_ENABLED=true but forgets
// VAULT_GAS_PRICE_ORACLE_KMS_KEY_ID - this must fall back to the hex
// key (if one is also set) rather than silently resolving no signer
// at all, matching InitEthSigner's identical "degrade gracefully"
// precedent for the ETH custodial corner.
func TestResolveVaultGasPriceOracleSigner_KMSEnabledButNoKeyIDFallsBackToHex(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	t.Setenv("VAULT_GAS_PRICE_ORACLE_KMS_ENABLED", "true")
	t.Setenv("VAULT_GAS_PRICE_ORACLE_KMS_KEY_ID", "")
	t.Setenv("VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX", hex.EncodeToString(priv))

	signer, ok := resolveVaultGasPriceOracleSigner()
	if !ok {
		t.Fatal("expected a fallback signer to resolve despite KMS being enabled without a key ID")
	}
	if _, ok := signer.(*localHexEd25519OracleSigner); !ok {
		t.Fatalf("expected a *localHexEd25519OracleSigner fallback, got %T", signer)
	}
}

// TestSubmitVaultGasPriceOracleUpdate_ProducesAnIndependentlyVerifiableTx
// is the end-to-end regression test: the transaction
// submitVaultGasPriceOracleUpdate records through the signer
// abstraction must still pass verifyTxAuth exactly as it did through
// the old direct-key-signing code, and applyVaultGasPriceOracleUpdate
// must actually accept and apply it.
func TestSubmitVaultGasPriceOracleUpdate_ProducesAnIndependentlyVerifiableTx(t *testing.T) {
	resetApplyTestState()
	resetVaultReleaseGasPriceWeiForTest(t)

	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	address := core.GenerateAddress(pub)

	t.Setenv("VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX", hex.EncodeToString(priv))
	t.Setenv("VAULT_GAS_PRICE_ORACLE_KMS_ENABLED", "")
	t.Setenv("VAULT_GAS_PRICE_ORACLE_ADDRESS", "cc"+address)

	mempool.Clear()
	// The bounded-step clamp (clampVaultGasPriceOracleUpdate) re-applies
	// at apply time regardless of what's submitted - that's a feature,
	// not something this test should fight. Compute the same expected,
	// clamped result rather than asserting the raw submitted value.
	before := governanceParamsStore.GetOrDefault("VaultReleaseGasPriceWei", DefaultVaultReleaseGasPriceWei)
	wantAfter := clampVaultGasPriceOracleUpdate(before, 100_000_000_000)
	if wantAfter == before {
		t.Fatalf("test setup issue: expected the submitted value to actually move VaultReleaseGasPriceWei off its current value %d", before)
	}

	submitVaultGasPriceOracleUpdate(100_000_000_000)

	if len(mempool.Transactions) != 1 {
		t.Fatalf("expected exactly one transaction recorded, got %d", len(mempool.Transactions))
	}
	tx := mempool.Transactions[0]
	applyTxOnce(tx)

	if got := governanceParamsStore.Snapshot()["VaultReleaseGasPriceWei"]; got != wantAfter {
		t.Fatalf("expected the applied update to set VaultReleaseGasPriceWei=%d, got %d - signature must not have verified", wantAfter, got)
	}
}
