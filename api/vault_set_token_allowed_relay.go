package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// This file mirrors api/vault_set_validators_relay.go exactly, for
// contracts/CreditChainVault.sol's other two self-governance setters
// (setTokenAllowed/setPerDepositorCaps) - same "permissionless relay,
// real authorization lives in the on-chain threshold-signature check"
// model, same reason for existing (a bridge validator signs client-
// side with their existing saved key; this handler just broadcasts the
// already-signed call through the funded relayer so the validator's
// own key never needs to hold ETH for gas).
//
// setTokenAllowed is the one-off prerequisite for the ETH liquid-
// staking feature: the vault only accepts depositERC20() for tokens in
// its allowedTokens map, and Lido's real stETH
// (0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84 on mainnet) isn't in it
// yet. setPerDepositorCaps is needed alongside it because the
// deployed contract's perDepositorCapUsdc check compares a token's RAW
// deposited amount against a 6-decimal-USDC-scaled cap regardless of
// the token's own actual decimals - for an 18-decimal token like
// stETH, any real deposit trivially exceeds it. Zeroing the cap (0 =
// disabled, same convention as every other "0 = not enforced" cap in
// this codebase) is the only way to accept stETH deposits without
// redeploying the contract - a deliberate, disclosed tradeoff, not an
// oversight.

var setTokenAllowedDigestArgs abi.Arguments
var setPerDepositorCapsDigestArgs abi.Arguments

func init() {
	stringType, err := abi.NewType("string", "", nil)
	if err != nil {
		panic("vault_set_token_allowed_relay.go: bad string abi type: " + err.Error())
	}
	addressType, err := abi.NewType("address", "", nil)
	if err != nil {
		panic("vault_set_token_allowed_relay.go: bad address abi type: " + err.Error())
	}
	boolType, err := abi.NewType("bool", "", nil)
	if err != nil {
		panic("vault_set_token_allowed_relay.go: bad bool abi type: " + err.Error())
	}
	uint256Type, err := abi.NewType("uint256", "", nil)
	if err != nil {
		panic("vault_set_token_allowed_relay.go: bad uint256 abi type: " + err.Error())
	}

	setTokenAllowedDigestArgs = abi.Arguments{
		{Type: stringType}, {Type: addressType}, {Type: boolType},
		{Type: uint256Type}, {Type: addressType}, {Type: uint256Type},
	}
	setPerDepositorCapsDigestArgs = abi.Arguments{
		{Type: stringType}, {Type: uint256Type}, {Type: uint256Type},
		{Type: uint256Type}, {Type: addressType}, {Type: uint256Type},
	}
}

// buildSetTokenAllowedDigestGo mirrors CreditChainVault.sol's
// setTokenAllowed digest exactly: keccak256(abi.encode("SET_TOKEN_ALLOWED",
// token, allowed, actionNonce, address(this), block.chainid)).
func buildSetTokenAllowedDigestGo(token common.Address, allowed bool, actionNonce *big.Int, vaultContract common.Address, chainID *big.Int) ([]byte, error) {
	packed, err := setTokenAllowedDigestArgs.Pack("SET_TOKEN_ALLOWED", token, allowed, actionNonce, vaultContract, chainID)
	if err != nil {
		return nil, err
	}
	return crypto.Keccak256(packed), nil
}

// buildSetPerDepositorCapsDigestGo mirrors CreditChainVault.sol's
// setPerDepositorCaps digest exactly: keccak256(abi.encode(
// "SET_PER_DEPOSITOR_CAPS", newCapWei, newCapUsdc, actionNonce,
// address(this), block.chainid)).
func buildSetPerDepositorCapsDigestGo(newCapWei, newCapUsdc, actionNonce *big.Int, vaultContract common.Address, chainID *big.Int) ([]byte, error) {
	packed, err := setPerDepositorCapsDigestArgs.Pack("SET_PER_DEPOSITOR_CAPS", newCapWei, newCapUsdc, actionNonce, vaultContract, chainID)
	if err != nil {
		return nil, err
	}
	return crypto.Keccak256(packed), nil
}

type vaultSetTokenAllowedRelayData struct {
	Token         string   `json:"token"`
	Allowed       bool     `json:"allowed"`
	ActionNonce   uint64   `json:"actionNonce"`
	SignaturesHex []string `json:"signaturesHex"`
}

var errInvalidSignature = errors.New("invalid 65-byte signature")

// decodeSignaturesHex is shared by every vault self-governance relay
// handler in this codebase (setValidators/setTokenAllowed/
// setPerDepositorCaps) - each expects one hex-encoded 65-byte
// [R||S||V] signature per co-signing validator, V already normalized
// to 27/28, same format EthTx.signDigest already produces.
func decodeSignaturesHex(signaturesHex []string) ([][]byte, error) {
	signatures := make([][]byte, 0, len(signaturesHex))
	for _, sigHex := range signaturesHex {
		sigBytes, err := hex.DecodeString(sigHex)
		if err != nil || len(sigBytes) != 65 {
			return nil, errInvalidSignature
		}
		signatures = append(signatures, sigBytes)
	}
	return signatures, nil
}

