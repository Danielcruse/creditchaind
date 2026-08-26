package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/creditchain/creditchaind/core"
)

var executiveProposalStore = core.NewExecutiveProposalStore()

// executiveProposalVotingWindowSeconds is shorter than the ordinary
// 7-day governance window - a judgment call, reasonable given this is
// already a small, reputation-pre-selected committee rather than the
// general electorate.
const executiveProposalVotingWindowSeconds = 3 * 24 * 3600

// realWorldTrustWeightParamKey, moderatorDeactivateParamKey, and the
// two moderator-fee/stake keys below are the ONLY keys an executive
// proposal is ever allowed to target - core.ExecutiveProposal is a
// generic ParamKey/ParamValue(/TargetAddress) tuple so a future
// executive-only action doesn't need a new type, but the allowlist
// itself is fixed server-side here, not client-open-ended.
// ModeratorRegisterFeeCCT/ModeratorStakeRequirementCCT (see
// api/moderators.go) fall through to the generic
// governanceParamsStore.Set(p.ParamKey, p.ParamValue) branch in
// processExecutiveProposalFinalization - same as RealWorldTrustWeightBps,
// no special-casing needed since both are plain uint64 params (unlike
// ModeratorDeactivate, which targets an address instead of a value).
const realWorldTrustWeightParamKey = "RealWorldTrustWeightBps"
const moderatorDeactivateParamKey = "ModeratorDeactivate"

// moderatorFeeParamKey/moderatorStakeParamKey are declared in
// api/moderators.go - reused here verbatim, not redeclared, so both
// writers (admin + executive) and both readers
// (moderatorRegisterFeeCCT/moderatorStakeRequirementCCT) always agree
// on the exact same GovernanceParamsStore key string.
func isAllowedExecutiveProposalParamKey(key string) bool {
	switch key {
	case realWorldTrustWeightParamKey, moderatorDeactivateParamKey, moderatorFeeParamKey, moderatorStakeParamKey:
		return true
	default:
		return false
	}
}

// normalizeExecutiveProposalParamKey defaults an empty ParamKey to
// realWorldTrustWeightParamKey - preserves the exact pre-existing
// behavior of every already-shipped caller (the frontend's
// createExecutiveProposal() only ever sent {paramValue}, with no
// paramKey field at all) now that this endpoint's payload also
// supports the moderator-deactivation action.
func normalizeExecutiveProposalParamKey(key string) string {
	if key == "" {
		return realWorldTrustWeightParamKey
	}
	return key
}

type executiveProposalCreateData struct {
	ParamKey      string `json:"paramKey"`
	ParamValue    uint64 `json:"paramValue"`
	TargetAddress string `json:"targetAddress"`
}

// HandleCreateExecutiveProposal lets a current executive propose either
// a new RealWorldTrustWeightBps value or a moderator's deactivation
// (the executive penalty-vote mechanism) - gated to isExecutive both
// here (fast-fail) and again at apply time (authoritative), since the
// top-N-reputation committee can shift between submission and
// confirmation.
func HandleCreateExecutiveProposal(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req executiveProposalCreateData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if !isExecutive(address) {
		http.Error(w, "only a current top-reputation executive may create an executive proposal", http.StatusForbidden)
		return
	}
	req.ParamKey = normalizeExecutiveProposalParamKey(req.ParamKey)
	if !isAllowedExecutiveProposalParamKey(req.ParamKey) {
		http.Error(w, "unrecognized executive proposal paramKey", http.StatusBadRequest)
		return
	}
	if req.ParamKey == moderatorDeactivateParamKey {
		m, ok := moderatorStore.Get(req.TargetAddress)
		if !ok || m.Deactivated {
			http.Error(w, "targetAddress is not a currently-active moderator", http.StatusBadRequest)
			return
		}
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxExecutiveProposalCreate, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

func applyExecutiveProposalCreate(tx core.Transaction) {
	if !isExecutive(tx.From) {
		return
	}
	var data executiveProposalCreateData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	data.ParamKey = normalizeExecutiveProposalParamKey(data.ParamKey)
	if !isAllowedExecutiveProposalParamKey(data.ParamKey) {
		return
	}
	if data.ParamKey == moderatorDeactivateParamKey {
		m, ok := moderatorStore.Get(data.TargetAddress)
		if !ok || m.Deactivated {
			return
		}
	}

	id := "execprop-" + tx.ID[:min(8, len(tx.ID))]
	executiveProposalStore.Create(&core.ExecutiveProposal{
		ID:            id,
		CreatedBy:     tx.From,
		ParamKey:      data.ParamKey,
		ParamValue:    data.ParamValue,
		TargetAddress: data.TargetAddress,
		Open:          true,
		Deadline:      tx.Timestamp + executiveProposalVotingWindowSeconds,
		CreatedAt:     tx.Timestamp,
	})
}

type executiveProposalVoteData struct {
	ProposalID string `json:"proposalId"`
	Support    bool   `json:"support"`
}

// HandleVoteExecutiveProposal is the executive-only vote - same
// isExecutive gate as creation, at both submission and apply time.
func HandleVoteExecutiveProposal(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req executiveProposalVoteData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if !isExecutive(address) {
		http.Error(w, "only a current top-reputation executive may vote here", http.StatusForbidden)
		return
	}
	p, ok := executiveProposalStore.Get(req.ProposalID)
	if !ok || !p.Open {
		http.Error(w, "executive proposal not found or no longer open", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxExecutiveProposalVote, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

// applyExecutiveProposalVote re-checks isExecutive at apply time even
// though the handler already checked it - critical, since the
// top-reputation set can genuinely shift between a request's
// submission and its confirmation a block or more later.
func applyExecutiveProposalVote(tx core.Transaction) {
	if !isExecutive(tx.From) {
		return
	}
	var data executiveProposalVoteData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	executiveProposalStore.Vote(data.ProposalID, tx.From, data.Support)
}

// processExecutiveProposalFinalization closes every open executive
// proposal whose deadline has passed - simple headcount majority
// (Yes>No), no quorum beyond that, since the committee itself is
// already the selection mechanism (see core.ExecutiveProposal's doc
// comment). Same block-timestamp-driven determinism as
// processProposalFinalization.
func processExecutiveProposalFinalization(asOf int64) {
	var ids []string
	for id, p := range executiveProposalStore.Proposals {
		if p.Open && asOf >= p.Deadline {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	for _, id := range ids {
		p := executiveProposalStore.Proposals[id]
		p.Open = false
		if p.YesVotes > p.NoVotes {
			p.Status = core.ProposalStatusPassed
			if p.ParamKey == moderatorDeactivateParamKey {
				moderatorStore.Deactivate(p.TargetAddress)
			} else {
				governanceParamsStore.SetAt(p.ParamKey, p.ParamValue, asOf)
			}
		} else {
			p.Status = core.ProposalStatusFailed
		}
	}
}

// HandleListExecutiveProposals is a public read of every executive
// proposal (open and closed) - same "status carries no value to
// protect" reasoning used throughout this package. The executive-only
// gate is on voting/creating, not reading.
func HandleListExecutiveProposals(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	json.NewEncoder(w).Encode(executiveProposalStore.List())
}
