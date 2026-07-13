package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

var ethCustodialStore = core.NewEthCustodialStore()

// Unlike every other economic action in this app (see
// audit/ARCHITECTURE.md's money-movement path), ETH-custodial account
// creation and withdrawal are handled synchronously, directly in the HTTP
// handler on whichever node receives the request - deliberately NOT
// routed through the gossip/block-confirmation pipeline. Reasoning:
// broadcasting a real Sepolia transaction is an external, irreversible
// side effect that must happen exactly once; letting every node
// independently "apply" a withdrawal from a confirmed block would risk
// two nodes racing to broadcast (best case, the second attempt is
// harmlessly rejected for a stale nonce; worst case, a genuine nonce
// race). This is a disclosed, deliberate simplification for this first
// pass - see audit/KNOWN_GAPS.md's ETH-custodial entry for the
// consequence: a custodial account (and the local-dev signer's key, when
// KMS isn't configured) only reliably works from whichever node it was
// created on, since core.EthCustodialStore is persisted via this node's
// own state.json snapshot but not gossiped to the other node the way
// every other store in this app is.

type ethCustodialCreateData struct{}

// HandleCreateEthCustodialAccount provisions a new Sepolia-testnet
// signing key (via ethSigner - AWS KMS in production, a clearly-labeled
// local dev-only fallback otherwise, see eth_signer.go) for the
// requesting wallet, and records the resulting address. One custodial
// account per CreditChain address, created once.
func HandleCreateEthCustodialAccount(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if ethSigner == nil {
		http.Error(w, "ETH custodial signer not initialized", http.StatusServiceUnavailable)
		return
	}

	var req ethCustodialCreateData
	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if existing, ok := ethCustodialStore.Get(address); ok {
		json.NewEncoder(w).Encode(map[string]string{
			"status":     "already exists",
			"ethAddress": existing.EthAddress,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	keyRef, ethAddress, err := ethSigner.NewKey(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to provision signing key: %v", err), http.StatusInternalServerError)
		return
	}

	ethCustodialStore.Create(&core.EthCustodialAccount{
		CreditChainAddress: address,
		EthAddress:         ethAddress,
		KeyRef:             keyRef,
		CreatedAt:          time.Now().Unix(),
	})

	json.NewEncoder(w).Encode(map[string]string{
		"status":     "created",
		"ethAddress": ethAddress,
	})
}

// HandleEthCustodialBalance returns the account's real, live Sepolia
// balance (queried fresh from the RPC node every call - never cached or
// tracked internally, since this corner has no internal ledger of its
// own; the real chain is the only source of truth for this balance,
// unlike CCT/ETHTEST/USDCTEST which are internally tracked balances).
func HandleEthCustodialBalance(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if ethRPCClient == nil {
		http.Error(w, "ETH custodial corner not enabled (no Sepolia RPC configured)", http.StatusServiceUnavailable)
		return
	}

	address := r.URL.Query().Get("address")
	account, ok := ethCustodialStore.Get(address)
	if !ok {
		http.Error(w, "no custodial ETH account for that address", http.StatusNotFound)
		return
	}

	wei, err := ethBalance(account.EthAddress)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to query balance: %v", err), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"ethAddress": account.EthAddress,
		"weiBalance": wei.String(),
	})
}

type ethCustodialWithdrawData struct {
	To        string `json:"to"`
	AmountWei string `json:"amountWei"`
}

// HandleEthCustodialWithdraw signs and broadcasts a real Sepolia
// transaction sending the account's own custodial ETH to an external
// address the caller controls - the private key backing it never leaves
// ethSigner's own implementation (see eth_signer.go). Executes
// synchronously (see this file's top-of-file doc comment for why) and
// returns the real Sepolia transaction hash once broadcast succeeds.
func HandleEthCustodialWithdraw(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if ethRPCClient == nil || ethSigner == nil {
		http.Error(w, "ETH custodial corner not enabled", http.StatusServiceUnavailable)
		return
	}

	var req ethCustodialWithdrawData
	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	account, ok := ethCustodialStore.Get(address)
	if !ok {
		http.Error(w, "no custodial ETH account for that address", http.StatusNotFound)
		return
	}

	if req.To == "" {
		http.Error(w, "to address required", http.StatusBadRequest)
		return
	}
	amountWei, ok := new(big.Int).SetString(req.AmountWei, 10)
	if !ok || amountWei.Sign() <= 0 {
		http.Error(w, "amountWei must be a positive decimal integer string", http.StatusBadRequest)
		return
	}

	// Fast-fail balance check - re-checked implicitly by the RPC node
	// itself when the transaction is broadcast (an insufficient-funds
	// transaction is simply rejected), same "cheap sanity check now,
	// real check at the point of truth" pattern as every other handler
	// in this app.
	currentBalance, err := ethBalance(account.EthAddress)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to check balance: %v", err), http.StatusBadGateway)
		return
	}
	if currentBalance.Cmp(amountWei) < 0 {
		http.Error(w, "insufficient custodial ETH balance", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	txHash, err := buildAndBroadcastWithdrawal(ctx, account.KeyRef, account.EthAddress, req.To, amountWei)
	if err != nil {
		http.Error(w, fmt.Sprintf("withdrawal failed: %v", err), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status": "broadcast",
		"txHash": txHash,
	})
}
