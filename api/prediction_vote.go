package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/creditchain/creditchaind/core"
)

var predictionPoolStore = core.NewPredictionPoolStore()

// PredictionVoteFeeCCT is the flat CCT fee charged on every RC-backed
// prediction vote - this is the validator-compensation piece of the
// prediction-market feature (see the plan's own framing: RC has no
// defined CCT exchange rate, so validators are paid in CCT, not RC,
// via the exact same fee-sweep-to-block-producer mechanism
// CCTTransferFee already uses). An ordinary Yes/No vote with no RC
// attached stays free, exactly as costly as any other governance vote
// - only the RC-bet path adds new validator load, so only that path
// pays for it.
const PredictionVoteFeeCCT = 1

// PredictionLongPoolBonusThresholdSeconds/PredictionLongPoolBonusMaxBps
// are the "2-week patience bonus" - judgment calls, not derived from
// any spec. A prediction whose pool stays active at least this long
// before resolving mints an extra bonus (capped at this bps share of
// the pool's total infused RC), split pro-rata across every infuser on
// BOTH sides - framed as compensation for capital that sat committed
// an unusually long time, not a win/loss reward, so it doesn't
// discriminate by outcome.
const (
	PredictionLongPoolBonusThresholdSeconds = 14 * 24 * 3600
	PredictionLongPoolBonusMaxBps           = 1000 // 10%
)

// PredictionMinDurationSeconds/PredictionMaxDurationSeconds/
// PredictionDefaultDurationSeconds bound how long a PREDICTION's
// voting window (and therefore its RC-bet pool) can run - unlike
// every other proposal kind, which is always exactly
// core.ProposalVotingWindowSeconds (7 days). A configurable, longer
// window is what makes the "pool active 14+ days" bonus reachable at
// all (see PredictionLongPoolBonusThresholdSeconds) - a fixed 7-day
// deadline could never satisfy a 14-day threshold. Judgment calls, not
// derived from any spec.
const (
	PredictionMinDurationSeconds     = 24 * 3600
	PredictionMaxDurationSeconds     = 60 * 24 * 3600
	PredictionDefaultDurationSeconds = core.ProposalVotingWindowSeconds
)

// predictionDurationSeconds clamps a requested duration into the
// valid range, defaulting to the ordinary 7-day governance window if
// unspecified (0) - so a prediction creator who doesn't care can just
// omit it and get today's familiar behavior.
func predictionDurationSeconds(requested uint64) int64 {
	if requested == 0 {
		return PredictionDefaultDurationSeconds
	}
	if requested < PredictionMinDurationSeconds {
		return PredictionMinDurationSeconds
	}
	if requested > PredictionMaxDurationSeconds {
		return PredictionMaxDurationSeconds
	}
	return int64(requested)
}

// isValidPredictionTargetSymbol restricts a PREDICTION proposal's
// target to the two developer-accessible CCT-paired price-reference
// pools (see api/token.go's btcTestStore/cctBtcTestPool and
// ethTestStore/cctEthTestPool) - never an arbitrary symbol, since
// resolution needs a real, live pool to read a price from.
func isValidPredictionTargetSymbol(symbol string) bool {
	return symbol == "BTCTEST" || symbol == "ETHTEST"
}

// predictionTargetPool resolves a validated target symbol to its
// live pool - the two are wired 1:1, see isValidPredictionTargetSymbol.
func predictionTargetPool(symbol string) *core.LiquidityPool {
	switch symbol {
	case "BTCTEST":
		return cctBtcTestPool
	case "ETHTEST":
		return cctEthTestPool
	}
	return nil
}

type predictionVoteData struct {
	ProposalID string `json:"proposalId"`
	Side       string `json:"side"`     // "YES" | "NO"
	RCAmount   uint64 `json:"rcAmount"` // 0 is valid - an ordinary vote with no RC attached
}

