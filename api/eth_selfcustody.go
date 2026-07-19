package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// api/eth_selfcustody.go implements the Multi-Chain HD Wallet Phase B
// read/broadcast proxy for the SELF-CUSTODIAL Ethereum account derived
// client-side in website/js/hdwallet.js (Phase A). Architecturally
// separate from api/eth_custodial.go's KMS-held corner: this file
// never sees, stores, or needs a private key for any address it's
// asked about - every address is caller-supplied and this code has no
// internal notion of "whose" address it is, unlike
// ethCustodialStore.Get's CreditChain-address binding. All signing
// happens in the browser (website/js/vendor/eth-tx.js); this is a pure
// read-through-RPC + relay-a-pre-signed-tx proxy, needed only because
// SEPOLIA_RPC_URL and the RPC provider's API key must never reach the
// browser (see eth_rpc.go's InitEthRPC doc comment).

// HandleEthSelfCustodyBalance returns balance/nonce/suggested-gas-price
// for an arbitrary Ethereum address - no CreditChain auth, since this
// isn't tied to any stored account, just a live RPC read-through for
// whatever address the caller already knows is its own.
func HandleEthSelfCustodyBalance(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if ethRPCClient == nil {
		http.Error(w, "Sepolia RPC not enabled (no SEPOLIA_RPC_URL configured)", http.StatusServiceUnavailable)
		return
	}

	address := r.URL.Query().Get("address")
	if !common.IsHexAddress(address) {
		http.Error(w, "address must be a valid 0x-prefixed Ethereum address", http.StatusBadRequest)
		return
	}

	wei, err := ethBalance(address)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to query balance: %v", err), http.StatusBadGateway)
		return
	}
	nonce, gasPrice, err := ethNonceAndGasPrice(address)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to query nonce/gas price: %v", err), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"address":              address,
		"weiBalance":           wei.String(),
		"nonce":                strconv.FormatUint(nonce, 10),
		"suggestedGasPriceWei": gasPrice.String(),
	})
}

// HandleEthSelfCustodyBroadcast relays an already client-signed raw
// transaction to Sepolia. Deliberately NOT gated behind
// requireSignedRequest (a CreditChain Ed25519 auth scheme tied to a
// CreditChain address, which has no relationship to the Ethereum
// address that actually signed this transaction) - the real
// authorization boundary is Ethereum's own signature verification,
// enforced by every Sepolia node this transaction reaches, not this
// relay. Abuse/spam is covered by the existing global per-IP rate
// limiter (api/hardening.go) plus the cheap early rejects in
// ethBroadcastRaw (bad hex, bad RLP, wrong chain ID) that fail before
// any RPC round-trip.
func HandleEthSelfCustodyBroadcast(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if ethRPCClient == nil {
		http.Error(w, "Sepolia RPC not enabled (no SEPOLIA_RPC_URL configured)", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		RawTxHex string `json:"rawTxHex"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	txHash, err := ethBroadcastRaw(ctx, req.RawTxHex)
	if err != nil {
		http.Error(w, fmt.Sprintf("broadcast failed: %v", err), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status": "broadcast",
		"txHash": txHash,
	})
}
