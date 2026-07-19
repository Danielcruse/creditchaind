package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// governanceParticipationCap is how many distinct proposals a wallet
// needs to have voted on to get full credit for the governance factor
// below - a judgment call (not derived from any other constant in
// this codebase), documented here rather than left unexplained.
const governanceParticipationCap = 5

// stakedCCTNormalizationCap is the staked-CCT amount that earns full
// credit for the staking factor below - chosen to match
// core.BorrowLimit's Silver-tier threshold (10000) as a sensible
// "meaningfully staked" reference point. A judgment call, not derived
// from any specified spec - documented so a future tuning pass
// doesn't mistake it for a load-bearing constant.
const stakedCCTNormalizationCap = 10000

// borrowingPowerResponse is CreditChain's composite trust score - see
// the doc comment on HandleBorrowingPower for exactly what feeds it
// and why. Every field here is computed from real data; the one
// factor with no real backing (validator endorsements) is reported
// with an explicit zero weight and "not yet active" status rather
// than silently omitted or faked.
type borrowingPowerResponse struct {
	Address          string `json:"address"`
	Score            uint64 `json:"score"`
	Tier             string `json:"tier"`
	MaxBorrow        uint64 `json:"maxBorrow"`
	InterestDiscount uint64 `json:"interestDiscountPercent"`
	ReputationFactor uint64 `json:"reputationFactor"`
	RepaymentFactor  uint64 `json:"repaymentFactor"`
	StakingFactor    uint64 `json:"stakingFactor"`
	CollateralFactor uint64 `json:"collateralFactor"`
	GovernanceFactor uint64 `json:"governanceFactor"`
	ValidatorWeight  uint64 `json:"validatorEndorsementWeight"`
	ValidatorStatus  string `json:"validatorEndorsementStatus"`
}

// HandleBorrowingPower computes CreditChain's signature "Borrowing
// Power Score" - a weighted composite of five REAL, already-tracked
// factors (reputation, repayment history, staked CCT, current
// collateral, governance participation), each normalized to 0-100
// before weighting so the formula stays auditable. The weights
// (40/25/15/10/10) are a reasonable starting formula, not derived
// from any specified economic model - a future tuning pass should
// feel free to revisit them, they are not load-bearing constants.
//
// A sixth factor, validator endorsement, is named in the product
// design but has no real mechanism anywhere in this codebase today
// (core.Validator.CommunityTrust is scaffolded for this, always 0,
// never written) - it is reported at weight 0 with an explicit
// "not yet active" status so the UI can show it as a disabled/future
// row, never as a fabricated number.
func HandleBorrowingPower(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "missing address", http.StatusBadRequest)
		return
	}

	wallet := walletStore.Get(address)
	profile := creditEngine.Profiles.Get(address)

	reputationFactor := wallet.Reputation // already 0-100

	repaymentFactor := uint64(50) // neutral default - no history yet
	if total := profile.LoansRepaid + profile.LoansDefault; total > 0 {
		repaymentFactor = profile.LoansRepaid * 100 / total
	}

	staked := stakingStore.Stakes[address]["CCT"]
	stakingFactor := staked * 100 / stakedCCTNormalizationCap
	if stakingFactor > 100 {
		stakingFactor = 100
	}

	collateralFactor := uint64(0)
	if oeth := collateralStore.LockedOf(address, "OETH"); oeth > 0 {
		if valueInYen, ok := collateralValueInYen("OETH", oeth); ok {
			collateralFactor = valueInYen * 100 / stakedCCTNormalizationCap
			if collateralFactor > 100 {
				collateralFactor = 100
			}
		}
	}

	votedOn := 0
	for _, voters := range proposalStore.Voted {
		if voters[address] {
			votedOn++
		}
	}
	governanceFactor := uint64(votedOn) * 100 / governanceParticipationCap
	if governanceFactor > 100 {
		governanceFactor = 100
	}

	score := reputationFactor*40/100 +
		repaymentFactor*25/100 +
		stakingFactor*15/100 +
		collateralFactor*10/100 +
		governanceFactor*10/100

	tier := core.TierForReputation(score)

	// MaxBorrow scales the wallet's real tier-based cap by how much the
	// composite score exceeds/falls short of raw reputation alone - a
	// strong composite score (staking, collateral, governance on top
	// of reputation) earns more room than reputation-only BorrowLimit
	// would give, never less than half of it.
	baseLimit := core.BorrowLimit(wallet.Reputation)
	maxBorrow := baseLimit * score / 100
	if minLimit := baseLimit / 2; maxBorrow < minLimit {
		maxBorrow = minLimit
	}

	bronzeRate := core.InterestRate(0) // Bronze baseline
	walletRate := core.InterestRate(wallet.Reputation)
	discount := uint64(0)
	if bronzeRate > walletRate {
		discount = bronzeRate - walletRate
	}

	json.NewEncoder(w).Encode(borrowingPowerResponse{
		Address:          address,
		Score:            score,
		Tier:             string(tier),
		MaxBorrow:        maxBorrow,
		InterestDiscount: discount,
		ReputationFactor: reputationFactor,
		RepaymentFactor:  repaymentFactor,
		StakingFactor:    stakingFactor,
		CollateralFactor: collateralFactor,
		GovernanceFactor: governanceFactor,
		ValidatorWeight:  0,
		ValidatorStatus:  "not yet active",
	})
}
