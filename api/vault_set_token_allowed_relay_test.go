package api

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestBuildSetTokenAllowedDigestGoMatchesManualEncoding independently
// re-derives the expected keccak256(abi.encode("SET_TOKEN_ALLOWED",
// token, allowed, actionNonce, address(this), chainid)) bytes by
// hand-assembling the ABI layout and checks
// buildSetTokenAllowedDigestGo produces the identical digest - same
// reasoning as TestBuildSetValidatorsDigestGoMatchesManualEncoding: a
// mismatch here means every collected signature verifies against the
// wrong message and the real setTokenAllowed() call reverts with
// "invalid signature" the moment it's actually broadcast against the
// live vault contract.
func TestBuildSetTokenAllowedDigestGoMatchesManualEncoding(t *testing.T) {
	token := common.HexToAddress("0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84") // real Lido stETH mainnet address
	allowed := true
	actionNonce := big.NewInt(1785700001)
	vaultContract := common.HexToAddress("0xeFC496d4D0E4b2e4071eb4079f7B5FbEc8d0b511")
	chainID := big.NewInt(1)

	got, err := buildSetTokenAllowedDigestGo(token, allowed, actionNonce, vaultContract, chainID)
	if err != nil {
		t.Fatalf("buildSetTokenAllowedDigestGo returned an error: %v", err)
	}

	// All 6 args are static except the leading string literal - head is
	// exactly 6 x 32-byte slots, tail holds only the string's length +
	// right-padded data.
	head := make([]byte, 0, 192)
	tail := make([]byte, 0)

	strOffset := 192
	head = append(head, common.LeftPadBytes(big.NewInt(int64(strOffset)).Bytes(), 32)...)
	strBytes := []byte("SET_TOKEN_ALLOWED")
	tail = append(tail, common.LeftPadBytes(big.NewInt(int64(len(strBytes))).Bytes(), 32)...)
	tail = append(tail, common.RightPadBytes(strBytes, 32)...)

	head = append(head, common.LeftPadBytes(token.Bytes(), 32)...)
	boolWord := make([]byte, 32)
	if allowed {
		boolWord[31] = 1
	}
	head = append(head, boolWord...)
	head = append(head, common.LeftPadBytes(actionNonce.Bytes(), 32)...)
	head = append(head, common.LeftPadBytes(vaultContract.Bytes(), 32)...)
	head = append(head, common.LeftPadBytes(chainID.Bytes(), 32)...)

	manual := append(head, tail...)
	want := crypto.Keccak256(manual)

	if !bytes.Equal(got, want) {
		t.Fatalf("digest mismatch:\n got  %x\n want %x", got, want)
	}
}

// TestBuildSetPerDepositorCapsDigestGoMatchesManualEncoding mirrors
// the test above for the other new self-governance digest -
// keccak256(abi.encode("SET_PER_DEPOSITOR_CAPS", newCapWei,
// newCapUsdc, actionNonce, address(this), chainid)).
func TestBuildSetPerDepositorCapsDigestGoMatchesManualEncoding(t *testing.T) {
	newCapWei := big.NewInt(0)
	newCapUsdc := big.NewInt(0)
	actionNonce := big.NewInt(1785700002)
	vaultContract := common.HexToAddress("0xeFC496d4D0E4b2e4071eb4079f7B5FbEc8d0b511")
	chainID := big.NewInt(1)

	got, err := buildSetPerDepositorCapsDigestGo(newCapWei, newCapUsdc, actionNonce, vaultContract, chainID)
	if err != nil {
		t.Fatalf("buildSetPerDepositorCapsDigestGo returned an error: %v", err)
	}

	head := make([]byte, 0, 192)
	tail := make([]byte, 0)

	strOffset := 192
	head = append(head, common.LeftPadBytes(big.NewInt(int64(strOffset)).Bytes(), 32)...)
	strBytes := []byte("SET_PER_DEPOSITOR_CAPS")
	tail = append(tail, common.LeftPadBytes(big.NewInt(int64(len(strBytes))).Bytes(), 32)...)
	tail = append(tail, common.RightPadBytes(strBytes, 32)...)

	head = append(head, common.LeftPadBytes(newCapWei.Bytes(), 32)...)
	head = append(head, common.LeftPadBytes(newCapUsdc.Bytes(), 32)...)
	head = append(head, common.LeftPadBytes(actionNonce.Bytes(), 32)...)
	head = append(head, common.LeftPadBytes(vaultContract.Bytes(), 32)...)
	head = append(head, common.LeftPadBytes(chainID.Bytes(), 32)...)

	manual := append(head, tail...)
	want := crypto.Keccak256(manual)

	if !bytes.Equal(got, want) {
		t.Fatalf("digest mismatch:\n got  %x\n want %x", got, want)
	}
}

// TestBuildSetTokenAllowedDigestGoRecoversSignerAddress is the
// end-to-end property _requireThresholdSignatures actually depends on -
// same as TestBuildSetValidatorsDigestGoRecoversEachSignerAddress.
func TestBuildSetTokenAllowedDigestGoRecoversSignerAddress(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	wantAddr := crypto.PubkeyToAddress(key.PublicKey)

	digest, err := buildSetTokenAllowedDigestGo(
		common.HexToAddress("0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84"),
		true,
		big.NewInt(1),
		common.HexToAddress("0xeFC496d4D0E4b2e4071eb4079f7B5FbEc8d0b511"),
		big.NewInt(1),
	)
	if err != nil {
		t.Fatalf("buildSetTokenAllowedDigestGo: %v", err)
	}

	sig, err := crypto.Sign(digest, key)
	if err != nil {
		t.Fatalf("crypto.Sign: %v", err)
	}
	recoveredPub, err := crypto.SigToPub(digest, sig)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	if got := crypto.PubkeyToAddress(*recoveredPub); got != wantAddr {
		t.Fatalf("recovered address %s does not match signer %s", got.Hex(), wantAddr.Hex())
	}
}

func TestPackSetTokenAllowedCalldata_RoundTrips(t *testing.T) {
	token := common.HexToAddress("0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84")
	calldata, err := packSetTokenAllowedCalldata(token, true, big.NewInt(42), [][]byte{make([]byte, 65)})
	if err != nil {
		t.Fatalf("packSetTokenAllowedCalldata: %v", err)
	}
	method, err := vaultContractABI.MethodById(calldata[:4])
	if err != nil {
		t.Fatalf("MethodById: %v", err)
	}
	if method.Name != "setTokenAllowed" {
		t.Fatalf("expected setTokenAllowed selector, got %s", method.Name)
	}
}

func TestPackSetPerDepositorCapsCalldata_RoundTrips(t *testing.T) {
	calldata, err := packSetPerDepositorCapsCalldata(big.NewInt(0), big.NewInt(0), big.NewInt(42), [][]byte{make([]byte, 65)})
	if err != nil {
		t.Fatalf("packSetPerDepositorCapsCalldata: %v", err)
	}
	method, err := vaultContractABI.MethodById(calldata[:4])
	if err != nil {
		t.Fatalf("MethodById: %v", err)
	}
	if method.Name != "setPerDepositorCaps" {
		t.Fatalf("expected setPerDepositorCaps selector, got %s", method.Name)
	}
}
