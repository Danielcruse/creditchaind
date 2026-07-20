package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// createWalletRequest is what the client now sends: the address and
// public key it already derived itself (see website/js/hdwallet.js),
// never a private key. This handler never generates a keypair - key
// generation moved entirely client-side per the Multi-Chain HD Wallet
// Phase A plan (§A.3), so the server never sees, and cannot leak, a
// private key for a self-custodial wallet.
type createWalletRequest struct {
	Address   string `json:"address"`
	PublicKey string `json:"publicKey"`
	// ReferredBy is optional - the referrer's own CreditChain address
	// (see core.Wallet.ReferredBy's doc comment for why there's no
	// separate referral-code system). Never validated against a real
	// existing wallet - a referrer who hasn't registered yet, or a
	// typo, just means ReferralCount never counts this row; harmless
	// either way for a reputation/voting-weight bonus with no real
	// value at stake.
	ReferredBy string `json:"referredBy"`
}

func HandleCreateWallet(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	var req createWalletRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	pubBytes, err := hex.DecodeString(req.PublicKey)
	if err != nil || len(pubBytes) == 0 {
		http.Error(w, "invalid publicKey", http.StatusBadRequest)
		return
	}
	if req.Address == "" || core.GenerateAddress(pubBytes) != req.Address {
		http.Error(w, "address does not match publicKey", http.StatusBadRequest)
		return
	}

	// Register a canonical core.Wallet (balances/reputation/etc.) under
	// this address so the rest of the system - staking, reputation,
	// credit tier - has somewhere to persist state going forward.
	// WalletStore.Register is a no-op if this address already exists
	// (idempotent - safe to call again, e.g. if the client retries),
	// so CreatedAt is only ever stamped once, on first registration.
	registered := core.NewWallet()
	registered.Address = req.Address
	registered.CreatedAt = time.Now().Unix()
	registered.LastActivityAt = time.Now().Unix()
	if req.ReferredBy != "" && req.ReferredBy != req.Address {
		registered.ReferredBy = req.ReferredBy
	}
	walletStore.Register(&registered)

	json.NewEncoder(w).Encode(
		map[string]string{
			"address":   req.Address,
			"publicKey": req.PublicKey,
		},
	)
}
