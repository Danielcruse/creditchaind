package api

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// vaultContractABIJSON mirrors contracts/CreditChainVault.sol's real
// function/event signatures exactly - hand-written rather than
// abigen-generated, per the plan's "keep the Go build self-contained"
// decision (no forge/abigen toolchain available in this environment -
// see contracts/README.md). Only the pieces this Go code actually
// calls/decodes are included; the contract has one more function
// (setPaused) that doesn't need a Go-side caller yet.
const vaultContractABIJSON = `[
	{"type":"function","name":"deposit","inputs":[{"name":"creditChainAddress","type":"bytes20"},{"name":"creditChainPublicKey","type":"bytes32"}],"outputs":[],"stateMutability":"payable"},
	{"type":"function","name":"depositERC20","inputs":[{"name":"token","type":"address"},{"name":"amount","type":"uint256"},{"name":"creditChainAddress","type":"bytes20"},{"name":"creditChainPublicKey","type":"bytes32"}],"outputs":[],"stateMutability":"nonpayable"},
	{"type":"function","name":"release","inputs":[{"name":"to","type":"address"},{"name":"token","type":"address"},{"name":"amount","type":"uint256"},{"name":"releaseId","type":"uint256"},{"name":"signatures","type":"bytes[]"}],"outputs":[],"stateMutability":"nonpayable"},
	{"type":"function","name":"setValidators","inputs":[{"name":"newValidators","type":"address[]"},{"name":"newThreshold","type":"uint256"},{"name":"actionNonce","type":"uint256"},{"name":"signatures","type":"bytes[]"}],"outputs":[],"stateMutability":"nonpayable"},
	{"type":"function","name":"setTokenAllowed","inputs":[{"name":"token","type":"address"},{"name":"allowed","type":"bool"},{"name":"actionNonce","type":"uint256"},{"name":"signatures","type":"bytes[]"}],"outputs":[],"stateMutability":"nonpayable"},
	{"type":"function","name":"setPerDepositorCaps","inputs":[{"name":"newCapWei","type":"uint256"},{"name":"newCapUsdc","type":"uint256"},{"name":"actionNonce","type":"uint256"},{"name":"signatures","type":"bytes[]"}],"outputs":[],"stateMutability":"nonpayable"},
	{"type":"event","name":"Locked","inputs":[
		{"name":"depositor","type":"address","indexed":true},
		{"name":"token","type":"address","indexed":true},
		{"name":"amount","type":"uint256","indexed":false},
		{"name":"creditChainAddress","type":"bytes20","indexed":false},
		{"name":"creditChainPublicKey","type":"bytes32","indexed":false}
	],"anonymous":false}
]`

var vaultContractABI abi.ABI

// lockedEventSignature is the keccak256 topic0 every Locked event log
// carries - used to filter FilterLogs queries to only this event
// (api/vault_eth_watch.go). Populated inside init() below, NOT via a
// package-level var initializer expression - Go runs every package-level
// var initializer before any init() function, regardless of file/decl
// order, so an initializer here would have read vaultContractABI before
// this same init() populated it (confirmed the hard way: this exact
// mistake panicked "Locked event missing from parsed ABI" on the first
// test run).
var lockedEventSignature common.Hash

func init() {
	parsed, err := abi.JSON(strings.NewReader(vaultContractABIJSON))
	if err != nil {
		// A malformed constant here is a build-time bug, not a runtime
		// condition - same "panic on an impossible programmer error"
		// posture core/cct_amount.go's init-time big.Int parsing already
		// takes for its own fixed constants.
		panic("vault_contract_abi.go: malformed vaultContractABIJSON: " + err.Error())
	}
	vaultContractABI = parsed

	event, ok := vaultContractABI.Events["Locked"]
	if !ok {
		panic("vault_contract_abi.go: Locked event missing from parsed ABI")
	}
	lockedEventSignature = event.ID
}

// packDepositCalldata builds the ABI-encoded calldata for a plain ETH
// deposit() call - the ETH amount itself is the transaction's value
// field (set by the caller), not part of this calldata.
func packDepositCalldata(creditChainAddress [20]byte, creditChainPublicKey [32]byte) ([]byte, error) {
	return vaultContractABI.Pack("deposit", creditChainAddress, creditChainPublicKey)
}

