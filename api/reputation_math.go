package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// ReputationMechanism describes one way a wallet's reputation can
// move - everything here is read live off the same constants the
// apply layer (api/apply.go, core/credit_engine.go, core/moderator.go)
// actually uses to move reputation, so this can never quietly drift
// out of sync with what really happens on chain.
type ReputationMechanism struct {
	Name        string `json:"name"`
	Delta       int64  `json:"delta"`
	Target      string `json:"target"`
	Description string `json:"description"`
	// Wired is false for mechanisms that are defined in code (a real
	// constant exists) but have no trigger path reachable from any
	// current endpoint yet - shown on the explainer page as a
	// disclosed gap rather than silently omitted.
	Wired bool `json:"wired"`
}

// ReputationMathResponse is the single source the reputation-math
// explainer page renders from.
type ReputationMathResponse struct {
	Floor       uint64              `json:"floor"`
	Ceiling     uint64              `json:"ceiling"`
	Tiers       []core.TierThreshold `json:"tiers"`
	TierRewards []core.TierReward   `json:"tierRewards"`
	Mechanisms  []ReputationMechanism `json:"mechanisms"`
}

func HandleReputationMath(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	resp := ReputationMathResponse{
		Floor:       core.MinReputation,
		Ceiling:     core.MaxReputation,
		Tiers:       core.TierThresholds(),
		TierRewards: core.TierRewards(),
		Mechanisms: []ReputationMechanism{
			{
				Name:        "P2P trade completed",
				Delta:       int64(ReputationDeltaP2PTrade),
				Target:      "both the buyer and the seller",
				Description: "Awarded to both parties the instant a P2P offer is accepted and settles - the only mechanism that pays two different wallets from a single transaction.",
				Wired:       true,
			},
			{
				Name:        "Fund a peer loan offer (as lender)",
				Delta:       int64(core.ReputationDeltaLoanFunded),
				Target:      "the lender",
				Description: "Earned when a borrower draws down your posted loan offer, or when you directly fund someone else's loan request.",
				Wired:       true,
			},
			{
				Name:        "Repay a peer-funded loan on time",
				Delta:       int64(core.ReputationDeltaLoanRepaidOnTime),
				Target:      "the borrower",
				Description: "Only paid for peer-funded loans (a real wallet on the other side) repaid through the p2p flow. Self-funded/protocol loans mint instantly with no lender, so repaying one earns no reputation - no one extended real trust to reward.",
				Wired:       true,
			},
			{
				Name:        "Repay a loan late",
				Delta:       int64(core.ReputationDeltaLoanRepaidLate),
				Target:      "the borrower",
				Description: "Defined in code as a smaller reward than an on-time repayment, but the protocol has no due-date/lateness-detection mechanism yet, so this path can never actually trigger.",
				Wired:       false,
			},
			{
				Name:        "Loan default",
				Delta:       -int64(core.ReputationDeltaLoanDefault),
				Target:      "the borrower",
				Description: "Defined in code as a reputation penalty for defaulting, but nothing currently detects or reports a default, so this path can never actually trigger.",
				Wired:       false,
			},
			{
				Name:        "Register as a moderator",
				Delta:       int64(core.ModeratorReputationBonus),
				Target:      "the moderator",
				Description: "A one-time bonus paid once, at registration.",
				Wired:       true,
			},
			{
				Name:        "Confirm an undisputed trade (as moderator)",
				Delta:       int64(core.ModeratorConfirmReputationBonus),
				Target:      "the moderator",
				Description: "Paid per trade confirmed WITHOUT a dispute - deliberately not paid on disputed trades, so moderators are never incentivized to manufacture one.",
				Wired:       true,
			},
			{
				Name:        "Produce blocks as a validator",
				Delta:       int64(blockProductionReputationGrowth),
				Target:      "the validator",
				Description: fmt.Sprintf("Awarded every %d blocks a validator successfully produces - a slow, steady trickle that rewards sustained uptime rather than any single action.", blockProductionReputationInterval),
				Wired:       true,
			},
		},
	}

	json.NewEncoder(w).Encode(resp)
}
