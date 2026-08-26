package api

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// HandleSweepVaultReleaseSigningKeyEth is a one-off cleanup tool, not a
// general-purpose "send from any key" utility - deliberately narrow:
// it only ever moves real ETH from VAULT_RELEASE_SIGNING_KEY_HEX's own
// address to the configured relayer's address. This exists for exactly
// one situation - an operator accidentally funded the signing key's
// address instead of the relayer's (the two look similar and are easy
// to mix up), and the signing key has no other use for ETH at all (it
// only ever produces signatures, see api/vault_release_approve_watch.go's
// doc comment). Admin-gated since it moves real value, same as
// api/admin_transfer.go's HandleAdminTransfer.
func HandleSweepVaultReleaseSigningKeyEth(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if _, _, err := requireAdminAuthed(r, nil); err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if ethRPCClient == nil {
		http.Error(w, "ETH RPC not configured on this server", http.StatusServiceUnavailable)
		return
	}

	_, signKey, ok := vaultReleaseSigningKey()
	if !ok {
		http.Error(w, "VAULT_RELEASE_SIGNING_KEY_HEX not configured", http.StatusServiceUnavailable)
		return
	}
	signAddr := crypto.PubkeyToAddress(signKey.PublicKey)

	relayerSignerForSweep, ok := resolveRelayerSigner()
	if !ok {
		http.Error(w, "no relayer key configured on this server", http.StatusServiceUnavailable)
		return
	}
	relayerAddrHex, err := relayerSignerForSweep.address(r.Context())
	if err != nil {
		http.Error(w, "failed to resolve relayer signer address: "+err.Error(), http.StatusBadGateway)
		return
	}
	relayerAddr := common.HexToAddress(relayerAddrHex)

	balance, err := ethBalance(signAddr.Hex())
	if err != nil {
		http.Error(w, "failed to read balance: "+err.Error(), http.StatusBadGateway)
		return
	}
	if balance.Sign() <= 0 {
		http.Error(w, "nothing to sweep - balance is zero", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
	defer cancel()

	tipCap, err := ethRPCClient.SuggestGasTipCap(ctx)
	if err != nil {
		http.Error(w, "failed to get gas tip: "+err.Error(), http.StatusBadGateway)
		return
	}
	head, err := ethRPCClient.HeaderByNumber(ctx, nil)
	if err != nil {
		http.Error(w, "failed to get chain head: "+err.Error(), http.StatusBadGateway)
		return
	}
	// Same "2x current base fee + tip" conservative-headroom heuristic
	// buildUnsignedContractCallTx already uses elsewhere in this file.
	feeCap := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tipCap)
	// A plain ETH transfer to an ordinary externally-owned address (the
	// relayer holds no contract code) always costs exactly 21000 gas -
	// no EstimateGas call needed, unlike a contract call.
	const plainTransferGas = 21000
	maxGasCost := new(big.Int).Mul(feeCap, big.NewInt(plainTransferGas))

	sweepAmount := new(big.Int).Sub(balance, maxGasCost)
	if sweepAmount.Sign() <= 0 {
		http.Error(w, "balance too small to cover gas for the sweep itself", http.StatusBadRequest)
		return
	}

	nonce, err := ethRPCClient.PendingNonceAt(ctx, signAddr)
	if err != nil {
		http.Error(w, "failed to get nonce: "+err.Error(), http.StatusBadGateway)
		return
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   expectedEthChainID,
		Nonce:     nonce,
		To:        &relayerAddr,
		Value:     sweepAmount,
		Gas:       plainTransferGas,
		GasFeeCap: feeCap,
		GasTipCap: tipCap,
	})

	signer := types.LatestSignerForChainID(expectedEthChainID)
	signedTx, err := types.SignTx(tx, signer, signKey)
	if err != nil {
		http.Error(w, "failed to sign sweep tx: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := ethRPCClient.SendTransaction(ctx, signedTx); err != nil {
		http.Error(w, "failed to broadcast sweep tx: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status":    "submitted",
		"txHash":    signedTx.Hash().Hex(),
		"amountWei": sweepAmount.String(),
		"from":      signAddr.Hex(),
		"to":        relayerAddr.Hex(),
	})
}
