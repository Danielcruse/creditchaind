package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// ReputationMechanism describes one way a wallet's trust can move -
// everything here is read live off the same constants the apply layer
// (api/apply.go, core/credit_engine.go, core/slashing.go,
// api/moderator_dispute_governance.go) actually uses, so this can never
// quietly drift out of sync with what really happens on chain.
// Dimension is the TrustDimensions field (core/trust.go) this mechanism
// moves once a wallet is past TrustDimensionsCutoverUnixSeconds - before
// that, every mechanism instead moved the single flat Reputation number
// directly, and Wallet.Reputation today is a live-synced mirror of the
// weighted blend across all six (core.ComputeTSI), not a separate score.
type ReputationMechanism struct {
	Name        string `json:"name"`
	Delta       int64  `json:"delta"`
	Target      string `json:"target"`
	Dimension   string `json:"dimension"`
	Description string `json:"description"`
	// Wired is false for mechanisms that are defined in code (a real
	// constant exists) but have no trigger path reachable from any
	// current endpoint yet - shown on the explainer page as a
	// disclosed gap rather than silently omitted.
	Wired bool `json:"wired"`
}

// TrustWeight is one dimension's current governance-adjustable share of
// the overall TSI blend (core.ComputeTSI) - read live off
// core.DefaultTrustWeights, which an admin TSIWeight*Bps param-set or a
// passed community PARAM_CHANGE proposal both mutate directly (see
// syncTrustWeightIfNeeded, api/governance_params.go), so a governance
// change to the split shows up here immediately, never hand-typed.
type TrustWeight struct {
	Dimension string `json:"dimension"`
	Bps       uint64 `json:"bps"`
}

// ReputationMathResponse is the single source the reputation-math
// explainer page renders from.
type ReputationMathResponse struct {
	Floor                             uint64                `json:"floor"`
	Ceiling                           uint64                `json:"ceiling"`
	Tiers                             []core.TierThreshold  `json:"tiers"`
	TierRewards                       []core.TierReward     `json:"tierRewards"`
	Dimensions                        []string              `json:"dimensions"`
	Weights                           []TrustWeight         `json:"weights"`
	TrustDimensionsCutoverUnixSeconds int64                 `json:"trustDimensionsCutoverUnixSeconds"`
	Mechanisms                        []ReputationMechanism `json:"mechanisms"`
}

