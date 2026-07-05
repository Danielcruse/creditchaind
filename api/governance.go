package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var proposalStore = core.NewProposalStore()

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
