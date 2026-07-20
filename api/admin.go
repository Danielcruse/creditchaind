package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/creditchain/creditchaind/core"
)

// HandleAdminWhoAmI is a public, unauthenticated read - the
// configured admin wallet address is inherently public anyway (it
// appears as AuthAddress on every admin transaction once one lands
// on-chain), so there's no confidentiality lost in letting the
// frontend ask "which wallet is the admin" up front, before an
// operator tries an action and gets a confusing 403 for being logged
// in as the wrong wallet.
func HandleAdminWhoAmI(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	json.NewEncoder(w).Encode(map[string]string{"adminWalletAddress": adminWalletAddress()})
}

// requireAdmin checks the X-Admin-Token header against ADMIN_TOKEN.
// If ADMIN_TOKEN isn't set at all, admin endpoints are disabled
// entirely (fail closed) rather than silently accepting any caller -
// an operator has to deliberately opt in to having a mint faucet at all.
//
// This is the LEGACY admin auth path, kept only for a migration
// window alongside requireAdminAuthed below (see that function's doc
// comment) - new admin actions should not be gated on this alone.
func requireAdmin(r *http.Request) bool {
	expected := os.Getenv("ADMIN_TOKEN")
	if expected == "" {
		return false
	}

	got := r.Header.Get("X-Admin-Token")
	if got == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

// adminWalletAddress is the network-wide-configured admin identity -
// an ordinary CreditChain wallet address (created the normal way via
// register.html) that every validator node is told, identically, is
// authorized to sign admin actions. Unlike ADMIN_TOKEN (a node-local
// secret only the HTTP-receiving node ever checks), this address must
// be set the same on every node: once an admin transaction carries a
// real AuthAddress, every node applying it independently verifies
// that address against this configured value - see verifyAdminTxAuth
// in apply.go. Empty means the signed-wallet admin path is disabled
// (falls back to the legacy token path only).
func adminWalletAddress() string {
	addr := strings.ToLower(strings.TrimSpace(os.Getenv("ADMIN_WALLET_ADDRESS")))
	return strings.TrimPrefix(addr, "cc")
}

// requireAdminAuthed is the migration-window admin gate. If
// ADMIN_WALLET_ADDRESS is configured, ONLY a full signed request from
// that exact address is accepted (the same Ed25519 proof-of-key-
// custody every ordinary wallet action already uses, see
// requireSignedRequest) - no token fallback, since once a network is
// migrated the legacy path's transactions would just silently no-op
// at apply time anyway (see verifyAdminTxAuth). If no admin wallet is
// configured at all, falls back to the legacy X-Admin-Token header.
// The two paths produce transactions with different on-chain trust
// models (see recordAdminTx) - the token path is a deliberate,
// temporary carryover for nodes not yet migrated, not a permanent
// second door; it's meant to be deleted once every node has
// ADMIN_WALLET_ADDRESS configured.
//
// Reads the request body itself (not via r.Body directly) since it
// may need to try decoding it two different ways - callers must not
// also read r.Body.
func requireAdminAuthed(r *http.Request, out any) (signedAuth core.SignedEnvelope, usedLegacyToken bool, err error) {
	body, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		return core.SignedEnvelope{}, false, errors.New("could not read request body")
	}

	configured := adminWalletAddress()
	if configured != "" {
		var envelope core.SignedEnvelope
		if jsonErr := json.Unmarshal(body, &envelope); jsonErr == nil {
			if verifyErr := envelope.Verify(); verifyErr == nil && envelope.Address == configured {
				if !rememberSignature(envelope.Signature) {
					return core.SignedEnvelope{}, false, errors.New("duplicate/replayed request")
				}
				if out != nil {
					if unmarshalErr := json.Unmarshal(envelope.Payload, out); unmarshalErr != nil {
						return core.SignedEnvelope{}, false, errors.New("invalid payload")
					}
				}
				return envelope, false, nil
			}
		}
		// An admin wallet IS configured but this request didn't match
		// it - do NOT fall through to the legacy token path. Once
		// configured, verifyAdminTxAuth (apply.go) only ever accepts a
		// real signature from this exact address, so accepting a
		// token-authenticated request here would just produce a
		// transaction that silently never takes effect at apply time -
		// better to fail loudly now than return a confusing "pending"
		// status for something that will quietly no-op.
		return core.SignedEnvelope{}, false, errors.New("request not signed by the configured admin wallet")
	}

	if !requireAdmin(r) {
		return core.SignedEnvelope{}, false, errors.New("admin token required")
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return core.SignedEnvelope{}, false, errors.New("invalid request")
		}
	}
	return core.SignedEnvelope{}, true, nil
}

// recordAdminTx picks the transaction's Auth/From fields between the
// two admin auth models requireAdminAuthed can return: the legacy
// path carries this node's own self-signed identity (the pre-existing
// trust model - every node trusts whichever node received the HTTP
// request to have checked ADMIN_TOKEN correctly); the signed-wallet
// path carries a real, independently-reverifiable signature from the
// configured admin wallet, which every node applying the transaction
// re-checks itself (see verifyAdminTxAuth in apply.go).
func recordAdminTx(signedAuth core.SignedEnvelope, usedLegacyToken bool, to string, amount uint64, txType core.TransactionType, data string) core.Transaction {
	if usedLegacyToken {
		return recordTx(selfSignedEnvelope([]byte(data)), reporterAddress(), to, amount, txType, data)
	}
	return recordTx(signedAuth, signedAuth.Address, to, amount, txType, data)
}

// verifyAdminTxAuth is the verifyTxAuth-side counterpart to
// requireAdminAuthed/recordAdminTx - every node applying an
// admin-only transaction runs this itself, independently of whichever
// node originally received the HTTP request.
//
// Once an admin wallet is configured, this ONLY accepts a genuine
// signature from that exact address (core.VerifyTransactionAuth,
// already called before this by every ApplyTransaction path, already
// confirmed the signature really does bind to tx.AuthAddress - this
// just checks that address is the right one) - the legacy
// Data-equals-AuthPayload fallback is deliberately NOT tried in that
// case. That fallback is a self-consistency check, not an identity
// check: it's equally true for a genuine signed transaction from ANY
// wallet, admin or not, since every ordinary signed-transaction call
// site in this codebase already re-marshals its own payload into both
// tx.Data and the envelope it signs. Falling through to it once a
// real admin wallet is configured would let an attacker take any
// unrelated wallet's genuinely-signed transaction and simply mislabel
// its type as an admin one to sail through - the fallback is only
// safe while NO admin wallet is configured at all (true legacy-only
// nodes, mid-migration-window, matching this codebase's original
// self-signed-node trust model for these transaction types).
func verifyAdminTxAuth(tx core.Transaction) bool {
	if configured := adminWalletAddress(); configured != "" {
		return tx.AuthAddress == configured
	}
	return tx.Data == string(tx.AuthPayload)
}
