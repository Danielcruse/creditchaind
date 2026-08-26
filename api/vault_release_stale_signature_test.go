package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestSignatureMatchesAddress_TrueForTheSigningKeysOwnAddress is the
// core regression test for the stuck-vault-release incident: a real
// signature over a real digest must recover to the address that
// actually produced it, using the same 27/28-to-0/1 recovery-id
// normalization production code applies.
func TestSignatureMatchesAddress_TrueForTheSigningKeysOwnAddress(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	digest := crypto.Keccak256([]byte("some release digest"))

	sig, err := crypto.Sign(digest, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sig[64] += 27 // production always stores/transmits the 27/28 form

	if !signatureMatchesAddress(digest, sig, addr) {
		t.Fatal("expected a genuine signature to match its own signer's address")
	}
}

// TestSignatureMatchesAddress_FalseForADifferentKey is the exact
// scenario that caused the real incident: a signature made by a
// DIFFERENT (e.g. since-rotated) key must NOT be reported as matching
// the current signing address, even though both are well-formed,
// validly-recoverable 65-byte signatures.
func TestSignatureMatchesAddress_FalseForADifferentKey(t *testing.T) {
	oldKey, _ := crypto.GenerateKey()
	newKey, _ := crypto.GenerateKey()
	newAddr := crypto.PubkeyToAddress(newKey.PublicKey)
	digest := crypto.Keccak256([]byte("some release digest"))

	staleSig, err := crypto.Sign(digest, oldKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	staleSig[64] += 27

	if signatureMatchesAddress(digest, staleSig, newAddr) {
		t.Fatal("expected a signature made by a different (old) key to NOT match the current signing address")
	}
}

func TestSignatureMatchesAddress_FalseForMalformedSignature(t *testing.T) {
	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey)
	digest := crypto.Keccak256([]byte("some release digest"))

	if signatureMatchesAddress(digest, []byte("not a real 65-byte signature"), addr) {
		t.Fatal("expected a malformed (wrong-length) signature to never match")
	}
}

func TestApplyVaultReleaseApprove_OverwritesStaleSignatureFromSameValidator(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-stale", "depositor-stale-pubkey"
	attestLock("ETH", "0xseed-stale", depositor, pubKey, "1000000000000000000", "val-a", 1000)
	attestLock("ETH", "0xseed-stale", depositor, pubKey, "1000000000000000000", "val-b", 1001)

	burnData, _ := json.Marshal(vaultBurnData{
		Chain:                 "ETH",
		DestinationExtAddress: "0xexternal",
		AmountSmallestUnit:    "1000000000000000000",
	})
	applyVaultBurn(core.Transaction{ID: "burn-stale", AuthAddress: depositor, AuthPublicKey: pubKey, Data: string(burnData), Timestamp: 1002})
	advancePastVaultReleaseConfirmationDepth(t)

	staleSig := hex.EncodeToString([]byte("stale-signature-from-a-rotated-key"))
	approveData1, _ := json.Marshal(vaultReleaseApproveData{BurnTxId: "burn-stale", SignatureHex: staleSig})
	applyVaultReleaseApprove(core.Transaction{AuthAddress: "val-a", Data: string(approveData1), Timestamp: 1003})

	approval, _ := vaultReleaseApprovalStore.Get("burn-stale")
	if hex.EncodeToString(approval.Signatures["val-a"]) != staleSig {
		t.Fatalf("expected the first (stale) signature to be recorded, got %x", approval.Signatures["val-a"])
	}

	// The same validator (val-a) resubmits with a fresh signature - e.g.
	// after rotating VAULT_RELEASE_SIGNING_KEY_HEX and the auto-sign
	// loop noticing the stored one no longer matches (see
	// signatureMatchesAddress). Before the fix, applyVaultReleaseApprove
	// silently refused this because an entry already existed under
	// "val-a" - permanently wedging the approval.
	freshSig := hex.EncodeToString([]byte("fresh-signature-from-the-new-key"))
	approveData2, _ := json.Marshal(vaultReleaseApproveData{BurnTxId: "burn-stale", SignatureHex: freshSig})
	applyVaultReleaseApprove(core.Transaction{AuthAddress: "val-a", Data: string(approveData2), Timestamp: 1004})

	approval, _ = vaultReleaseApprovalStore.Get("burn-stale")
	if len(approval.Signatures) != 1 {
		t.Fatalf("expected still exactly 1 entry for val-a (overwritten, not appended), got %d", len(approval.Signatures))
	}
	if hex.EncodeToString(approval.Signatures["val-a"]) != freshSig {
		t.Fatalf("expected the stale signature to be overwritten by the fresh one, got %x", approval.Signatures["val-a"])
	}
}
