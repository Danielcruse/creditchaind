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

// EthCustodialAccountFeeCCT is an anti-Sybil cost for provisioning a new
// custodial signing key - unlike Hub/Moderator/Validator registration fees
// (10-50 CCT, pure anti-spam), this specific action creates a real,
// recurring-cost external resource the operator pays for (one AWS KMS key
// per account, ~$1/month indefinitely once created - see
// ops/ETH_CUSTODIAL_SETUP.md's cost note). CreditChain wallet creation
// itself is free and instant (client-side, no signup cost - see the HD
// wallet work in core/address.go), so without this fee a script could
// generate unlimited wallets and call this endpoint once per wallet,
// each call provisioning a real KMS key with no requirement to ever fund
// or use it - a real, unbounded cost-exhaustion vector against the
// operator's AWS bill, not just a spam nuisance. Charged directly and
// synchronously in the handler (not via the usual recordTx/apply
// pending-tx pipeline) because this entire feature is already,
// deliberately, a synchronous/node-local exception to that pipeline (see
// this file's own top-of-file doc comment). Set higher than the flat
// registration fees since it's funding an ongoing cost, not a one-time
// anti-spam cost - a judgment call, not derived from any specified
// value; still only a partial fix (a one-time fee doesn't stop an
// abandoned/never-funded account from costing the operator money forever
// after that single charge) - see audit/KNOWN_GAPS.md's entry on the
// still-needed recurring-renewal + ScheduleKeyDeletion-on-lapse follow-up.
const EthCustodialAccountFeeCCT = 100

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

// HandleCreateEthCustodialAccount is RETIRED as a live account-creation
// path. Outernet's ETH story is now exactly two things: self-custodial
// ETH (the user's own HD-derived key, never seen by CreditChain - see
// api/eth_selfcustody.go) and ETHB, the bridged ledger credit that
// mirrors that same self-custodial address's balance (see
// api/eth_bridge.go's ethAddressStore/HandleEthRegisterAddress, the
// same free, non-custodial watched-address model core/btc_address.go
// already used for BTCB). A CreditChain-generated, CreditChain-held
// signing key has no place in that story, so this endpoint no longer
// provisions one - it returns a clear, disclosed redirect instead of a
// bare 404, matching this project's "disclose, don't silently break"
// convention (see api/token_registry.go's HandleRegisterToken for the
// precedent this mirrors).
//
// The underlying core.EthCustodialStore/EthCustodialAccount type and
// the read/withdraw endpoints below are deliberately NOT deleted -
// never strand funds: if any account was already provisioned under the
// old design (see api/eth_bridge.go's deposit-detection loop, which
// still polls ethCustodialStore for backward compatibility), that
// account's owner can still see its balance and withdraw it via those
// endpoints - just no NEW account can ever be created, and this
// mechanism is no longer surfaced anywhere in the product UI as an
// account a user is meant to actively use.
func HandleCreateEthCustodialAccount(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	http.Error(w, "custodial ETH accounts are retired - CreditChain never holds an ETH signing key on your behalf. Use your self-custodial ETH account (derived from your own recovery phrase) instead; ETHB automatically mirrors its balance once you register it via /eth-bridge/register-address.", http.StatusGone)
}

// HandleEthCustodialBalance returns the account's real, live Sepolia
// balance (queried fresh from the RPC node every call - never cached or
// tracked internally, since this corner has no internal ledger of its
// own; the real chain is the only source of truth for this balance,
// unlike CCT/ETHTEST/USDCTEST which are internally tracked balances).
// HandleEthCustodialAddress is a lightweight, RPC-free lookup of a
// wallet's real custodial Sepolia address - unlike
// HandleEthCustodialBalance, this never touches ethRPCClient, so it
// works even when no Sepolia RPC is configured (local dev/tests).
// This is what the frontend now calls to resolve "my ETHB address"
// (see website/js/api.js's getEthbAddress) instead of computing the
// old derived-hash sub-address locally, matching resolveBalanceAddress's
// same real-address-first, derived-hash-fallback resolution on the Go
// side (see api/subaddress.go) - without this, the two sides would
// disagree the moment a custodial account exists.
func HandleEthCustodialAddress(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	account, ok := ethCustodialStore.Get(address)
	if !ok {
		http.Error(w, "no custodial ETH account for that address", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"ethAddress": account.EthAddress})
}

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
