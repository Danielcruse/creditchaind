package api

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestVaultContractABI_ParsesCleanlyAtInit is a basic sanity check that
// the hand-written vaultContractABIJSON constant (api/vault_contract_abi.go)
// is well-formed - the package-level init() already panics on a
// malformed ABI, so a clean test binary compile/run is itself evidence
// this passed, but asserting the parsed method/event set directly
// documents the expectation explicitly.
func TestVaultContractABI_ParsesCleanlyAtInit(t *testing.T) {
	for _, name := range []string{"deposit", "depositERC20", "release"} {
		if _, ok := vaultContractABI.Methods[name]; !ok {
			t.Fatalf("expected method %q to be present in the parsed ABI", name)
		}
	}
	if _, ok := vaultContractABI.Events["Locked"]; !ok {
		t.Fatal("expected the Locked event to be present in the parsed ABI")
	}
}

func TestPackDepositCalldata_StartsWithTheCorrectSelectorAndRoundTrips(t *testing.T) {
	var ccAddr [20]byte
	copy(ccAddr[:], bytes.Repeat([]byte{0xAB}, 20))
	var ccPub [32]byte
	copy(ccPub[:], bytes.Repeat([]byte{0xCD}, 32))

	data, err := packDepositCalldata(ccAddr, ccPub)
	if err != nil {
		t.Fatalf("packDepositCalldata failed: %v", err)
	}

	wantSelector := vaultContractABI.Methods["deposit"].ID
	if !bytes.Equal(data[:4], wantSelector) {
		t.Fatalf("expected calldata to start with the deposit() selector %x, got %x", wantSelector, data[:4])
	}

	unpacked, err := vaultContractABI.Methods["deposit"].Inputs.Unpack(data[4:])
	if err != nil {
		t.Fatalf("failed to unpack deposit calldata: %v", err)
	}
	gotAddr, ok := unpacked[0].([20]byte)
	if !ok || gotAddr != ccAddr {
		t.Fatalf("expected creditChainAddress to round-trip as %x, got %v", ccAddr, unpacked[0])
	}
	gotPub, ok := unpacked[1].([32]byte)
	if !ok || gotPub != ccPub {
		t.Fatalf("expected creditChainPublicKey to round-trip as %x, got %v", ccPub, unpacked[1])
	}
}

func TestPackDepositERC20Calldata_RoundTrips(t *testing.T) {
	token := common.HexToAddress("0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238")
	amount := big.NewInt(123456789)
	var ccAddr [20]byte
	copy(ccAddr[:], bytes.Repeat([]byte{0x11}, 20))
	var ccPub [32]byte
	copy(ccPub[:], bytes.Repeat([]byte{0x22}, 32))

	data, err := packDepositERC20Calldata(token, amount, ccAddr, ccPub)
	if err != nil {
		t.Fatalf("packDepositERC20Calldata failed: %v", err)
	}

	unpacked, err := vaultContractABI.Methods["depositERC20"].Inputs.Unpack(data[4:])
	if err != nil {
		t.Fatalf("failed to unpack depositERC20 calldata: %v", err)
	}
	if got := unpacked[0].(common.Address); got != token {
		t.Fatalf("expected token %s, got %s", token, got)
	}
	if got := unpacked[1].(*big.Int); got.Cmp(amount) != 0 {
		t.Fatalf("expected amount %s, got %s", amount, got)
	}
}

func TestPackReleaseCalldata_RoundTripsSignaturesArray(t *testing.T) {
	to := common.HexToAddress("0x000000000000000000000000000000000000Be")
	token := common.Address{} // ETH
	amount := big.NewInt(2_000_000_000_000_000_000)
	releaseID := uint64(424242)
	sigs := [][]byte{
		bytes.Repeat([]byte{0x01}, 65),
		bytes.Repeat([]byte{0x02}, 65),
	}

	data, err := packReleaseCalldata(to, token, amount, releaseID, sigs)
	if err != nil {
		t.Fatalf("packReleaseCalldata failed: %v", err)
	}

	unpacked, err := vaultContractABI.Methods["release"].Inputs.Unpack(data[4:])
	if err != nil {
		t.Fatalf("failed to unpack release calldata: %v", err)
	}
	gotSigs, ok := unpacked[4].([][]byte)
	if !ok || len(gotSigs) != 2 {
		t.Fatalf("expected 2 signatures to round-trip, got %v", unpacked[4])
	}
	if !bytes.Equal(gotSigs[0], sigs[0]) || !bytes.Equal(gotSigs[1], sigs[1]) {
		t.Fatal("expected signature bytes to round-trip exactly")
	}
	gotReleaseID, ok := unpacked[3].(*big.Int)
	if !ok || gotReleaseID.Uint64() != releaseID {
		t.Fatalf("expected releaseId %d to round-trip, got %v", releaseID, unpacked[3])
	}
}

