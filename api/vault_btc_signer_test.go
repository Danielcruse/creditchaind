package api

import (
	"encoding/asn1"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	gethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// TestKMSBtcSignatureReconstruction exercises the exact DER-parsing +
// low-S normalization + re-serialization logic kmsBtcVaultSigner.Sign
// uses, without needing a real KMS call - same technique as
// TestKMSSignatureRecovery (eth_signer_test.go): produce a real
// secp256k1 signature locally, force it into HIGH-S form (so the
// correction branch actually runs, not just the pass-through case),
// DER-encode it the way KMS's Sign API would return it, run it through
// the same reconstruction path kmsBtcVaultSigner.Sign uses, and confirm
// the result is a valid, low-S signature that verifies against the
// correct public key.
func TestKMSBtcSignatureReconstruction(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub := priv.PubKey()

	var hash [32]byte
	copy(hash[:], []byte("this is exactly 32 bytes long!!"))

	realSig := ecdsa.Sign(priv, hash[:])

	var parsedReal ecdsaDERSignature
	if _, err := asn1.Unmarshal(realSig.Serialize(), &parsedReal); err != nil {
		t.Fatalf("parse real signature DER: %v", err)
	}

	// Force S into HIGH-S form (real signatures from this library are
	// already low-S, so negate to get the form we want to test the
	// correction against) - simulates a raw KMS response, which makes no
	// low-S guarantee at all.
	secp256k1N := gethcrypto.S256().Params().N
	highS := new(big.Int).Sub(secp256k1N, parsedReal.S)

	derSig, err := asn1.Marshal(ecdsaDERSignature{R: parsedReal.R, S: highS})
	if err != nil {
		t.Fatalf("DER-encode signature: %v", err)
	}

	// --- Replicate kmsBtcVaultSigner.Sign's reconstruction logic ---
	var parsed ecdsaDERSignature
	if _, err := asn1.Unmarshal(derSig, &parsed); err != nil {
		t.Fatalf("parse DER signature: %v", err)
	}
	var rScalar, sScalar btcec.ModNScalar
	rScalar.SetByteSlice(padTo32(parsed.R.Bytes()))
	sScalar.SetByteSlice(padTo32(parsed.S.Bytes()))
	wasOverHalfOrder := sScalar.IsOverHalfOrder()
	if wasOverHalfOrder {
		sScalar.Negate()
	}
	reconstructed := ecdsa.NewSignature(&rScalar, &sScalar)

	if !wasOverHalfOrder {
		t.Fatal("test setup bug: the forced-high-S value wasn't actually over half order, so the correction branch was never exercised")
	}
	if !reconstructed.Verify(hash[:], pub) {
		t.Fatal("reconstructed signature does not verify against the correct public key - the KMS BTC signature reconstruction logic has a bug")
	}
	if sScalar.IsOverHalfOrder() {
		t.Fatal("expected the final S value to be low-S after correction")
	}
}

// TestKMSBtcVaultSigner_NewKeyProducesCompressedPublicKey confirms the
// compressed-pubkey derivation path kmsBtcVaultSigner.NewKey uses
// (parseKMSPublicKey + go-ethereum's CompressPubkey) produces the exact
// same 33-byte compressed key form core.BridgeValidator.BtcPublicKeyHex
// requires - built the same way KMS's GetPublicKey API returns a
// SubjectPublicKeyInfo DER encoding.
func TestKMSBtcVaultSigner_NewKeyProducesCompressedPublicKey(t *testing.T) {
	priv, err := gethcrypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	want := gethcrypto.CompressPubkey(&priv.PublicKey)

	rawPoint := gethcrypto.FromECDSAPub(&priv.PublicKey)
	der, err := asn1.Marshal(pkixPublicKeyInfo{
		Algorithm: asn1.RawValue{FullBytes: []byte{0x30, 0x00}}, // placeholder algorithm identifier - parseKMSPublicKey ignores this field entirely
		PublicKey: asn1.BitString{Bytes: rawPoint, BitLength: len(rawPoint) * 8},
	})
	if err != nil {
		t.Fatalf("DER-encode public key info: %v", err)
	}

	pub, err := parseKMSPublicKey(der)
	if err != nil {
		t.Fatalf("parseKMSPublicKey: %v", err)
	}
	got := gethcrypto.CompressPubkey(pub)

	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("compressed pubkey mismatch: got %x want %x", got, want)
	}
	if len(got) != 33 {
		t.Fatalf("expected a 33-byte compressed public key, got %d bytes", len(got))
	}
}
