package api

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestBuildReleaseApprovalDigestGoMatchesManualEncoding independently
// re-derives the expected keccak256(abi.encode(...)) bytes via plain
// 32-byte left-padding (bypassing abi.Arguments.Pack entirely) and
// checks buildReleaseApprovalDigestGo produces the identical digest -
// the one property that actually matters here: a mismatch against
// website/js/vault.js's buildReleaseApprovalDigest would mean every
// auto-signed approval silently fails to verify against a
// browser-signed one, without any error ever surfacing until releases
// mysteriously never reach threshold.
func TestBuildReleaseApprovalDigestGoMatchesManualEncoding(t *testing.T) {
	to := common.HexToAddress("0x17f730eca0d53e2C61dd6b74Cbd3f068b596D746")
	token := common.Address{} // ETH: zero address
	amount := big.NewInt(497500000000000)
	releaseID := new(big.Int).SetUint64(13453968096500691182)
	vaultContract := common.HexToAddress("0xeFC496d4D0E4b2e4071eb4079f7B5FbEc8d0b511")
	chainID := big.NewInt(1)

	got, err := buildReleaseApprovalDigestGo(to, token, amount, releaseID, vaultContract, chainID)
	if err != nil {
		t.Fatalf("buildReleaseApprovalDigestGo returned an error: %v", err)
	}

	var manual []byte
	manual = append(manual, common.LeftPadBytes(to.Bytes(), 32)...)
	manual = append(manual, common.LeftPadBytes(token.Bytes(), 32)...)
	manual = append(manual, common.LeftPadBytes(amount.Bytes(), 32)...)
	manual = append(manual, common.LeftPadBytes(releaseID.Bytes(), 32)...)
	manual = append(manual, common.LeftPadBytes(vaultContract.Bytes(), 32)...)
	manual = append(manual, common.LeftPadBytes(chainID.Bytes(), 32)...)
	want := crypto.Keccak256(manual)

	if !bytes.Equal(got, want) {
		t.Fatalf("digest mismatch:\n got  %x\n want %x", got, want)
	}
}

// TestBuildReleaseApprovalDigestGoRecoversSignerAddress signs the
// digest and confirms crypto.SigToPub recovers the exact signing
// address back out - the real end-to-end property the vault contract's
// own ecrecover(digest, v, r, s) depends on, not just "the bytes look
// plausible."
func TestBuildReleaseApprovalDigestGoRecoversSignerAddress(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	wantAddr := crypto.PubkeyToAddress(key.PublicKey)

	digest, err := buildReleaseApprovalDigestGo(
		common.HexToAddress("0x17f730eca0d53e2C61dd6b74Cbd3f068b596D746"),
		common.Address{},
		big.NewInt(1000),
		big.NewInt(1),
		common.HexToAddress("0xeFC496d4D0E4b2e4071eb4079f7B5FbEc8d0b511"),
		big.NewInt(1),
	)
	if err != nil {
		t.Fatalf("buildReleaseApprovalDigestGo: %v", err)
	}

	sig, err := crypto.Sign(digest, key)
	if err != nil {
		t.Fatalf("crypto.Sign: %v", err)
	}
	sig[64] += 27

	// ecrecover-style recovery expects the raw 0/1 recovery id, not the
	// 27/28-normalized one this file submits on-chain - undo the +27
	// shift for this recovery-only check, same as any ecrecover caller
	// would.
	recoverSig := make([]byte, 65)
	copy(recoverSig, sig)
	recoverSig[64] -= 27

	recoveredPub, err := crypto.SigToPub(digest, recoverSig)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	gotAddr := crypto.PubkeyToAddress(*recoveredPub)

	if gotAddr != wantAddr {
		t.Fatalf("recovered address %s does not match signer %s", gotAddr.Hex(), wantAddr.Hex())
	}
}