// HandleVaultSetTokenAllowedRelay - see this file's own doc comment
// for why gating is isBridgeValidatorAddress, not requireAdminAuthed.
func HandleVaultSetTokenAllowedRelay(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req vaultSetTokenAllowedRelayData
	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if !isBridgeValidatorAddress(address) {
		http.Error(w, "address is not a configured bridge validator", http.StatusForbidden)
		return
	}
	if !common.IsHexAddress(req.Token) {
		http.Error(w, "token must be a valid 0x-prefixed address", http.StatusBadRequest)
		return
	}
	if len(req.SignaturesHex) == 0 {
		http.Error(w, "signaturesHex must be non-empty", http.StatusBadRequest)
		return
	}

	vaultAddr, ok := vaultContractAddress()
	if !ok {
		http.Error(w, "vault contract not configured on this server", http.StatusServiceUnavailable)
		return
	}
	signer, ok := resolveRelayerSigner()
	if !ok {
		http.Error(w, "no relayer key configured on this server - cannot broadcast", http.StatusServiceUnavailable)
		return
	}
	relayerAddr, err := signer.address(r.Context())
	if err != nil {
		http.Error(w, "failed to resolve relayer signer address: "+err.Error(), http.StatusBadGateway)
		return
	}

	signatures, err := decodeSignaturesHex(req.SignaturesHex)
	if err != nil {
		http.Error(w, "signaturesHex contains an invalid 65-byte signature", http.StatusBadRequest)
		return
	}

	calldata, err := packSetTokenAllowedCalldata(common.HexToAddress(req.Token), req.Allowed, new(big.Int).SetUint64(req.ActionNonce), signatures)
	if err != nil {
		http.Error(w, "failed to pack setTokenAllowed calldata: "+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()

	txHash, err := buildAndBroadcastContractCallWithRelayerSigner(ctx, signer, relayerAddr, vaultAddr.Hex(), calldata, big.NewInt(0))
	if err != nil {
		http.Error(w, "setTokenAllowed() call failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "submitted", "txHash": txHash})
}

type vaultSetPerDepositorCapsRelayData struct {
	NewCapWei     string   `json:"newCapWei"`
	NewCapUsdc    string   `json:"newCapUsdc"`
	ActionNonce   uint64   `json:"actionNonce"`
	SignaturesHex []string `json:"signaturesHex"`
}

// HandleVaultSetPerDepositorCapsRelay - see this file's own doc
// comment for why gating is isBridgeValidatorAddress, not
// requireAdminAuthed.
func HandleVaultSetPerDepositorCapsRelay(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req vaultSetPerDepositorCapsRelayData
	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if !isBridgeValidatorAddress(address) {
		http.Error(w, "address is not a configured bridge validator", http.StatusForbidden)
		return
	}

	newCapWei, ok := new(big.Int).SetString(req.NewCapWei, 10)
	if !ok || newCapWei.Sign() < 0 {
		http.Error(w, "newCapWei must be a non-negative decimal integer", http.StatusBadRequest)
		return
	}
	newCapUsdc, ok := new(big.Int).SetString(req.NewCapUsdc, 10)
	if !ok || newCapUsdc.Sign() < 0 {
		http.Error(w, "newCapUsdc must be a non-negative decimal integer", http.StatusBadRequest)
		return
	}
	if len(req.SignaturesHex) == 0 {
		http.Error(w, "signaturesHex must be non-empty", http.StatusBadRequest)
		return
	}

	vaultAddr, ok := vaultContractAddress()
	if !ok {
		http.Error(w, "vault contract not configured on this server", http.StatusServiceUnavailable)
		return
	}
	signer, ok := resolveRelayerSigner()
	if !ok {
		http.Error(w, "no relayer key configured on this server - cannot broadcast", http.StatusServiceUnavailable)
		return
	}
	relayerAddr, err := signer.address(r.Context())
	if err != nil {
		http.Error(w, "failed to resolve relayer signer address: "+err.Error(), http.StatusBadGateway)
		return
	}

	signatures, err := decodeSignaturesHex(req.SignaturesHex)
	if err != nil {
		http.Error(w, "signaturesHex contains an invalid 65-byte signature", http.StatusBadRequest)
		return
	}

	calldata, err := packSetPerDepositorCapsCalldata(newCapWei, newCapUsdc, new(big.Int).SetUint64(req.ActionNonce), signatures)
	if err != nil {
		http.Error(w, "failed to pack setPerDepositorCaps calldata: "+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()

	txHash, err := buildAndBroadcastContractCallWithRelayerSigner(ctx, signer, relayerAddr, vaultAddr.Hex(), calldata, big.NewInt(0))
	if err != nil {
		http.Error(w, "setPerDepositorCaps() call failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "submitted", "txHash": txHash})
}