// TestDecodeLockedEvent_RoundTripsAConstructedLog builds a
// types.Log the way a real Ethereum node would deliver one for a
// Locked event, then confirms decodeLockedEvent correctly separates
// the indexed topics (depositor/token) from the ABI-packed Data
// (amount/creditChainAddress/creditChainPublicKey) - the exact split
// api/vault_eth_watch.go's scanVaultEthLogsOnce depends on.
func TestDecodeLockedEvent_RoundTripsAConstructedLog(t *testing.T) {
	depositor := common.HexToAddress("0x000000000000000000000000000000DeaDBeef")
	token := common.Address{} // ETH deposit
	amount := big.NewInt(3_500_000_000_000_000_000)
	var ccAddr [20]byte
	copy(ccAddr[:], bytes.Repeat([]byte{0x33}, 20))
	var ccPub [32]byte
	copy(ccPub[:], bytes.Repeat([]byte{0x44}, 32))

	nonIndexedData, err := vaultContractABI.Events["Locked"].Inputs.NonIndexed().Pack(amount, ccAddr, ccPub)
	if err != nil {
		t.Fatalf("failed to pack non-indexed event fields: %v", err)
	}

	log := types.Log{
		Topics: []common.Hash{
			lockedEventSignature,
			common.BytesToHash(depositor.Bytes()),
			common.BytesToHash(token.Bytes()),
		},
		Data:   nonIndexedData,
		TxHash: common.HexToHash("0xfeed"),
		Index:  7,
	}

	decoded, err := decodeLockedEvent(log)
	if err != nil {
		t.Fatalf("decodeLockedEvent failed: %v", err)
	}
	if decoded.Depositor != depositor {
		t.Fatalf("expected depositor %s, got %s", depositor, decoded.Depositor)
	}
	if decoded.Token != token {
		t.Fatalf("expected token %s, got %s", token, decoded.Token)
	}
	if decoded.Amount.Cmp(amount) != 0 {
		t.Fatalf("expected amount %s, got %s", amount, decoded.Amount)
	}
	if decoded.CreditChainAddress != ccAddr {
		t.Fatalf("expected creditChainAddress %x, got %x", ccAddr, decoded.CreditChainAddress)
	}
	if decoded.CreditChainPublicKey != ccPub {
		t.Fatalf("expected creditChainPublicKey %x, got %x", ccPub, decoded.CreditChainPublicKey)
	}
}

func TestDecodeLockedEvent_RejectsWrongTopicCount(t *testing.T) {
	log := types.Log{Topics: []common.Hash{lockedEventSignature}, Data: []byte{}}
	if _, err := decodeLockedEvent(log); err == nil {
		t.Fatal("expected an error for a log with the wrong number of topics")
	}
}

func TestDecodeLockedEvent_RejectsWrongEventSignature(t *testing.T) {
	log := types.Log{
		Topics: []common.Hash{common.HexToHash("0xnotlocked"), common.Hash{}, common.Hash{}},
		Data:   []byte{},
	}
	if _, err := decodeLockedEvent(log); err == nil {
		t.Fatal("expected an error for a log whose topic0 doesn't match the Locked event signature")
	}
}

// TestBridgeValidatorSigningKey_RejectsMalformedHex confirms a
// misconfigured BRIDGE_VALIDATOR_PRIVATE_KEY_HEX degrades to "this node
// doesn't submit attestations" rather than panicking - same posture
// InitEthRPC already takes for a missing/bad RPC URL.
func TestBridgeValidatorSigningKey_RejectsMalformedHex(t *testing.T) {
	t.Setenv("BRIDGE_VALIDATOR_PRIVATE_KEY_HEX", "not-valid-hex")
	if _, _, ok := bridgeValidatorSigningKey(); ok {
		t.Fatal("expected malformed hex to be rejected")
	}

	t.Setenv("BRIDGE_VALIDATOR_PRIVATE_KEY_HEX", "aabb") // valid hex, wrong length
	if _, _, ok := bridgeValidatorSigningKey(); ok {
		t.Fatal("expected a too-short key to be rejected")
	}

	t.Setenv("BRIDGE_VALIDATOR_PRIVATE_KEY_HEX", "")
	if _, _, ok := bridgeValidatorSigningKey(); ok {
		t.Fatal("expected an unset key to be rejected")
	}
}

func TestVaultContractAddress_RequiresValidHexAddress(t *testing.T) {
	t.Setenv("VAULT_CONTRACT_ADDRESS", "")
	if _, ok := vaultContractAddress(); ok {
		t.Fatal("expected an unset address to be rejected")
	}

	t.Setenv("VAULT_CONTRACT_ADDRESS", "not-an-address")
	if _, ok := vaultContractAddress(); ok {
		t.Fatal("expected a malformed address to be rejected")
	}

	t.Setenv("VAULT_CONTRACT_ADDRESS", "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238")
	got, ok := vaultContractAddress()
	if !ok || got != common.HexToAddress("0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238") {
		t.Fatal("expected a valid hex address to be accepted and parsed correctly")
	}
}
