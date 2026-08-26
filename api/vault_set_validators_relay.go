package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// This file lets a bridge validator update contracts/CreditChainVault.sol's
// real on-chain validator set - the contract has no separate admin/owner
// key at all (see its own doc comment: "No separate admin/owner key
// exists anywhere in this contract"), every state change including this
// one requires a genuine threshold-signed digest from the CURRENT
// validator set. This is a one-off governance action (unlike
// release()/vault_release_relay.go, there's no ongoing loop here) - a
// bridge validator signs client-side with their existing saved key
// (website/js/vault.js's buildSetValidatorsDigest + EthTx.signDigest,
// same pattern release approval already uses), and this handler relays
// the already-signed call through the SAME funded relayer account
// release() broadcasts use, so the validator's own signing key never
// needs to hold ETH for gas itself.

// setValidatorsDigestArgs mirrors CreditChainVault.sol's setValidators
// digest exactly: keccak256(abi.encode("SET_VALIDATORS", newValidators,
// newThreshold, actionNonce, address(this), block.chainid)) - a string
// literal and an address[] are both dynamic types, unlike the release
// digest's all-static fields, but abi.Arguments.Pack (go-ethereum's own
// abi.encode implementation) handles the head/tail dynamic-type layout
// correctly without this code needing to hand-roll it.
var setValidatorsDigestArgs abi.Arguments

func init() {
	stringType, err := abi.NewType("string", "", nil)
	if err != nil {
		panic("vault_set_validators_relay.go: bad string abi type: " + err.Error())
	}
	addressArrayType, err := abi.NewType("address[]", "", nil)
	if err != nil {
		panic("vault_set_validators_relay.go: bad address[] abi type: " + err.Error())
	}
	addressType, err := abi.NewType("address", "", nil)
	if err != nil {
		panic("vault_set_validators_relay.go: bad address abi type: " + err.Error())
	}
	uint256Type, err := abi.NewType("uint256", "", nil)
	if err != nil {
		panic("vault_set_validators_relay.go: bad uint256 abi type: " + err.Error())
	}
	setValidatorsDigestArgs = abi.Arguments{
		{Type: stringType}, {Type: addressArrayType}, {Type: uint256Type},
		{Type: uint256Type}, {Type: addressType}, {Type: uint256Type},
	}
}

func buildSetValidatorsDigestGo(newValidators []common.Address, newThreshold, actionNonce *big.Int, vaultContract common.Address, chainID *big.Int) ([]byte, error) {
	packed, err := setValidatorsDigestArgs.Pack("SET_VALIDATORS", newValidators, newThreshold, actionNonce, vaultContract, chainID)
	if err != nil {
		return nil, err
	}
	return crypto.Keccak256(packed), nil
}

type vaultSetValidatorsRelayData struct {
	NewValidators []string `json:"newValidators"`
	NewThreshold  uint64   `json:"newThreshold"`
	ActionNonce   uint64   `json:"actionNonce"`
	// SignaturesHex is one hex-encoded 65-byte [R||S||V] signature per
	// currently-authorized validator that's co-signing this change -
	// same format EthTx.signDigest already produces for release
	// approvals, V already normalized to 27/28.
	SignaturesHex []string `json:"signaturesHex"`
}

// HandleVaultSetValidatorsRelay is gated on isBridgeValidatorAddress,
// not requireAdminAuthed - deliberately: the REAL authorization is the
// on-chain signature check inside setValidators() itself (this handler
// cannot forge a valid signature, and a bad one just makes the real
// transaction revert, wasting only the relayer's own gas - same
// "permissionless relay, authorization lives in the signature" model
// api/vault_release_relay.go's own doc comment describes). Restricting
// who can even ask this node to relay is just spam/gas-waste
// prevention, not a security boundary.
func HandleVaultSetValidatorsRelay(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req vaultSetValidatorsRelayData
	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if !isBridgeValidatorAddress(address) {
		http.Error(w, "address is not a configured bridge validator", http.StatusForbidden)
		return
	}

	if len(req.NewValidators) == 0 {
		http.Error(w, "newValidators must be non-empty", http.StatusBadRequest)
		return
	}
	if req.NewThreshold == 0 || req.NewThreshold > uint64(len(req.NewValidators)) {
		http.Error(w, "newThreshold must be > 0 and <= len(newValidators)", http.StatusBadRequest)
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

	newValidators := make([]common.Address, 0, len(req.NewValidators))
	for _, v := range req.NewValidators {
		if !common.IsHexAddress(v) {
			http.Error(w, "newValidators contains an invalid address: "+v, http.StatusBadRequest)
			return
		}
		newValidators = append(newValidators, common.HexToAddress(v))
	}

	signatures := make([][]byte, 0, len(req.SignaturesHex))
	for _, sigHex := range req.SignaturesHex {
		sigBytes, err := hex.DecodeString(sigHex)
		if err != nil || len(sigBytes) != 65 {
			http.Error(w, "signaturesHex contains an invalid 65-byte signature", http.StatusBadRequest)
			return
		}
		signatures = append(signatures, sigBytes)
	}

	calldata, err := packSetValidatorsCalldata(newValidators, new(big.Int).SetUint64(req.NewThreshold), new(big.Int).SetUint64(req.ActionNonce), signatures)
	if err != nil {
		http.Error(w, "failed to pack setValidators calldata: "+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()

	txHash, err := buildAndBroadcastContractCallWithRelayerSigner(ctx, signer, relayerAddr, vaultAddr.Hex(), calldata, big.NewInt(0))
	if err != nil {
		http.Error(w, "setValidators() call failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "submitted", "txHash": txHash})
}
