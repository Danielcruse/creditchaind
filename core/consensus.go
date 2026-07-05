package core

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// Locked PoSR (Proof of Stake and Reputation) parameters.
// Approved 2026-07: Stake 25% / Reputation 55% / Uptime 20%,
// reputation dominates wealth by design ("Proof of Stake and
// Reputation", not "Proof of Richest"). All constants here are
// bootstrap defaults intended to become governance-adjustable.
const (
	posrStakeWeight      = 0.25
	posrReputationWeight = 0.55
	posrUptimeWeight     = 0.20

	// StakeCap normalizes stake so no single whale can dominate
	// selection purely through wealth.
	StakeCap = 100_000

	// Eligibility gate - must pass ALL of these to enter the
	// weighted-random pool at all.
	MinValidatorStake          = 1_000
	MinValidatorUptime         = 0.90
	MinValidatorReputationGate = 20

	// Each slashing strike decays a validator's selection weight by
	// 10%, compounding. Reputation itself never goes below 1 (see
	// reputation.go) - this decay is a separate, additional
	// consequence on top of the reputation penalty.
	slashDecayFactor = 0.9
)

// validatorWeight computes a validator's PoSR selection weight.
// Reputation is read from the validator's own Wallet - Validator has
// no separate reputation field (see validator.go).
func validatorWeight(v *Validator) float64 {
	normalizedStake := float64(v.Stake) / float64(StakeCap)
	if normalizedStake > 1.0 {
		normalizedStake = 1.0
	}

	reputationFactor := float64(v.Wallet.Reputation) / float64(MaxReputation)
	uptimeFactor := v.Uptime

	raw := (normalizedStake * posrStakeWeight) +
		(reputationFactor * posrReputationWeight) +
		(uptimeFactor * posrUptimeWeight)

	decay := math.Pow(slashDecayFactor, float64(v.SlashCount))

	return raw * decay
}

// isEligible gates validators out of the selection pool entirely
// (as opposed to just down-weighting them). A validator can drop out
// of eligibility without its reputation ever hitting the 1-100
// floor/cap - eligibility and reputation are separate mechanisms.
func isEligible(v *Validator) bool {
	return v.Active &&
		v.Stake >= MinValidatorStake &&
		v.Uptime >= MinValidatorUptime &&
		v.Wallet.Reputation >= MinValidatorReputationGate
}

// isEligibleRelaxed is isEligible without the reputation floor - the
// fallback tier SelectValidator reaches for only when NOT ONE
// registered validator clears the strict gate. Without this, the
// network can permanently deadlock: reputation only ever grows back
// via blockProductionReputationGrowth (api/apply.go's
// applyBlockReward), which requires actually producing blocks, which
// requires being eligible in the first place. If every validator's
// reputation drifts below MinValidatorReputationGate through
// accumulated slashing, that's a closed loop with no honest way out -
// directly contradicting reputation.go's own documented guarantee
// that every participant always has a path back. Falling back to
// "genuinely registered, staked, and active, just currently
// low-reputation" (rather than freezing block production entirely)
// preserves that guarantee. This never weakens the reputation
// requirement's INFLUENCE - validatorWeight still weighs it at 55% -
// it only stops it from being an absolute veto in the one situation
// where enforcing it strictly would be self-defeating.
func isEligibleRelaxed(v *Validator) bool {
	return v.Active &&
		v.Stake >= MinValidatorStake &&
		v.Uptime >= MinValidatorUptime
}

// selectFrom runs the actual deterministic weighted-random draw over
// whichever pool (strict or relaxed) SelectValidator decided to use.
func selectFrom(pool []*Validator, previousBlockHash string) *Validator {
	weights := make([]float64, 0, len(pool))
	var totalWeight float64

	for _, v := range pool {
		w := validatorWeight(v)
		if w <= 0 {
			continue
		}
		weights = append(weights, w)
		totalWeight += w
	}

	if totalWeight <= 0 {
		return nil
	}

	seed := sha256.Sum256([]byte(previousBlockHash))
	seedInt := binary.BigEndian.Uint64(seed[:8])
	point := (float64(seedInt%1_000_000) / 1_000_000.0) * totalWeight

	var cumulative float64
	for i, w := range weights {
		cumulative += w
		if point <= cumulative {
			return pool[i]
		}
	}
	return pool[len(pool)-1]
}

// AnyStrictlyEligible reports whether at least one validator currently
// clears the full reputation gate - false means SelectValidator is
// leaning on the isEligibleRelaxed fallback tier, which the frontend
// surfaces (see api/validators.go's HandleConsensusStatus) so this
// stays visible rather than silently happening.
func AnyStrictlyEligible(validators []*Validator) bool {
	for _, v := range validators {
		if isEligible(v) {
			return true
		}
	}
	return false
}

// SelectValidator performs deterministic weighted-random selection
// among eligible validators, seeded from the previous block's hash
// so every honest node reaches the identical conclusion (required for
// consensus determinism) without falling back to round-robin
// (predictable/gameable) or highest-stake-wins (centralizing) -
// both explicitly rejected in favor of this approach. Falls back to
// isEligibleRelaxed only if the strict pool is completely empty - see
// its doc comment for why that fallback exists at all.
func SelectValidator(validators []*Validator, previousBlockHash string) *Validator {
	strict := make([]*Validator, 0, len(validators))
	for _, v := range validators {
		if isEligible(v) {
			strict = append(strict, v)
		}
	}
	if len(strict) > 0 {
		return selectFrom(strict, previousBlockHash)
	}

	relaxed := make([]*Validator, 0, len(validators))
	for _, v := range validators {
		if isEligibleRelaxed(v) {
			relaxed = append(relaxed, v)
		}
	}
	if len(relaxed) == 0 {
		return nil
	}
	return selectFrom(relaxed, previousBlockHash)
}

