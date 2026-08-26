package api

import (
	"context"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

// TestLocalDevEthSigner_NewKeyAndSign verifies the dev-only fallback
// signer's full round trip: a key it generates and hands back an
// address for actually produces signatures that recover to that same
// address.
func TestLocalDevEthSigner_NewKeyAndSign(t *testing.T) {
	signer := newLocalDevEthSigner()
	ctx := context.Background()

	keyID, addr, err := signer.NewKey(ctx)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}

	var hash [32]byte
	copy(hash[:], []byte("another exactly-32-byte digest!"))

	sig, err := signer.Sign(ctx, keyID, hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != 65 {
		t.Fatalf("expected 65-byte signature, got %d", len(sig))
	}

	pub, err := crypto.SigToPub(hash[:], sig)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	if crypto.PubkeyToAddress(*pub).Hex() != addr {
		t.Fatalf("signature recovers to %s, expected %s", crypto.PubkeyToAddress(*pub).Hex(), addr)
	}

	// Signing with an unknown key ID must fail clearly, not panic or
	// silently succeed.
	if _, err := signer.Sign(ctx, "nonexistent-key", hash); err == nil {
		t.Fatalf("expected an error signing with an unknown key ID")
	}
}
