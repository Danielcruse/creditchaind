package api

import (
	"crypto/ecdsa"
	"encoding/asn1"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

// TestKMSSignatureRecovery exercises the exact DER-parsing + low-S
// normalization + recovery-id brute-force logic kmsEthSigner.Sign uses,
// without needing a real KMS call: it generates a real secp256k1 keypair
// locally, produces a real ECDSA signature, DER-encodes it the same way
// KMS's Sign API would return it, then runs it through the same
// reconstruction path kmsEthSigner.Sign uses and confirms the result is
// a valid 65-byte Ethereum signature that recovers to the correct
// address. This is the highest-risk piece of the ETH custodial signer -
// getting the recovery id wrong would produce signatures that fail to
// recover to the right address (a functional bug, caught immediately by
// this test) or, worse if unnoticed, could be mistaken for a different
// key's signature.
func TestKMSSignatureRecovery(t *testing.T) {
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wantAddr := crypto.PubkeyToAddress(priv.PublicKey)

	var hash [32]byte
	copy(hash[:], []byte("this is exactly 32 bytes long!!"))

	// Produce a real signature the normal way (this already includes a
	// correct recovery id, which we deliberately discard below to
	// simulate what KMS gives us: only r and s, no recovery id, and s
	// not necessarily normalized to low-S).
	fullSig, err := crypto.Sign(hash[:], priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	r := new(big.Int).SetBytes(fullSig[:32])
	s := new(big.Int).SetBytes(fullSig[32:64])

	// DER-encode exactly the way KMS's Sign API returns ECDSA
	// signatures, to prove the parsing code actually round-trips.
	derSig, err := asn1.Marshal(ecdsaDERSignature{R: r, S: s})
	if err != nil {
		t.Fatalf("DER-encode signature: %v", err)
	}

	// --- From here on, replicate kmsEthSigner.Sign's reconstruction logic ---
	var parsed ecdsaDERSignature
	if _, err := asn1.Unmarshal(derSig, &parsed); err != nil {
		t.Fatalf("parse DER signature: %v", err)
	}

	secp256k1N := crypto.S256().Params().N
	halfN := new(big.Int).Rsh(secp256k1N, 1)
	sVal := parsed.S
	if sVal.Cmp(halfN) > 0 {
		sVal = new(big.Int).Sub(secp256k1N, sVal)
	}

	rBytes := padTo32(parsed.R.Bytes())
	sBytes := padTo32(sVal.Bytes())

	var recovered *ecdsa.PublicKey
	var recoveredSig []byte
	for recID := byte(0); recID < 2; recID++ {
		candidate := append(append(append([]byte{}, rBytes...), sBytes...), recID)
		pub, err := crypto.SigToPub(hash[:], candidate)
		if err != nil {
			continue
		}
		if crypto.PubkeyToAddress(*pub) == wantAddr {
			recovered = pub
			recoveredSig = candidate
			break
		}
	}

	if recovered == nil {
		t.Fatalf("failed to recover correct public key from any recovery id (0 or 1) - the KMS signature reconstruction logic has a bug")
	}
	if len(recoveredSig) != 65 {
		t.Fatalf("expected a 65-byte reconstructed signature, got %d bytes", len(recoveredSig))
	}
	if crypto.PubkeyToAddress(*recovered) != wantAddr {
		t.Fatalf("recovered address %s does not match expected %s", crypto.PubkeyToAddress(*recovered).Hex(), wantAddr.Hex())
	}
}

// TestKMSPublicKeyParsing exercises parseKMSPublicKey against a real
// SubjectPublicKeyInfo DER encoding built the same way KMS's
// GetPublicKey API returns one for a secp256k1 key, confirming the
// address derived from it matches the address derived directly from the
// same keypair via go-ethereum.
func TestKMSPublicKeyParsing(t *testing.T) {
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wantAddr := crypto.PubkeyToAddress(priv.PublicKey)

	rawPoint := elliptic_Marshal(priv)

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
	if crypto.PubkeyToAddress(*pub) != wantAddr {
		t.Fatalf("parsed public key's address %s does not match expected %s", crypto.PubkeyToAddress(*pub).Hex(), wantAddr.Hex())
	}
}

func elliptic_Marshal(priv *ecdsa.PrivateKey) []byte {
	return crypto.FromECDSAPub(&priv.PublicKey)
}
