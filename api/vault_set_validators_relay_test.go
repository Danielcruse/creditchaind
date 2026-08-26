package api

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestBuildSetValidatorsDigestGoMatchesManualEncoding independently
// re-derives the expected keccak256(abi.encode("SET_VALIDATORS",
// newValidators, newThreshold, actionNonce, address(this), chainid))
// bytes by hand-assembling the dynamic-type ABI layout (head offsets +
// tail data) and checks buildSetValidatorsDigestGo produces the
// identical digest. This is the one property that actually matters: a
// mismatch against contracts/CreditChainVault.sol's own on-chain
// setValidators() digest would mean every signature this code collects
// verifies against the wrong message and the real transaction reverts
// with "invalid signature", exactly the class of bug this test exists
// to catch before it ever reaches a live contract call.
func TestBuildSetValidatorsDigestGoMatchesManualEncoding(t *testing.T) {
	newValidators := []common.Address{
		common.HexToAddress("0x82c4e688d52f4a6a1a2fede737d29cb5ff25495d"),
		common.HexToAddress("0x7864A654505F2e2D7E5c2FE33014dF2C25c2Fee4"),
	}
	newThreshold := big.NewInt(1)
	actionNonce := big.NewInt(1785700000)
	vaultContract := common.HexToAddress("0xeFC496d4D0E4b2e4071eb4079f7B5FbEc8d0b511")
	chainID := big.NewInt(1)

	got, err := buildSetValidatorsDigestGo(newValidators, newThreshold, actionNonce, vaultContract, chainID)
	if err != nil {
		t.Fatalf("buildSetValidatorsDigestGo returned an error: %v", err)
	}

	// --- Manual head/tail ABI encoding, built independently of abi.Arguments.Pack ---
	head := make([]byte, 0, 192)
	tail := make([]byte, 0)

	// arg0 (string "SET_VALIDATORS"): dynamic - head holds an offset,
	// tail holds 32-byte length + right-padded UTF-8 data.
	strOffset := 192 // 6 head slots x 32 bytes
	head = append(head, common.LeftPadBytes(big.NewInt(int64(strOffset)).Bytes(), 32)...)
	strBytes := []byte("SET_VALIDATORS")
	tail = append(tail, common.LeftPadBytes(big.NewInt(int64(len(strBytes))).Bytes(), 32)...)
	tail = append(tail, common.RightPadBytes(strBytes, 32)...) // 14 bytes pads to one 32-byte word

	// arg1 (address[] newValidators): dynamic - head holds an offset,
	// tail holds 32-byte length + N x 32-byte left-padded addresses.
	arrOffset := strOffset + 32 + 32 // + length word + one padded data word for the string
	head = append(head, common.LeftPadBytes(big.NewInt(int64(arrOffset)).Bytes(), 32)...)
	arrTail := make([]byte, 0)
	arrTail = append(arrTail, common.LeftPadBytes(big.NewInt(int64(len(newValidators))).Bytes(), 32)...)
	for _, v := range newValidators {
		arrTail = append(arrTail, common.LeftPadBytes(v.Bytes(), 32)...)
	}
	tail = append(tail, arrTail...)

	// arg2-5: all static, inline in the head.
	head = append(head, common.LeftPadBytes(newThreshold.Bytes(), 32)...)
	head = append(head, common.LeftPadBytes(actionNonce.Bytes(), 32)...)
	head = append(head, common.LeftPadBytes(vaultContract.Bytes(), 32)...)
	head = append(head, common.LeftPadBytes(chainID.Bytes(), 32)...)

	manual := append(head, tail...)
	want := crypto.Keccak256(manual)

	if !bytes.Equal(got, want) {
		t.Fatalf("digest mismatch:\n got  %x\n want %x", got, want)
	}
}

// TestBuildSetValidatorsDigestGoRecoversEachSignerAddress signs the
// digest with two independent keys and confirms crypto.SigToPub
// recovers each exact signing address back out - the real end-to-end
// property CreditChainVault.sol's _requireThresholdSignatures depends
// on for counting DISTINCT valid signers toward threshold.
func TestBuildSetValidatorsDigestGoRecoversEachSignerAddress(t *testing.T) {
	key1, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	key2, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	wantAddr1 := crypto.PubkeyToAddress(key1.PublicKey)
	wantAddr2 := crypto.PubkeyToAddress(key2.PublicKey)

	digest, err := buildSetValidatorsDigestGo(
		[]common.Address{wantAddr1, wantAddr2},
		big.NewInt(2),
		big.NewInt(1),
		common.HexToAddress("0xeFC496d4D0E4b2e4071eb4079f7B5FbEc8d0b511"),
		big.NewInt(1),
	)
	if err != nil {
		t.Fatalf("buildSetValidatorsDigestGo: %v", err)
	}

	sig1, err := crypto.Sign(digest, key1)
	if err != nil {
		t.Fatalf("crypto.Sign key1: %v", err)
	}
	sig2, err := crypto.Sign(digest, key2)
	if err != nil {
		t.Fatalf("crypto.Sign key2: %v", err)
	}

	recoveredPub1, err := crypto.SigToPub(digest, sig1)
	if err != nil {
		t.Fatalf("SigToPub sig1: %v", err)
	}
	if got := crypto.PubkeyToAddress(*recoveredPub1); got != wantAddr1 {
		t.Fatalf("recovered address %s does not match signer1 %s", got.Hex(), wantAddr1.Hex())
	}

	recoveredPub2, err := crypto.SigToPub(digest, sig2)
	if err != nil {
		t.Fatalf("SigToPub sig2: %v", err)
	}
	if got := crypto.PubkeyToAddress(*recoveredPub2); got != wantAddr2 {
		t.Fatalf("recovered address %s does not match signer2 %s", got.Hex(), wantAddr2.Hex())
	}
}
