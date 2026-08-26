package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// promotionSecondsPerRC/promotionMaxDurationSeconds are judgment
// calls, not derived from any spec: spending more RC buys more
// promoted-visibility time, up to a hard ceiling, so promotion can
// never be bought indefinitely by one large burn.
const (
	promotionSecondsPerRC       = 3600        // 1 hour of promotion per RC spent
	promotionMaxDurationSeconds = 3 * 24 * 3600 // hard ceiling: 3 days
)

// promotionDurationForRCAmount is the one place "how much promoted
// time does N RC buy" is decided - shared between the fast-fail
// preview (none needed today, kept simple) and the authoritative
// apply-time calculation.
func promotionDurationForRCAmount(amount uint64) int64 {
	d := int64(amount) * promotionSecondsPerRC
	if d > promotionMaxDurationSeconds {
		d = promotionMaxDurationSeconds
	}
	return d
}

type proposalPromoteData struct {
	ProposalID string `json:"proposalId"`
	RCAmount   uint64 `json:"rcAmount"`
}

// HandlePromoteProposal lets any wallet burn its own RC to keep a
// still-open Proposal sorted near the top of governance.html's Voting
// Ballots list (see sortBallots in website/js/governance.js) - a pure
// burn from the promoter's own balance, distinct from TxRCInfuse
// (api/rc_infuse.go), which mints into the governance pool instead.
func HandlePromoteProposal(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req proposalPromoteData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.RCAmount == 0 {
		http.Error(w, "rcAmount must be positive", http.StatusBadRequest)
		return
	}
	p, ok := proposalStore.Get(req.ProposalID)
	if !ok || !p.Open {
		http.Error(w, "proposal not found or no longer open", http.StatusBadRequest)
		return
	}
	if reptStore.BalanceOf(address) < req.RCAmount {
		http.Error(w, fmt.Sprintf("insufficient RC balance - you have %d", reptStore.BalanceOf(address)), http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxProposalPromote, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

// applyProposalPromote re-checks everything the handler already
// checked (never trust submission-time state), burns the RC, and
// extends (never additively stacks) the proposal's PromotedUntil.
func applyProposalPromote(tx core.Transaction) {
	var data proposalPromoteData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.RCAmount == 0 {
		return
	}
	p, ok := proposalStore.Get(data.ProposalID)
	if !ok || !p.Open {
		return
	}
	if !reptStore.Burn(tx.From, data.RCAmount) {
		return
	}

	until := tx.Timestamp + promotionDurationForRCAmount(data.RCAmount)
	if until > p.PromotedUntil {
		p.PromotedUntil = until
	}
}
