package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestApplyVaultReleaseComplete_FlipsStatusAndRecordsHash(t *testing.T) {
	resetApplyTestState()

	vaultReleaseApprovalStore.Create(&core.VaultReleaseApproval{
		ID:     "burn-tx-1",
		Chain:  "ETH",
		Status: core.VaultReleasePending,
	})

	data, _ := json.Marshal(vaultReleaseCompleteData{BurnTxId: "burn-tx-1", ExternalHash: "0xrealtx"})
	tx := core.Transaction{ID: "complete-1", Data: string(data), Timestamp: 12345}

	applyVaultReleaseComplete(tx)

	approval, ok := vaultReleaseApprovalStore.Get("burn-tx-1")
	if !ok {
		t.Fatal("expected the approval to still exist")
	}
	if approval.Status != core.VaultReleaseReleased {
		t.Fatalf("expected status RELEASED, got %s", approval.Status)
	}
	if approval.ExternalReleaseTxHash != "0xrealtx" {
		t.Fatalf("expected external hash 0xrealtx, got %s", approval.ExternalReleaseTxHash)
	}
	if approval.ReleasedAt != 12345 {
		t.Fatalf("expected releasedAt 12345, got %d", approval.ReleasedAt)
	}
}

// TestApplyVaultReleaseComplete_IdempotentAgainstAReplayedReport confirms
// a second report for an already-released approval (e.g. two relay nodes
// both broadcasting and both self-reporting, or a chain replay from
// genesis) doesn't overwrite the original outcome with a different one.
func TestApplyVaultReleaseComplete_IdempotentAgainstAReplayedReport(t *testing.T) {
	resetApplyTestState()

	vaultReleaseApprovalStore.Create(&core.VaultReleaseApproval{
		ID:     "burn-tx-2",
		Chain:  "BTC",
		Status: core.VaultReleasePending,
	})

	first, _ := json.Marshal(vaultReleaseCompleteData{BurnTxId: "burn-tx-2", ExternalHash: "first-real-txid"})
	applyVaultReleaseComplete(core.Transaction{Data: string(first), Timestamp: 100})

	second, _ := json.Marshal(vaultReleaseCompleteData{BurnTxId: "burn-tx-2", ExternalHash: "different-txid-from-a-second-relay"})
	applyVaultReleaseComplete(core.Transaction{Data: string(second), Timestamp: 200})

	approval, _ := vaultReleaseApprovalStore.Get("burn-tx-2")
	if approval.ExternalReleaseTxHash != "first-real-txid" {
		t.Fatalf("expected the FIRST report to win, got %s", approval.ExternalReleaseTxHash)
	}
	if approval.ReleasedAt != 100 {
		t.Fatalf("expected releasedAt from the first report (100), got %d", approval.ReleasedAt)
	}
}

func TestApplyVaultReleaseComplete_RejectsMissingFieldsAndUnknownApproval(t *testing.T) {
	resetApplyTestState()

	// Missing fields - no crash, no-op.
	applyVaultReleaseComplete(core.Transaction{Data: `{"burnTxId":"","externalHash":""}`})

	// Unknown approval ID - no crash, no-op.
	data, _ := json.Marshal(vaultReleaseCompleteData{BurnTxId: "never-existed", ExternalHash: "0xabc"})
	applyVaultReleaseComplete(core.Transaction{Data: string(data)})

	if _, ok := vaultReleaseApprovalStore.Get("never-existed"); ok {
		t.Fatal("expected no approval to have been created for an unknown ID")
	}
}

func TestResolveRelayerSigner_RejectsMalformedOrUnsetHex(t *testing.T) {
	t.Setenv("RELAYER_KMS_ENABLED", "")

	t.Setenv("RELAYER_PRIVATE_KEY_HEX", "")
	if _, ok := resolveRelayerSigner(); ok {
		t.Fatal("expected an unset key to be rejected")
	}

	t.Setenv("RELAYER_PRIVATE_KEY_HEX", "not-valid-hex-at-all")
	if _, ok := resolveRelayerSigner(); ok {
		t.Fatal("expected malformed hex to be rejected")
	}

	// A real, valid secp256k1 private key (32 bytes) should be accepted
	// and produce a real 0x-prefixed address.
	t.Setenv("RELAYER_PRIVATE_KEY_HEX", "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318")
	signer, ok := resolveRelayerSigner()
	if !ok {
		t.Fatal("expected a valid 32-byte hex key to be accepted")
	}
	if _, isLocal := signer.(*localHexRelayerSigner); !isLocal {
		t.Fatalf("expected a *localHexRelayerSigner, got %T", signer)
	}
	addr, err := signer.address(context.Background())
	if err != nil {
		t.Fatalf("address: %v", err)
	}
	if len(addr) != 42 || addr[:2] != "0x" {
		t.Fatalf("expected a 0x-prefixed 20-byte address, got %s", addr)
	}
}

