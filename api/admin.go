package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/creditchain/creditchaind/core"
)

// HandleAdminWhoAmI is a public, unauthenticated read - the
// configured admin wallet address is inherently public anyway (it
// appears as AuthAddress on every admin transaction once one lands
// on-chain), so there's no confidentiality lost in letting the
// frontend ask "which wallet is the admin" up front, before an
// operator tries an action and gets a confusing 403 for being logged
// in as the wrong wallet. Also surfaces multisig configuration, once
// set, for the same reason.
func HandleAdminWhoAmI(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	json.NewEncoder(w).Encode(map[string]any{
		"adminWalletAddress": adminWalletAddress(),
		"multisigEnabled":    adminMultisigEnabled(),
		"multisigSigners":    adminMultisigSigners(),
		"multisigThreshold":  adminMultisigThreshold(),
	})
}

// normalizeAdminAddress converts a user/operator-supplied admin
// address string into the raw form core.GenerateAddress produces (40
// lowercase hex characters, no prefix) - handles both that raw form
// and the "cc"-prefixed display form the website shows (see task
// #267's cosmetic address prefix). Critically, this only strips a
// leading "cc" when doing so leaves exactly 40 characters behind - a
// raw address whose own first real byte happens to be 0xcc (roughly
// 1 in 256 addresses - confirmed to actually occur, not just a
// theoretical edge case, while testing this feature) must NOT have
// those two genuine hex characters stripped, or it would silently
// resolve to the wrong 20-byte address and lock out a legitimate
// signer/admin wallet with no obvious error message pointing at why.
func normalizeAdminAddress(raw string) string {
	return normalizeCreditChainAddress(raw)
}

// normalizeCreditChainAddress is normalizeAdminAddress's transform under
// a name that doesn't imply "admin" - stripping the cosmetic "cc"
// prefix applies to any CreditChain address, not just the configured
// admin/operator identities normalizeAdminAddress's own call sites deal
// with. Callers that accept an arbitrary caller-supplied destination
// address (e.g. a mint or balance-recovery target) need this same
// normalization too - without it, an address typed in its normal
// "cc"-prefixed display form resolves to a completely different,
// unreachable 42-character key instead of the real 40-character
// address the recipient's wallet actually signs with (see
// api/token_mint.go and api/admin_transfer.go's own doc comments for
// the incident this closes).
func normalizeCreditChainAddress(raw string) string {
	addr := strings.ToLower(strings.TrimSpace(raw))
	if len(addr) == 42 && strings.HasPrefix(addr, "cc") {
		addr = addr[2:]
	}
	return addr
}

// adminMultisigSigners/adminMultisigThreshold/adminMultisigEnabled/
// isAdminMultisigSigner are the multisig counterpart to
// adminWalletAddress below - ADMIN_MULTISIG_ADDRESSES (comma-
// separated wallet addresses) and ADMIN_MULTISIG_THRESHOLD (an
// integer M) must be set identically on every node, same network-
// wide-consistency requirement as ADMIN_WALLET_ADDRESS/NETWORK_SECRET,
// since every node independently evaluates whether a proposal has
// reached threshold. Once both are configured, this supersedes the
// single-key ADMIN_WALLET_ADDRESS model entirely for the admin action
// types listed in allowedAdminMultisigActionType (see
// requireAdminAuthed/verifyAdminTxAuth) - no single key, however it's
// stored, can act alone anymore.
func adminMultisigSigners() []string {
	// Once a protocol manifest has been committed (audit finding C-09 -
	// see core.ProtocolManifest's doc comment), it's the source of
	// truth every node genuinely agrees on, not this process's own
	// local environment - falls back to the raw env var only in the
	// pre-manifest bootstrap state.
	if core.ActiveProtocolManifest.Set {
		return core.ActiveProtocolManifest.AdminMultisigAddresses
	}
	raw := os.Getenv("ADMIN_MULTISIG_ADDRESSES")
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		addr := normalizeAdminAddress(part)
		if addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

func adminMultisigThreshold() int {
	if core.ActiveProtocolManifest.Set {
		return core.ActiveProtocolManifest.AdminMultisigThreshold
	}
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("ADMIN_MULTISIG_THRESHOLD")))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// adminMultisigEnabled requires a threshold that's actually reachable
// by the configured signer set - a misconfigured M > N would silently
// make every admin action permanently unexecutable (indistinguishable
// from "working as intended, just waiting for more signers"), which
// is a worse failure mode than refusing to enable multisig at all
// until the operator fixes it.
func adminMultisigEnabled() bool {
	signers := adminMultisigSigners()
	threshold := adminMultisigThreshold()
	return len(signers) > 0 && threshold > 0 && threshold <= len(signers)
}

