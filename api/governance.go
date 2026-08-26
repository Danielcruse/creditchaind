package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// TokenCreationProposalFeeCCT is the submission-time cost of a
// TOKEN_CREATION proposal - a judgment call, deliberately "substantially
// higher" than the old self-service TokenCreationFeeETHTest=5, per the
// user's own "must charge high fee and pass community vote" instruction.
// Charged unconditionally at apply time (applyGovernance's "create"
// case), never refunded on FAILED - a submission cost for using up a
// governance slot, the mirror image of ProposalPassedProposerReward
// which only pays out on PASSED.
const TokenCreationProposalFeeCCT = 500

// TokenCreationMinRC is the RC (REPT) "release privilege" gate on
// TOKEN_CREATION proposal submission - a second gate alongside the CCT
// fee, per the RC governance economy's "release privileges" mechanic
// (see api/rc_promote.go's sibling mechanics). A judgment call, e.g.
// roughly Silver-tier-reward scale.
const TokenCreationMinRC = 100

// NodeAdmissionProposalQuorum is a doubled bar over
// core.DefaultProposalQuorum, specifically for ProposalKindNodeAdmission -
// admitting a new node to the peer gossip network is materially
// higher-stakes than an ordinary param change or civic prompt (it's
// the actual Sybil-resistance mechanism, see core.NodeAdmissionStore's
// doc comment), so it clears a higher bar. No existing precedent for a
// kind-specific quorum elsewhere in this codebase - a new judgment
// call, not copied from anywhere.
const NodeAdmissionProposalQuorum = core.DefaultProposalQuorum * 2

type CreateProposalRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`

	// Kind/ParamKey/ParamValue/TargetVersion/Token* let a proposal
	// request an automatic protocol-parameter change, a version-
	// activation flag flip, or a new token's registration once it
	// passes - see core.ProposalKind's doc comment. Creation stays open
	// to any activated wallet regardless of kind; the real gate is the
	// vote itself (real quorum + majority), not who's allowed to
	// propose - see the governance plan's §4/§5.
	Kind          string `json:"kind,omitempty"`
	ParamKey      string `json:"paramKey,omitempty"`
	ParamValue    uint64 `json:"paramValue,omitempty"`
	TargetVersion string `json:"targetVersion,omitempty"`

	// TokenSymbol/TokenName/TokenInitialSupply/TokenStandard/
	// TokenPegReference are only read/validated when Kind ==
	// TOKEN_CREATION - same fields HandleRegisterToken's now-retired
	// direct path used to take.
	TokenSymbol        string `json:"tokenSymbol,omitempty"`
	TokenName          string `json:"tokenName,omitempty"`
	TokenInitialSupply uint64 `json:"tokenInitialSupply,omitempty"`
	TokenStandard      string `json:"tokenStandard,omitempty"`
	TokenPegReference  string `json:"tokenPegReference,omitempty"`

	// PredictionTargetSymbol/PredictionTargetPrice/
	// PredictionDurationSeconds are only read when Kind == PREDICTION -
	// see api/prediction_vote.go.
	PredictionTargetSymbol    string `json:"predictionTargetSymbol,omitempty"`
	PredictionTargetPrice     uint64 `json:"predictionTargetPrice,omitempty"`
	PredictionDurationSeconds uint64 `json:"predictionDurationSeconds,omitempty"`

	// DisputeModeratorAddress/DisputeReferenceType/DisputeReferenceID
	// are only read when Kind == MODERATOR_DISPUTE - see
	// core.ProposalKindModeratorDispute's doc comment and
	// api/moderator_dispute_governance.go.
	DisputeModeratorAddress string `json:"disputeModeratorAddress,omitempty"`
	DisputeReferenceType    string `json:"disputeReferenceType,omitempty"`
	DisputeReferenceID      string `json:"disputeReferenceId,omitempty"`
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

	switch req.Kind {
	case "", string(core.ProposalKindParamChange), string(core.ProposalKindVersionActivation), string(core.ProposalKindTokenCreation), string(core.ProposalKindPrediction), string(core.ProposalKindModeratorDispute):
		// valid
	default:
		http.Error(w, "kind must be empty, PARAM_CHANGE, VERSION_ACTIVATION, TOKEN_CREATION, PREDICTION, or MODERATOR_DISPUTE", http.StatusBadRequest)
		return
	}
	if req.Kind == string(core.ProposalKindParamChange) && req.ParamKey == "" {
		http.Error(w, "paramKey required for a PARAM_CHANGE proposal", http.StatusBadRequest)
		return
	}
	if req.Kind == string(core.ProposalKindVersionActivation) && req.TargetVersion == "" {
		http.Error(w, "targetVersion required for a VERSION_ACTIVATION proposal", http.StatusBadRequest)
		return
	}
	if req.Kind == string(core.ProposalKindTokenCreation) {
		// Same validation HandleRegisterToken's now-retired direct path
		// used to run - re-run here (and again, authoritatively, at
		// apply time) rather than trusting this submission-time check
		// alone, same "never trust submission-time state" discipline
		// every other apply* function already follows.
		if !core.ValidateTokenSymbol(req.TokenSymbol) {
			http.Error(w, "tokenSymbol must be 2-10 uppercase letters and not already reserved (CCT/ETHTEST/USDCTEST/REPT)", http.StatusBadRequest)
			return
		}
		if !core.ValidateTokenName(req.TokenName) {
			http.Error(w, "tokenName must be 1-64 characters", http.StatusBadRequest)
			return
		}
		if req.TokenInitialSupply == 0 || req.TokenInitialSupply > core.MaxCustomTokenSupply {
			http.Error(w, "tokenInitialSupply must be positive and at most 1,000,000,000", http.StatusBadRequest)
			return
		}
		if _, exists := tokenRegistry.Meta[req.TokenSymbol]; exists {
			http.Error(w, "tokenSymbol already registered", http.StatusBadRequest)
			return
		}
		if req.TokenStandard == "" {
			req.TokenStandard = "C1"
		}
		if !tokenStandardRegistry.IsCreatable(req.TokenStandard) {
			http.Error(w, "tokenStandard must be a real, user-creatable token standard (see GET /token-standards)", http.StatusBadRequest)
			return
		}
		cctBalance, _ := core.ParseCctSmallest(cctStore.BalanceOf(address))
		feeSmallest := core.CctWholeToSmallest(TokenCreationProposalFeeCCT)
		if cctBalance.Cmp(feeSmallest) < 0 {
			http.Error(w, fmt.Sprintf("you need at least %d CCT to cover the token creation proposal fee", TokenCreationProposalFeeCCT), http.StatusBadRequest)
			return
		}
		if reptStore.BalanceOf(address) < TokenCreationMinRC {
			http.Error(w, fmt.Sprintf("you need at least %d RC (REPT) to unlock token creation - a release privilege on top of the CCT fee", TokenCreationMinRC), http.StatusBadRequest)
			return
		}
	}
	if req.Kind == string(core.ProposalKindPrediction) {
		if !isValidPredictionTargetSymbol(req.PredictionTargetSymbol) {
			http.Error(w, "predictionTargetSymbol must be BTCTEST or ETHTEST", http.StatusBadRequest)
			return
		}
		if req.PredictionTargetPrice == 0 {
			http.Error(w, "predictionTargetPrice must be positive", http.StatusBadRequest)
			return
		}
		if req.PredictionDurationSeconds != 0 && (req.PredictionDurationSeconds < PredictionMinDurationSeconds || req.PredictionDurationSeconds > PredictionMaxDurationSeconds) {
			http.Error(w, "predictionDurationSeconds must be between 1 and 60 days (or omitted for the default 7-day window)", http.StatusBadRequest)
			return
		}
	}
	if req.Kind == string(core.ProposalKindModeratorDispute) {
		if !moderatorStore.IsModerator(req.DisputeModeratorAddress) {
			http.Error(w, "disputeModeratorAddress must be a currently-registered moderator", http.StatusBadRequest)
			return
		}
		if !isValidModeratorDisputeReferenceType(req.DisputeReferenceType) {
			http.Error(w, "disputeReferenceType must be negotiation, offer, or company", http.StatusBadRequest)
			return
		}
		if !moderatorDisputeReferenceValid(req.DisputeReferenceType, req.DisputeReferenceID, req.DisputeModeratorAddress) {
			http.Error(w, "disputeReferenceId must name a real record that disputeModeratorAddress was actually assigned to", http.StatusBadRequest)
			return
		}
	}

	data, _ := json.Marshal(governanceData{
		Action:                    "create",
		Title:                     req.Title,
		Description:               req.Description,
		Kind:                      req.Kind,
		ParamKey:                  req.ParamKey,
		ParamValue:                req.ParamValue,
		TargetVersion:             req.TargetVersion,
		TokenSymbol:               req.TokenSymbol,
		TokenName:                 req.TokenName,
		TokenInitialSupply:        req.TokenInitialSupply,
		TokenStandard:             req.TokenStandard,
		TokenPegReference:         req.TokenPegReference,
		PredictionTargetSymbol:    req.PredictionTargetSymbol,
		PredictionTargetPrice:     req.PredictionTargetPrice,
		PredictionDurationSeconds: req.PredictionDurationSeconds,
		DisputeModeratorAddress:   req.DisputeModeratorAddress,
		DisputeReferenceType:      req.DisputeReferenceType,
		DisputeReferenceID:        req.DisputeReferenceID,
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

	// Voting now requires the same one-time governance opt-in as
	// posting a status update (HandlePostGovernanceStatus already had
	// this check; voting itself never did - see the governance plan's
	// §1 for why that was a real inconsistency worth closing here).
	if walletStore.Get(address).VotingPower == 0 {
		http.Error(w, "activate governance/voting rights before voting", http.StatusForbidden)
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

// HandleGovernanceMyWeight is a small public read-only wrapper around
// votingWeight (api/governance_weight.go) - lets the frontend show a
// wallet its real, current vote weight before it votes, so "your vote
// is weighted by stake/reputation" isn't just copy nobody can verify.
func HandleGovernanceMyWeight(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "address required", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(map[string]uint64{
		"weight": votingWeight(address),
	})
}

// HandleRecentGovernanceStatuses is a public read (no wallet needed) -
// this is what the public landing page's live news corner polls, so
// it deliberately has no auth requirement, same as every other public
// GET in this app.
func HandleRecentGovernanceStatuses(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	json.NewEncoder(w).Encode(governanceStatusStore.Recent(20))
}
