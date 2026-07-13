package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

var proposalStore = core.NewProposalStore()
var governanceStatusStore = core.NewGovernanceStatusStore()

const (
	maxProposalTitleLen = 200
	maxProposalDescLen  = 2000
)

type CreateProposalRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type VoteRequest struct {
	ProposalID string `json:"proposalId"`
	Support    bool   `json:"support"`
}

// HandleGovernance lists every proposal from the shared ProposalStore.
func HandleGovernance(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	json.NewEncoder(w).Encode(
		proposalStore.List(),
	)
}

func HandleCreateProposal(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req CreateProposalRequest

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Title == "" || len(req.Title) > maxProposalTitleLen {
		http.Error(w, "title required and must be under 200 characters", http.StatusBadRequest)
		return
	}

	if len(req.Description) > maxProposalDescLen {
		http.Error(w, "description must be under 2000 characters", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(governanceData{
		Action:      "create",
		Title:       req.Title,
		Description: req.Description,
	})

	tx := recordTx(auth, address, "", 0, core.TxGovernance, string(data))

	json.NewEncoder(w).Encode(
		map[string]string{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}

func HandleVoteProposal(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req VoteRequest

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if _, ok := proposalStore.Get(req.ProposalID); !ok {
		http.Error(w, "proposal not found", http.StatusBadRequest)
		return
	}

	if proposalStore.HasVoted(req.ProposalID, address) {
		http.Error(w, "address already voted on this proposal", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(governanceData{
		Action:     "vote",
		ProposalID: req.ProposalID,
		Support:    req.Support,
	})

	tx := recordTx(auth, address, "", 0, core.TxGovernance, string(data))

	json.NewEncoder(w).Encode(
		map[string]string{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}

// HandleActivateGovernance is a wallet opting in to governance/voting
// rights - see applyGovernance's "activate" case for what this
// actually flips (Wallet.VotingPower 0 -> 1). Once activated, the
// same wallet can post status updates via HandlePostGovernanceStatus.
func HandleActivateGovernance(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req struct{}

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	data, _ := json.Marshal(governanceData{Action: "activate"})
	tx := recordTx(auth, address, "", 0, core.TxGovernance, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

func newGovernanceStatusID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const maxGovernanceStatusLen = 500

type postGovernanceStatusData struct {
	Text string `json:"text"`
}

// HandlePostGovernanceStatus lets an activated wallet (VotingPower >
// 0) post a free-text update - takes effect immediately (not queued
// as a block-confirmed transaction), same reasoning as negotiation
// chat messages: this moves no value and doesn't need consensus-level
// guarantees, only proof it really came from an activated wallet.
func HandlePostGovernanceStatus(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req postGovernanceStatusData

	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if len(req.Text) == 0 || len(req.Text) > maxGovernanceStatusLen {
		http.Error(w, "status must be 1-500 characters", http.StatusBadRequest)
		return
	}

	if walletStore.Get(address).VotingPower == 0 {
		http.Error(w, "activate governance/voting rights before posting a status update", http.StatusForbidden)
		return
	}

	governanceStatusStore.Add(&core.GovernanceStatus{
		ID:        newGovernanceStatusID(),
		Address:   address,
		Text:      req.Text,
		Timestamp: time.Now().Unix(),
	})

	json.NewEncoder(w).Encode(map[string]string{"status": "posted"})
}

// HandleRecentGovernanceStatuses is a public read (no wallet needed) -
// this is what the public landing page's live news corner polls, so
// it deliberately has no auth requirement, same as every other public
// GET in this app.
func HandleRecentGovernanceStatuses(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	json.NewEncoder(w).Encode(governanceStatusStore.Recent(20))
}
