package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/creditchain/creditchaind/core"
)

// setProtocolManifestData carries the same fields ProtocolManifest
// stores - see that struct's doc comment (core/protocol_manifest.go)
// for the full C-09/C-06 reasoning this closes. AdminMultisigAddresses/
// BridgeValidatorAddresses are comma-separated strings on the wire
// (matching how ADMIN_MULTISIG_ADDRESSES/BRIDGE_VALIDATOR_ADDRESSES are
// already written as env vars) rather than JSON arrays, so an operator
// filling this in from their existing systemd drop-in can paste the
// exact same value through unchanged.
type setProtocolManifestData struct {
	MainnetMode              bool   `json:"mainnetMode"`
	AdminWalletAddress       string `json:"adminWalletAddress"`
	AdminMultisigAddresses   string `json:"adminMultisigAddresses"`
	AdminMultisigThreshold   int    `json:"adminMultisigThreshold"`
	BridgeValidatorAddresses string `json:"bridgeValidatorAddresses"`
	BridgeValidatorThreshold int    `json:"bridgeValidatorThreshold"`
}

// HandleSetProtocolManifest lets an admin commit the network's
// security-critical configuration into replicated consensus state -
// see core.ProtocolManifest's doc comment. Authorized by whichever
// admin mechanism (single wallet or multisig) is CURRENTLY configured
// via local environment variables - that's exactly what bootstraps the
// first commit, the same "local config authorizes the action that then
// becomes consensus state" pattern TxAdminSetParam already uses for
// governance parameters.
func HandleSetProtocolManifest(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req setProtocolManifestData
	signedAuth, mode, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxSetProtocolManifest, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// applySetProtocolManifest is the consensus-critical write - every
// node applies this identically, which is the entire point: once this
// runs, core.ActiveProtocolManifest.Set becomes true everywhere, and
// every consumer (adminWalletAddress, bridgeValidatorThreshold,
// mainnetMode, etc.) switches from reading its own local environment
// to reading this replicated value instead.
func applySetProtocolManifest(tx core.Transaction) {
	var data setProtocolManifestData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	core.ActiveProtocolManifest = core.ProtocolManifest{
		Set:                      true,
		MainnetMode:              data.MainnetMode,
		AdminWalletAddress:       normalizeAdminAddress(data.AdminWalletAddress),
		AdminMultisigAddresses:   splitAddressList(data.AdminMultisigAddresses),
		AdminMultisigThreshold:   data.AdminMultisigThreshold,
		BridgeValidatorAddresses: splitAddressList(data.BridgeValidatorAddresses),
		BridgeValidatorThreshold: data.BridgeValidatorThreshold,
		SetAt:                    tx.Timestamp,
		SetBy:                    tx.AuthAddress,
	}
	log.Printf("protocol manifest committed by %s: mainnetMode=%v adminWallet=%s adminMultisig=%d-of-%v bridgeValidators=%d-of-%v",
		tx.AuthAddress, data.MainnetMode, core.ActiveProtocolManifest.AdminWalletAddress,
		data.AdminMultisigThreshold, core.ActiveProtocolManifest.AdminMultisigAddresses,
		data.BridgeValidatorThreshold, core.ActiveProtocolManifest.BridgeValidatorAddresses)
}

// verifySetProtocolManifestAuth is deliberately NOT verifyAdminTxAuth's
// generic legacy-fallback pattern. That function - by design, see its
// own doc comment - accepts ANY signed transaction whenever neither an
// admin wallet nor multisig is configured yet, a tradeoff acceptable
// for something like adjusting a single governance param but NOT
// acceptable here: this transaction type decides WHO the admin even
// is, so the generic fallback would let any wallet self-appoint as
// admin the moment a node happens to have no admin configured at all -
// a privilege-escalation risk specific to this one transaction type,
// worse than the equivalent risk for an ordinary admin action.
//
// Once a manifest has ALREADY been committed, only the address it
// itself names as admin (or a passing multisig proposal under its own
// committed signer set - adminMultisigEnabled/verifyAdminTxAuth both
// already read the manifest once Set is true, see adminWalletAddress's
// own doc comment) may change it - self-referential authority, the
// same way a real access-control list can only be edited by someone
// already on it. Before any manifest has ever been committed, this
// requires a genuinely pre-configured ADMIN_WALLET_ADDRESS (the
// deliberate, operator-chosen bootstrap case) - it does NOT fall back
// further to the bare Data==AuthPayload legacy shape the way
// verifyAdminTxAuth does, closing the self-appoint gap for exactly
// this one transaction type.
func verifySetProtocolManifestAuth(tx core.Transaction) bool {
	if core.ActiveProtocolManifest.Set {
		return verifyAdminTxAuth(tx)
	}
	if adminMultisigEnabled() {
		return false
	}
	configured := adminWalletAddress()
	if configured == "" {
		return false
	}
	return tx.AuthAddress == configured
}

// splitAddressList mirrors adminMultisigSigners/bridgeValidatorAddresses'
// own comma-split-and-normalize logic (api/admin.go, api/bridge_validator.go)
// so a manifest commit parses its address-list fields identically to how
// the equivalent environment variables have always been parsed.
func splitAddressList(raw string) []string {
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