func isAdminMultisigSigner(address string) bool {
	normalized := normalizeAdminAddress(address)
	for _, s := range adminMultisigSigners() {
		if s == normalized {
			return true
		}
	}
	return false
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
	if core.ActiveProtocolManifest.Set {
		return core.ActiveProtocolManifest.AdminWalletAddress
	}
	return normalizeAdminAddress(os.Getenv("ADMIN_WALLET_ADDRESS"))
}

// adminAuthMode records which of the three admin trust tiers
// authenticated a given request - requireAdminAuthed's caller uses
// this to decide how recordAdminTx should record the resulting
// transaction (direct admin action vs. a multisig proposal, see
// recordAdminTx's doc comment).
type adminAuthMode int

const (
	adminAuthLegacyToken adminAuthMode = iota
	adminAuthSingleWallet
	adminAuthMultisig
)

// requireAdminAuthed is the migration-window admin gate, now with
// three tiers instead of two. If multisig is configured
// (adminMultisigEnabled), ONLY a signed request from one of the
// configured signer addresses is accepted - this supersedes the
// single-wallet tier below entirely, not merely takes priority over
// it, since a single genuinely-signed request must never be enough to
// execute an admin action once multisig governs (see recordAdminTx).
// Otherwise, if ADMIN_WALLET_ADDRESS is configured, that single
// address's signature is required (unchanged from before multisig
// existed). Otherwise, falls back to the legacy X-Admin-Token header.
// Each tier is a deliberate, temporary carryover for networks not yet
// migrated to the next one, not a permanent parallel door.
//
// Reads the request body itself (not via r.Body directly) since it
// may need to try decoding it two different ways - callers must not
// also read r.Body.
func requireAdminAuthed(r *http.Request, out any) (signedAuth core.SignedEnvelope, mode adminAuthMode, err error) {
	body, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		return core.SignedEnvelope{}, adminAuthLegacyToken, errors.New("could not read request body")
	}

	if adminMultisigEnabled() {
		var envelope core.SignedEnvelope
		if jsonErr := json.Unmarshal(body, &envelope); jsonErr == nil {
			if verifyErr := envelope.Verify(); verifyErr == nil && isAdminMultisigSigner(envelope.Address) {
				if !rememberSignature(envelope.Signature) {
					return core.SignedEnvelope{}, adminAuthMultisig, errors.New("duplicate/replayed request")
				}
				if out != nil {
					if unmarshalErr := json.Unmarshal(envelope.Payload, out); unmarshalErr != nil {
						return core.SignedEnvelope{}, adminAuthMultisig, errors.New("invalid payload")
					}
				}
				return envelope, adminAuthMultisig, nil
			}
		}
		// Multisig IS configured but this request wasn't signed by one
		// of its authorized signers - do NOT fall through to the
		// single-wallet or token tiers. Once multisig governs,
		// verifyAdminTxAuth (apply.go) rejects every admin action type
		// outright unless it went through the propose/approve path, so
		// accepting a different credential here would just produce a
		// transaction that silently never takes effect - better to
		// fail loudly now.
		return core.SignedEnvelope{}, adminAuthMultisig, errors.New("request not signed by an authorized multisig signer")
	}

	configured := adminWalletAddress()
	if configured != "" {
		var envelope core.SignedEnvelope
		if jsonErr := json.Unmarshal(body, &envelope); jsonErr == nil {
			if verifyErr := envelope.Verify(); verifyErr == nil && envelope.Address == configured {
				if !rememberSignature(envelope.Signature) {
					return core.SignedEnvelope{}, adminAuthSingleWallet, errors.New("duplicate/replayed request")
				}
				if out != nil {
					if unmarshalErr := json.Unmarshal(envelope.Payload, out); unmarshalErr != nil {
						return core.SignedEnvelope{}, adminAuthSingleWallet, errors.New("invalid payload")
					}
				}
				return envelope, adminAuthSingleWallet, nil
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
		return core.SignedEnvelope{}, adminAuthSingleWallet, errors.New("request not signed by the configured admin wallet")
	}

	if !requireAdmin(r) {
		return core.SignedEnvelope{}, adminAuthLegacyToken, errors.New("admin token required")
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return core.SignedEnvelope{}, adminAuthLegacyToken, errors.New("invalid request")
		}
	}
	return core.SignedEnvelope{}, adminAuthLegacyToken, nil
}