// packDepositERC20Calldata builds the calldata for a depositERC20()
// call - the caller must have already sent a separate approve()
// transaction for at least `amount` beforehand (standard ERC20
// approve+transferFrom, not permit - see contracts/CreditChainVault.sol's
// doc comment for why).
func packDepositERC20Calldata(token common.Address, amount *big.Int, creditChainAddress [20]byte, creditChainPublicKey [32]byte) ([]byte, error) {
	return vaultContractABI.Pack("depositERC20", token, amount, creditChainAddress, creditChainPublicKey)
}

// packReleaseCalldata builds the calldata for a release() call - see
// api/vault_release_relay.go for the node-local relay that assembles
// this once a VaultReleaseApproval reaches signature threshold.
func packReleaseCalldata(to common.Address, token common.Address, amount *big.Int, releaseID uint64, signatures [][]byte) ([]byte, error) {
	return vaultContractABI.Pack("release", to, token, amount, new(big.Int).SetUint64(releaseID), signatures)
}

// packSetValidatorsCalldata builds the calldata for a setValidators()
// call - see api/vault_set_validators_relay.go for the handler that
// broadcasts this once a bridge validator has produced a real signature
// over buildSetValidatorsDigestGo's digest.
func packSetValidatorsCalldata(newValidators []common.Address, newThreshold *big.Int, actionNonce *big.Int, signatures [][]byte) ([]byte, error) {
	return vaultContractABI.Pack("setValidators", newValidators, newThreshold, actionNonce, signatures)
}

// packSetTokenAllowedCalldata/packSetPerDepositorCapsCalldata build
// calldata for the vault's other two self-governance setters - see
// api/vault_set_token_allowed_relay.go for the handlers that broadcast
// these once a bridge validator has produced a real signature over
// buildSetTokenAllowedDigestGo/buildSetPerDepositorCapsDigestGo's
// digests.
func packSetTokenAllowedCalldata(token common.Address, allowed bool, actionNonce *big.Int, signatures [][]byte) ([]byte, error) {
	return vaultContractABI.Pack("setTokenAllowed", token, allowed, actionNonce, signatures)
}

func packSetPerDepositorCapsCalldata(newCapWei, newCapUsdc, actionNonce *big.Int, signatures [][]byte) ([]byte, error) {
	return vaultContractABI.Pack("setPerDepositorCaps", newCapWei, newCapUsdc, actionNonce, signatures)
}

// decodedLockedEvent is the Go-side shape of one real on-chain Locked
// event, after decoding both its indexed topics (depositor, token) and
// its non-indexed data fields (amount, creditChainAddress,
// creditChainPublicKey).
type decodedLockedEvent struct {
	Depositor            common.Address
	Token                common.Address // zero address means ETH
	Amount               *big.Int
	CreditChainAddress   [20]byte
	CreditChainPublicKey [32]byte
}

// decodeLockedEvent unpacks one real Ethereum log entry into a
// decodedLockedEvent - the depositor/token addresses come from the
// log's indexed topics (Solidity packs indexed params into topics, not
// Data), everything else from Data via the ABI's own unpacking.
func decodeLockedEvent(log types.Log) (*decodedLockedEvent, error) {
	if len(log.Topics) != 3 {
		return nil, fmt.Errorf("decodeLockedEvent: expected 3 topics (signature + 2 indexed params), got %d", len(log.Topics))
	}
	if log.Topics[0] != lockedEventSignature {
		return nil, fmt.Errorf("decodeLockedEvent: topic0 does not match the Locked event signature")
	}

	out := map[string]interface{}{}
	if err := vaultContractABI.UnpackIntoMap(out, "Locked", log.Data); err != nil {
		return nil, fmt.Errorf("decodeLockedEvent: unpack data: %w", err)
	}

	amount, ok := out["amount"].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("decodeLockedEvent: amount field missing or wrong type")
	}
	ccAddr, ok := out["creditChainAddress"].([20]byte)
	if !ok {
		return nil, fmt.Errorf("decodeLockedEvent: creditChainAddress field missing or wrong type")
	}
	ccPub, ok := out["creditChainPublicKey"].([32]byte)
	if !ok {
		return nil, fmt.Errorf("decodeLockedEvent: creditChainPublicKey field missing or wrong type")
	}

	return &decodedLockedEvent{
		Depositor:            common.BytesToAddress(log.Topics[1].Bytes()),
		Token:                common.BytesToAddress(log.Topics[2].Bytes()),
		Amount:               amount,
		CreditChainAddress:   ccAddr,
		CreditChainPublicKey: ccPub,
	}, nil
}
