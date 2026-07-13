package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var moderatorStore = core.NewModeratorStore()

// MinModeratorReputation matches the default reputation a fresh
// wallet starts with (core.NewWallet), same reasoning as
// MinValidatorReputation - any wallet in ordinary good standing is
// immediately eligible.
const MinModeratorReputation = 50

// ModeratorRegisterFeeCCT is a CCT anti-spam cost for registering as a
// moderator - same pattern as HubApplyFeeCCT/ValidatorRegisterFeeCCT,
// paid to registrationFeeAddress (api/apply.go). Lowest of the three
// registration fees: moderating carries real trust but less exposure
// than running a hub or a validator.
const ModeratorRegisterFeeCCT = 10

type moderatorInfo struct {
	Address       string `json:"address"`
	Confirmations uint64 `json:"confirmations"`
	Disputes      uint64 `json:"disputes"`
	RegisteredAt  int64  `json:"registeredAt"`
}

func toModeratorInfo(m *core.ModeratorStatus) moderatorInfo {
	return moderatorInfo{
		Address:       m.Address,
		Confirmations: m.Confirmations,
		Disputes:      m.Disputes,
		RegisteredAt:  m.RegisteredAt,
	}
}

// HandleRegisterModerator lets any wallet in good standing (same
// reputation floor as becoming a validator) register as a moderator -
// automatically assigned going forward to observe and attest to new
// trade agreements (see core/moderator.go's doc comment on what that
// attestation is and isn't). Registering grants a one-time reputation
// bump, which mechanically increases validator selection weight too
// (reputation is 55% of it - see consensus.go's validatorWeight),
// since moderating trades in good faith is itself a form of
// participation worth rewarding.
func HandleRegisterModerator(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	type request struct{}
	var req request

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if moderatorStore.IsModerator(address) {
		http.Error(w, "already registered as a moderator", http.StatusBadRequest)
		return
	}

	wallet := walletStore.Get(address)
	if wallet.Reputation < MinModeratorReputation {
		http.Error(w, "reputation too low to become a moderator", http.StatusBadRequest)
		return
	}

	if tokenStore.BalanceOf(address) < ModeratorRegisterFeeCCT {
		http.Error(w, fmt.Sprintf("you need at least %d CCT to cover the moderator registration fee", ModeratorRegisterFeeCCT), http.StatusBadRequest)
		return
	}

	tx := recordTx(auth, address, "", 0, core.TxModeratorRegister, "{}")

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleListModerators is a public read of every registered moderator
// and their own confirm/dispute track record - lets anyone weigh a
// moderator's history before relying on their assignment.
func HandleListModerators(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	list := []moderatorInfo{}
	for _, m := range moderatorStore.List() {
		list = append(list, toModeratorInfo(m))
	}
	json.NewEncoder(w).Encode(list)
}

// moderationConfirmData is the payload for a moderator's on-chain
// attestation about one specific trade - Kind picks which store owns
// the ID (negotiations and P2P offers are separate stores with
// separately-numbered IDs).
type moderationConfirmData struct {
	Kind     string `json:"kind"` // "negotiation" or "p2p"
	ID       string `json:"id"`
	Disputed bool   `json:"disputed"`
}

// HandleConfirmTrade is the assigned moderator attesting whether a
// trade agreement concluded successfully. This never gates or
// reverses the trade's own fund movement (negotiations execute via
// the lender's own confirm, P2P offers settle atomically on accept) -
// it's an additional, disclosed trust layer recorded on chain, not an
// escrow release mechanism (see core/moderator.go's doc comment).
func HandleConfirmTrade(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req moderationConfirmData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !moderatorStore.IsModerator(address) {
		http.Error(w, "not a registered moderator", http.StatusForbidden)
		return
	}

	switch req.Kind {
	case "negotiation":
		n, ok := negotiationStore.Get(req.ID)
		if !ok {
			http.Error(w, "negotiation not found", http.StatusNotFound)
			return
		}
		if n.ModeratorAddress != address {
			http.Error(w, "this trade wasn't assigned to you", http.StatusForbidden)
			return
		}
		if n.ModerationStatus != core.ModerationPending {
			http.Error(w, "this trade has already been moderated", http.StatusBadRequest)
			return
		}
	case "p2p":
		o, ok := p2pOfferStore.Get(req.ID)
		if !ok {
			http.Error(w, "p2p offer not found", http.StatusNotFound)
			return
		}
		if o.PaymentMode == core.PaymentModeExternal {
			http.Error(w, "external-payment trades settle through /p2p/offers/resolve-dispute, not this generic attestation endpoint", http.StatusBadRequest)
			return
		}
		if o.ModeratorAddress != address {
			http.Error(w, "this trade wasn't assigned to you", http.StatusForbidden)
			return
		}
		if o.ModerationStatus != core.ModerationPending {
			http.Error(w, "this trade has already been moderated", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "kind must be 'negotiation' or 'p2p'", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxModeratorConfirm, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}