func HandleReputationMath(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	weights := core.DefaultTrustWeights

	resp := ReputationMathResponse{
		Floor:       core.MinReputation,
		Ceiling:     core.MaxReputation,
		Tiers:       core.TierThresholds(),
		TierRewards: core.TierRewards(),
		Dimensions: []string{
			core.DimensionRepayment.String(),
			core.DimensionTrading.String(),
			core.DimensionGovernance.String(),
			core.DimensionContribution.String(),
			core.DimensionVerification.String(),
			core.DimensionDispute.String(),
		},
		Weights: []TrustWeight{
			{Dimension: core.DimensionRepayment.String(), Bps: weights.RepaymentBps},
			{Dimension: core.DimensionTrading.String(), Bps: weights.TradingBps},
			{Dimension: core.DimensionGovernance.String(), Bps: weights.GovernanceBps},
			{Dimension: core.DimensionContribution.String(), Bps: weights.ContributionBps},
			{Dimension: core.DimensionVerification.String(), Bps: weights.VerificationBps},
			{Dimension: core.DimensionDispute.String(), Bps: weights.DisputeBps},
		},
		TrustDimensionsCutoverUnixSeconds: TrustDimensionsCutoverUnixSeconds,
		Mechanisms: []ReputationMechanism{
			{
				Name:        "P2P trade completed",
				Delta:       int64(ReputationDeltaP2PTrade),
				Target:      "both the buyer and the seller",
				Dimension:   core.DimensionTrading.String(),
				Description: "Awarded to both parties the instant a P2P offer is accepted and settles - the only mechanism that pays two different wallets from a single transaction.",
				Wired:       true,
			},
			{
				Name:        "Positive trade review",
				Delta:       int64(TradeReviewReputationBonus),
				Target:      "the reviewed party",
				Dimension:   core.DimensionTrading.String(),
				Description: "Paid once per reviewer/reviewee pair (see TradeReviewStore's own cap) after a completed trade - kept small deliberately, on top of the larger completion bonus above.",
				Wired:       true,
			},
			{
				Name:        "Fund a peer loan offer (as lender)",
				Delta:       int64(core.ReputationDeltaLoanFunded),
				Target:      "the lender",
				Dimension:   core.DimensionContribution.String(),
				Description: "Earned when a borrower draws down your posted loan offer, or when you directly fund someone else's loan request. Routed to Contribution, not Repayment - a lender is providing liquidity, not demonstrating their own repayment reliability.",
				Wired:       true,
			},
			{
				Name:        "Repay a peer-funded loan on time",
				Delta:       int64(core.ReputationDeltaLoanRepaidOnTime),
				Target:      "the borrower",
				Dimension:   core.DimensionRepayment.String(),
				Description: "Only paid for peer-funded loans (a real wallet on the other side) repaid through the p2p flow. Self-funded/protocol loans mint instantly with no lender, so repaying one earns no reputation - no one extended real trust to reward.",
				Wired:       true,
			},
			{
				Name:        "Repay a loan late",
				Delta:       int64(core.ReputationDeltaLoanRepaidLate),
				Target:      "the borrower",
				Dimension:   core.DimensionRepayment.String(),
				Description: "Paid instead of the on-time reward when a peer-funded loan is repaid after its due date but before its collateral is actually liquidated - covers the Warning/Margin Call/Grace Period stages, where the loan is still open to repayment. A due date passing on its own is not a penalty; it only reduces the reward this repayment earns.",
				Wired:       true,
			},
			{
				Name:        "Loan default / liquidation shortfall",
				Delta:       -int64(core.ReputationDeltaLoanDefault),
				Target:      "the borrower",
				Dimension:   core.DimensionRepayment.String(),
				Description: fmt.Sprintf("Triggered by a peer-funded loan missing its due date, or (for OETH/STETH_CCC1-collateralized loans) collateral value falling below its required threshold (%d%% for OETH, %d%% for STETH_CCC1 - see core.LiquidationThresholdPercentFor) even before the due date. The penalty scales with how much of the debt liquidation failed to cover - 0 if collateral fully covered it (no penalty, loan marked repaid instead), up to this full amount if nothing was recovered. The lender receives the mirror-image gain.", core.LiquidationThresholdPercent, core.StethCcc1LiquidationThresholdPercent),
				Wired:       true,
			},
			{
				Name:        "Loan default / liquidation shortfall (lender side)",
				Delta:       int64(core.ReputationDeltaLoanDefaultLenderGain),
				Target:      "the lender",
				Dimension:   core.DimensionContribution.String(),
				Description: "The mirror-image gain to the borrower-side default penalty above - scales with the same shortfall, capped at this amount. A lender who was made fully whole (0 shortfall) gains nothing here either. Routed to Contribution, same reasoning as the loan-funded event above.",
				Wired:       true,
			},
			{
				Name:        "P2P dispute resolved against the seller",
				Delta:       -int64(ReputationDeltaP2PFraud),
				Target:      "the seller (offer creator)",
				Dimension:   core.DimensionDispute.String(),
				Description: "A moderator sided with the buyer, finding the seller's dispute claim false. Routed to Dispute, not Trading - dispute conduct is a distinct, large-magnitude signal deliberately kept separate so one fraud finding can't also tank routine marketplace standing.",
				Wired:       true,
			},
			{
				Name:        "Dispute fault - declined to pay",
				Delta:       -int64(ReputationDeltaP2PFraud),
				Target:      "the buyer found at fault",
				Dimension:   core.DimensionDispute.String(),
				Description: "A moderator sided with the seller, finding the buyer's payment claim false. The at-fault buyer gets a 72-hour window to pay the moderator's would-be fee directly instead (no penalty) or explicitly decline (this penalty, no fee). Explicitly declining resolves it immediately.",
				Wired:       true,
			},
			{
				Name:        "Dispute fault - no response in time",
				Delta:       -int64(ReputationDeltaP2PFraud),
				Target:      "the buyer found at fault",
				Dimension:   core.DimensionDispute.String(),
				Description: "The same dispute-fault penalty above, applied automatically if the at-fault buyer neither pays nor explicitly declines within the 72-hour window - both the forced fee and this penalty apply together in that case.",
				Wired:       true,
			},
			{
				Name:        "Register as a moderator",
				Delta:       int64(core.ModeratorReputationBonus),
				Target:      "the moderator",
				Dimension:   core.DimensionContribution.String(),
				Description: "A one-time bonus paid once, at registration.",
				Wired:       true,
			},
			{
				Name:        "Confirm an undisputed trade (as moderator)",
				Delta:       int64(core.ModeratorConfirmReputationBonus),
				Target:      "the moderator",
				Dimension:   core.DimensionContribution.String(),
				Description: "Paid per trade confirmed WITHOUT a dispute - deliberately not paid on disputed trades, so moderators are never incentivized to manufacture one.",
				Wired:       true,
			},
			{
				Name:        "Moderator abuse penalty (governance vote)",
				Delta:       -int64(ModeratorDisputeReputationPenalty),
				Target:      "the moderator",
				Dimension:   core.DimensionContribution.String(),
				Description: "Applied once the community votes to uphold a complaint that a moderator resolved a dispute in bad faith. Same magnitude class as the dispute-fault penalties above, but routed to Contribution (a moderator's own standing as a network participant), not Dispute (which tracks the conduct of trade parties, not moderators).",
				Wired:       true,
			},
			{
				Name:        "Produce blocks as a validator",
				Delta:       int64(blockProductionReputationGrowth),
				Target:      "the validator",
				Dimension:   core.DimensionContribution.String(),
				Description: fmt.Sprintf("Awarded every %d blocks a validator successfully produces - a slow, steady trickle that rewards sustained uptime rather than any single action.", blockProductionReputationInterval),
				Wired:       true,
			},
			{
				Name:        "Validator misses an assigned block",
				Delta:       -int64(core.ReputationPenaltyMissedBlock),
				Target:      "the validator",
				Dimension:   core.DimensionContribution.String(),
				Description: "Paired with a small stake slash and a strike toward a permanent ban - see core.MissedBlockStakeSlashBps and core.PermanentBanSlashCountThreshold. Validator uptime/behavior is routed to Contribution, the same dimension steady block production feeds.",
				Wired:       true,
			},
			{
				Name:        "Validator double-signs",
				Delta:       -int64(core.ReputationPenaltyDoubleSign),
				Target:      "the validator",
				Dimension:   core.DimensionContribution.String(),
				Description: "The most severe penalty in the protocol - attacks consensus itself, not just individual reliability. Paired with a much larger stake slash (core.DoubleSignStakeSlashBps) and a heavier strike count toward a permanent ban.",
				Wired:       true,
			},
			{
				Name:        "Author a proposal that passes",
				Delta:       int64(GovernanceTrustDeltaProposerPassed),
				Target:      "the proposer",
				Dimension:   core.DimensionGovernance.String(),
				Description: "Paid only once a proposal genuinely reaches quorum and passes, not merely for submitting one - deliberately reuses the existing REPT-reward finalization path rather than rewarding participation alone, which would be trivially farmable by proposing indiscriminately.",
				Wired:       true,
			},
			{
				Name:        "Vote on a proposal that passes",
				Delta:       int64(GovernanceTrustDeltaVoterPassed),
				Target:      "each voter who voted",
				Dimension:   core.DimensionGovernance.String(),
				Description: "Asymmetric with the proposer reward above (5 vs 1), mirroring the existing register-vs-confirm asymmetry between core.ModeratorReputationBonus and core.ModeratorConfirmReputationBonus. Only paid on a proposal that actually passes - voting on a failed proposal earns nothing, for the same anti-farming reason as the proposer reward.",
				Wired:       true,
			},
			{
				Name:        "Submit KYC for the first time",
				Delta:       int64(VerificationTrustDeltaKYCSubmit),
				Target:      "the wallet submitting",
				Dimension:   core.DimensionVerification.String(),
				Description: "A one-time bonus on genuine KYC self-attestation - explicitly not re-granted on a resubmission (e.g. correcting a typo'd name), only on the first-ever submission for a wallet.",
				Wired:       true,
			},
		},
	}

	json.NewEncoder(w).Encode(resp)
}