// TestResolveRelayerSigner_KMSEnabledButNoKeyIDFallsBackToHex mirrors
// resolveVaultGasPriceOracleSigner's identical misconfiguration test -
// an operator setting RELAYER_KMS_ENABLED=true without also setting
// RELAYER_KMS_KEY_ID must fall back to the hex key, not silently
// resolve no signer at all.
func TestResolveRelayerSigner_KMSEnabledButNoKeyIDFallsBackToHex(t *testing.T) {
	t.Setenv("RELAYER_KMS_ENABLED", "true")
	t.Setenv("RELAYER_KMS_KEY_ID", "")
	t.Setenv("RELAYER_PRIVATE_KEY_HEX", "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318")

	signer, ok := resolveRelayerSigner()
	if !ok {
		t.Fatal("expected a fallback signer to resolve despite KMS being enabled without a key ID")
	}
	if _, isLocal := signer.(*localHexRelayerSigner); !isLocal {
		t.Fatalf("expected a *localHexRelayerSigner fallback, got %T", signer)
	}
}

// TestLocalHexRelayerSigner_SignProducesRecoverableSignature confirms
// a signature produced through the relayerSigner interface actually
// recovers to the same address the signer itself reports - the whole
// point of routing every relayer call site through this interface is
// that a real, on-chain-verifiable ECDSA signature comes out the other
// end, identically whether backed by KMS or a raw hex key.
func TestLocalHexRelayerSigner_SignProducesRecoverableSignature(t *testing.T) {
	t.Setenv("RELAYER_KMS_ENABLED", "")
	t.Setenv("RELAYER_PRIVATE_KEY_HEX", "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318")

	signer, ok := resolveRelayerSigner()
	if !ok {
		t.Fatal("expected the signer to resolve")
	}
	ctx := context.Background()
	addr, err := signer.address(ctx)
	if err != nil {
		t.Fatalf("address: %v", err)
	}

	var hash [32]byte
	copy(hash[:], []byte("test message for the relayer signer, padded"))

	sig, err := signer.sign(ctx, hash)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	recoveredPub, err := crypto.SigToPub(hash[:], sig)
	if err != nil {
		t.Fatalf("recover pubkey from signature: %v", err)
	}
	if got := crypto.PubkeyToAddress(*recoveredPub).Hex(); got != addr {
		t.Fatalf("expected the signature to recover to %s, got %s", addr, got)
	}
}

func TestVaultReleaseTxVsize_ScalesWithThresholdAndRedeemScriptLength(t *testing.T) {
	small := vaultReleaseTxVsize(2, 71, false)
	larger := vaultReleaseTxVsize(3, 105, false)
	if larger <= small {
		t.Fatalf("expected a higher threshold + longer redeem script to increase estimated vsize, got small=%d larger=%d", small, larger)
	}

	withChange := vaultReleaseTxVsize(2, 71, true)
	if withChange <= small {
		t.Fatalf("expected adding a change output to increase estimated vsize, got noChange=%d withChange=%d", small, withChange)
	}
}

func TestSelectVaultReleaseUtxo_PicksSmallestSufficientDeterministically(t *testing.T) {
	utxos := []BtcUtxo{
		{Txid: "b", Vout: 0, Value: 500000, Status: struct {
			Confirmed   bool   `json:"confirmed"`
			BlockHeight uint32 `json:"block_height"`
		}{Confirmed: true}},
		{Txid: "a", Vout: 0, Value: 200000, Status: struct {
			Confirmed   bool   `json:"confirmed"`
			BlockHeight uint32 `json:"block_height"`
		}{Confirmed: true}},
		{Txid: "c", Vout: 0, Value: 100000, Status: struct {
			Confirmed   bool   `json:"confirmed"`
			BlockHeight uint32 `json:"block_height"`
		}{Confirmed: false}}, // unconfirmed - must never be selected
	}

	selected, fee, ok := selectVaultReleaseUtxo(utxos, 150000, 2, 71)
	if !ok {
		t.Fatal("expected a sufficient UTXO to be found")
	}
	if selected.Txid != "a" {
		t.Fatalf("expected the smallest SUFFICIENT confirmed UTXO (txid a, 200000 sats) to be selected, got %s (%d sats)", selected.Txid, selected.Value)
	}
	if fee == 0 {
		t.Fatal("expected a nonzero estimated fee")
	}

	// Repeated calls with the identical input must return the identical
	// answer - this determinism is load-bearing (see this file's own doc
	// comment: every node must independently reconstruct the same
	// unsigned transaction).
	selectedAgain, feeAgain, _ := selectVaultReleaseUtxo(utxos, 150000, 2, 71)
	if selectedAgain.Txid != selected.Txid || selectedAgain.Vout != selected.Vout || feeAgain != fee {
		t.Fatal("expected selectVaultReleaseUtxo to be fully deterministic across repeated calls")
	}
}

func TestSelectVaultReleaseUtxo_ReturnsNotOkWhenNothingSufficient(t *testing.T) {
	utxos := []BtcUtxo{
		{Txid: "a", Vout: 0, Value: 1000, Status: struct {
			Confirmed   bool   `json:"confirmed"`
			BlockHeight uint32 `json:"block_height"`
		}{Confirmed: true}},
	}
	if _, _, ok := selectVaultReleaseUtxo(utxos, 1_000_000, 2, 71); ok {
		t.Fatal("expected no UTXO to be sufficient for an amount far exceeding the only available UTXO")
	}
}
