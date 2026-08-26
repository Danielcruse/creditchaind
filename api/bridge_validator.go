package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/creditchain/creditchaind/core"
)

// bridgeValidatorAddresses/bridgeValidatorThreshold mirror
// adminMultisigSigners/adminMultisigThreshold (api/admin.go) for a
// deliberately SEPARATE signer set - see core/bridge_validator.go's doc
// comment for why vault custody keys aren't the same registry as
// internal admin governance keys. BRIDGE_VALIDATOR_ADDRESSES (comma-
// separated wallet addresses) and BRIDGE_VALIDATOR_THRESHOLD must be
// set identically on every node, same network-wide-consistency
// requirement every other threshold mechanism in this codebase already
// has, since every node independently evaluates whether an
// attestation/approval has reached threshold.
//
// isBridgeValidatorAddress (below) has TWO eligibility paths again, as
// of 2026-08-20 (temporary restoration - see below): an address
// configured directly in BRIDGE_VALIDATOR_ADDRESSES, OR any address
// that is a genuine, currently-active CreditChain PoSR validator
// (isRealPoSRValidator, below).
//
// History: earlier the same day, the allowlist was removed entirely
// (PoSR-only), per explicit direction to tie bridge-validator trust
// solely to CreditChain's real, economically-secured validator set.
// That shipped and immediately broke real production signing: mainnet
// has exactly one real PoSR validator, and neither of the two wallets
// actually registered as bridge validators (the ONLY two addresses the
// already-deployed CreditChainVault.sol contract's own on-chain
// validators[] recognizes - a signature from anyone else would fail
// the contract's ecrecover check regardless of what CreditChain's own
// consensus thinks) is that PoSR validator. A pending real release got
// stuck with no eligible signer at all. Restored the allowlist
// alongside PoSR eligibility (this is the pre-2026-08-20 additive
// design, not a full revert of the confirmation-depth work - see
// vault_release_approve_watch.go/vault_lock.go, which stays PoSR-only-
// agnostic and untouched) to unblock signing immediately.
//
// Real fix, not yet done: get one of the two existing bridge-validator
// wallets (174db2f0.../5457b6ce... - see bridge-validator/list) through
// CreditChain's actual validator process (funded to
// core.MinValidatorStake, validatorApplicationStore approval via
// governance support + emailed confirmation, then real registration) so
// it becomes independently PoSR-eligible - at which point the allowlist
// entry for it becomes redundant, not load-bearing. Revisit removing
// the allowlist again only once that's true for every currently-
// necessary signer. See audit/KNOWN_GAPS.md's entry on this incident.
func bridgeValidatorAddresses() []string {
	// Once a protocol manifest has been committed (audit finding C-09 -
	// see core.ProtocolManifest's doc comment), it's the source of
	// truth every node genuinely agrees on, not this process's own
	// local environment - falls back to the raw env var only in the
	// pre-manifest bootstrap state.
	if core.ActiveProtocolManifest.Set {
		return core.ActiveProtocolManifest.BridgeValidatorAddresses
	}
	raw := os.Getenv("BRIDGE_VALIDATOR_ADDRESSES")
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

func bridgeValidatorThreshold() int {
	if core.ActiveProtocolManifest.Set {
		return core.ActiveProtocolManifest.BridgeValidatorThreshold
	}
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("BRIDGE_VALIDATOR_THRESHOLD")))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func isBridgeValidatorAddress(address string) bool {
	normalized := normalizeAdminAddress(address)
	for _, a := range bridgeValidatorAddresses() {
		if a == normalized {
			return true
		}
	}
	return isRealPoSRValidator(normalized)
}

// isRealPoSRValidator is the second eligibility path (alongside the
// restored BRIDGE_VALIDATOR_ADDRESSES allowlist above) - see the doc
// comment on this file's own header for the full reasoning. Deliberately
// mirrors core.isEligible's exact thresholds (Active, not
// PermanentlyBanned, staked >= core.MinValidatorStake, reputation >=
// core.MinValidatorReputationGate) rather than reusing that unexported
// function directly, since core.isEligible takes a core.Validator - a
// type assembled by the consensus hot path from several stores together
// (see core/consensus.go's SelectValidator) that this call site has no
// reason to reconstruct. Computed directly from the same stores
// instead, so the two checks can never silently drift on WHAT they
// check even though they can't share the same function body.
func isRealPoSRValidator(address string) bool {
	status, ok := validatorStore.Get(address)
	if !ok || !status.Active || status.PermanentlyBanned {
		return false
	}
	if stakingStore.StakeOf(address, "CCT") < core.MinValidatorStake {
		return false
	}
	if walletStore.Get(address).Reputation < core.MinValidatorReputationGate {
		return false
	}
	return true
}

// bridgeValidatorRegisterData is TxBridgeValidatorRegister's payload -
// comparable (plain strings only) so verifyTxAuth's signed==claimed
// check works the same way every other wallet-signed action's does.
type bridgeValidatorRegisterData struct {
	EthSigningAddress string `json:"ethSigningAddress"`
	BtcPublicKeyHex   string `json:"btcPublicKeyHex"`
}

// HandleBridgeValidatorRegister lets a pre-authorized address (one
// listed in BRIDGE_VALIDATOR_ADDRESSES) declare the real external-chain
// keys it will use to attest vault lock events and co-sign releases.
// Only a loose shape check happens here - a wrong/malformed value can
// only ever make THAT validator's own attestations/signatures never
// verify (harmless, same "wrong value just never matches" reasoning
// EthWatchedAddress/BtcWatchedAddress registration already uses), never
// let anyone else's funds move.
func HandleBridgeValidatorRegister(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req bridgeValidatorRegisterData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if !isBridgeValidatorAddress(address) {
		http.Error(w, "address is not a configured bridge validator", http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(req.EthSigningAddress, "0x") || len(req.EthSigningAddress) != 42 {
		http.Error(w, "ethSigningAddress must be a 0x-prefixed 20-byte address", http.StatusBadRequest)
		return
	}
	if len(req.BtcPublicKeyHex) != 66 {
		http.Error(w, "btcPublicKeyHex must be a 33-byte compressed secp256k1 public key (66 hex chars)", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxBridgeValidatorRegister, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

// HandleBridgeValidatorList is a public, unauthenticated read - the
// current signer set is exactly what a P2WSH redeem-script builder or
// an ETH vault contract's setValidators call needs, and it's visible
// on-chain anyway once any TxBridgeValidatorRegister confirms.
func HandleBridgeValidatorList(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	json.NewEncoder(w).Encode(bridgeValidatorStore.Snapshot())
}

// applyBridgeValidatorRegister re-checks everything the handler already
// checked (never trust submission-time state - the address list could
// have changed between submission and confirmation) before recording
// the declared keys.
func applyBridgeValidatorRegister(tx core.Transaction) {
	var data bridgeValidatorRegisterData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if !isBridgeValidatorAddress(tx.AuthAddress) {
		return
	}
	if !strings.HasPrefix(data.EthSigningAddress, "0x") || len(data.EthSigningAddress) != 42 {
		return
	}
	if len(data.BtcPublicKeyHex) != 66 {
		return
	}

	bridgeValidatorStore.Set(&core.BridgeValidator{
		CreditChainAddress: tx.AuthAddress,
		EthSigningAddress:  data.EthSigningAddress,
		BtcPublicKeyHex:    data.BtcPublicKeyHex,
		RegisteredAt:       tx.Timestamp,
	})
}