// HandlePredictionVote is a PREDICTION proposal's combined
// vote-plus-optional-RC-stake action - one call registers the
// ordinary Yes/No headcount vote every proposal has, and (if
// RCAmount > 0) also backs that side with a real RC stake, forming or
// adding to the proposal's visible PredictionPool.
func HandlePredictionVote(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req predictionVoteData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	p, ok := proposalStore.Get(req.ProposalID)
	if !ok || !p.Open || p.Kind != core.ProposalKindPrediction {
		http.Error(w, "prediction not found or no longer open", http.StatusBadRequest)
		return
	}
	if req.Side != string(core.PredictionSideYes) && req.Side != string(core.PredictionSideNo) {
		http.Error(w, "side must be YES or NO", http.StatusBadRequest)
		return
	}
	if proposalStore.HasVoted(req.ProposalID, address) {
		http.Error(w, "address already voted on this prediction", http.StatusBadRequest)
		return
	}
	if walletStore.Get(address).VotingPower == 0 {
		http.Error(w, "activate governance/voting rights before voting", http.StatusForbidden)
		return
	}
	if req.RCAmount > 0 {
		if reptStore.BalanceOf(address) < req.RCAmount {
			http.Error(w, "insufficient RC balance to back this prediction", http.StatusBadRequest)
			return
		}
		if cctAdapter.BalanceOf(address) < PredictionVoteFeeCCT {
			http.Error(w, "insufficient CCT to cover the prediction-vote fee", http.StatusBadRequest)
			return
		}
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxPredictionVote, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

// applyPredictionVote re-checks everything the handler already
// checked (never trusts submission-time state), registers the
// ordinary headcount vote via the same helper applyGovernance's
// "vote" case uses, and - only if RCAmount > 0 - burns the fee and RC
// stake, lazily creating/growing the proposal's PredictionPool.
func applyPredictionVote(tx core.Transaction) {
	var data predictionVoteData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.Side != string(core.PredictionSideYes) && data.Side != string(core.PredictionSideNo) {
		return
	}

	p, ok := proposalStore.Get(data.ProposalID)
	if !ok || !p.Open || p.Kind != core.ProposalKindPrediction {
		return
	}
	if proposalStore.HasVoted(data.ProposalID, tx.From) {
		return
	}

	support := data.Side == string(core.PredictionSideYes)
	if !applyProposalVoteHeadcount(data.ProposalID, tx.From, support, tx.Timestamp) {
		return
	}

	if data.RCAmount == 0 {
		return
	}
	if reptStore.BalanceOf(tx.From) < data.RCAmount {
		return
	}
	if !cctAdapter.Transfer(tx.From, cctTransferFeePoolAddress(), PredictionVoteFeeCCT) {
		return
	}
	if !reptStore.Burn(tx.From, data.RCAmount) {
		return
	}

	predictionPoolStore.Infuse(&core.PredictionInfusion{
		ProposalID: data.ProposalID,
		Address:    tx.From,
		Side:       core.PredictionSide(data.Side),
		RCAmount:   data.RCAmount,
		CreatedAt:  tx.Timestamp,
	})
}

// processPredictionResolution runs once, from finalizeProposal, the
// instant a PREDICTION proposal's deadline passes - independent of
// its ordinary Yes/No headcount quorum outcome (see finalizeProposal's
// doc comment for why). A prediction with no RC infusions has no
// PredictionPool at all and this is a no-op beyond that lookup.
func processPredictionResolution(p *core.Proposal, asOf int64) {
	pool := predictionPoolStore.Get(p.ID)
	if pool == nil || pool.Resolved {
		return
	}

	targetPool := predictionTargetPool(p.PredictionTargetSymbol)
	currentPrice := core.CurrentPoolPriceScaled(targetPool)
	met := currentPrice >= p.PredictionTargetPrice

	winningSide := core.PredictionSideNo
	winningPoolRC, losingPoolRC := pool.NoPoolRC, pool.YesPoolRC
	if met {
		winningSide = core.PredictionSideYes
		winningPoolRC, losingPoolRC = pool.YesPoolRC, pool.NoPoolRC
	}
	pool.WinningSide = winningSide

	// Pro-rata payout: each winning infuser gets their own stake back
	// plus their share of the losing pool, proportional to their share
	// of the winning side's total. A winning side with zero infusers
	// (nobody backed the side that turned out right) is a real,
	// disclosed edge case - the losing pool simply isn't distributed
	// (stays burned) rather than inventing a fallback recipient.
	if winningPoolRC > 0 {
		var winners []*core.PredictionInfusion
		for _, inf := range pool.Infusions {
			if inf.Side == winningSide {
				winners = append(winners, inf)
			}
		}
		sort.Slice(winners, func(i, j int) bool { return winners[i].Address < winners[j].Address })
		for _, inf := range winners {
			payout := inf.RCAmount * losingPoolRC / winningPoolRC
			mintRept(inf.Address, inf.RCAmount+payout)
		}
	}

	// The 14-day patience bonus - see the constants' doc comment.
	// core.Proposal has no CreatedAt field of its own; recovered from
	// Deadline - PredictionDurationSeconds (the actual duration this
	// specific prediction was created with - see predictionDurationSeconds
	// and core.Proposal.PredictionDurationSeconds's doc comment).
	createdAt := p.Deadline - p.PredictionDurationSeconds
	if asOf-createdAt >= PredictionLongPoolBonusThresholdSeconds {
		totalPoolRC := pool.YesPoolRC + pool.NoPoolRC
		bonus := totalPoolRC * PredictionLongPoolBonusMaxBps / 10000
		if bonus > 0 && totalPoolRC > 0 {
			sorted := make([]*core.PredictionInfusion, len(pool.Infusions))
			copy(sorted, pool.Infusions)
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].Address < sorted[j].Address })
			for _, inf := range sorted {
				share := bonus * inf.RCAmount / totalPoolRC
				if share > 0 {
					mintRept(inf.Address, share)
				}
			}
			pool.BonusMinted = bonus
		}
	}

	pool.Resolved = true
}

// HandleGetPredictionPool is a public read of a prediction's live RC
// bet pool - "the pool should be visible," per the feature's own
// requirement. Returns a zero-value shape (not 404) for a PREDICTION
// proposal with no infusions yet, so the frontend can render "no bets
// placed yet" rather than treating it as an error.
func HandleGetPredictionPool(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	proposalID := r.URL.Query().Get("proposalId")
	pool := predictionPoolStore.Get(proposalID)
	if pool == nil {
		json.NewEncoder(w).Encode(&core.PredictionPool{ProposalID: proposalID})
		return
	}
	json.NewEncoder(w).Encode(pool)
}