// recordAdminTx picks how to record the resulting transaction based
// on which of the three admin trust tiers requireAdminAuthed
// returned. Legacy: this node's own self-signed identity (every node
// trusts whichever node received the HTTP request to have checked
// ADMIN_TOKEN correctly). Single wallet: a real, independently-
// reverifiable signature from the configured admin wallet, re-checked
// by every node (see verifyAdminTxAuth). Multisig: the action does
// NOT execute directly at all - it's wrapped in a
// TxAdminMultisigPropose transaction instead (see
// api/admin_multisig.go), and only actually takes effect once enough
// other authorized signers separately approve it. txType/data here
// describe the INTENDED action; in multisig mode they become the
// proposal's ActionType/Payload rather than the transaction's own
// Type/Data.
func recordAdminTx(signedAuth core.SignedEnvelope, mode adminAuthMode, to string, amount uint64, txType core.TransactionType, data string) core.Transaction {
	switch mode {
	case adminAuthMultisig:
		proposeData, _ := json.Marshal(adminMultisigProposeData{ActionType: txType, Payload: data})
		return recordTx(signedAuth, signedAuth.Address, to, amount, core.TxAdminMultisigPropose, string(proposeData))
	case adminAuthSingleWallet:
		return recordTx(signedAuth, signedAuth.Address, to, amount, txType, data)
	default:
		return recordTx(selfSignedEnvelope([]byte(data)), reporterAddress(), to, amount, txType, data)
	}
}

// verifyAdminTxAuth is the verifyTxAuth-side counterpart to
// requireAdminAuthed/recordAdminTx - every node applying an
// admin-only transaction runs this itself, independently of whichever
// node originally received the HTTP request.
//
// Once multisig is configured, this ALWAYS rejects a direct
// submission of any of these transaction types outright - going
// forward they can only ever take effect via
// executeAdminMultisigAction (api/admin_multisig.go), called
// internally once a real proposal reaches its approval threshold and
// deliberately bypassing this check entirely (auth was already
// established through the propose/approve chain, not through this
// synthetic transaction's own signature). Accepting a directly-
// submitted one here - even from the address that used to be the
// single admin wallet - would reopen exactly the single-key bypass
// multisig exists to close.
//
// Otherwise (multisig not configured), behavior is unchanged from
// before multisig existed: once a single admin wallet is configured,
// this ONLY accepts a genuine signature from that exact address
// (core.VerifyTransactionAuth, already called before this by every
// ApplyTransaction path, already confirmed the signature really does
// bind to tx.AuthAddress - this just checks that address is the right
// one) - the legacy Data-equals-AuthPayload fallback is deliberately
// NOT tried in that case. That fallback is a self-consistency check,
// not an identity check: it's equally true for a genuine signed
// transaction from ANY wallet, admin or not, since every ordinary
// signed-transaction call site in this codebase already re-marshals
// its own payload into both tx.Data and the envelope it signs.
// Falling through to it once a real admin wallet is configured would
// let an attacker take any unrelated wallet's genuinely-signed
// transaction and simply mislabel its type as an admin one to sail
// through - the fallback is only safe while NEITHER an admin wallet
// NOR multisig is configured at all (true legacy-only nodes,
// mid-migration-window, matching this codebase's original
// self-signed-node trust model for these transaction types).
func verifyAdminTxAuth(tx core.Transaction) bool {
	if adminMultisigEnabled() {
		return false
	}
	if configured := adminWalletAddress(); configured != "" {
		return tx.AuthAddress == configured
	}
	return tx.Data == string(tx.AuthPayload)
}
