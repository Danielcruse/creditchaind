package core

import (
	"crypto/sha256"
	"encoding/binary"
)

// Locked PoSR (Proof of Stake and Reputation) parameters.
// Approved 2026-07: Stake 25% / Reputation 55% / Uptime 20%,
// reputation dominates wealth by design ("Proof of Stake and
// Reputation", not "Proof of Richest"). All constants here are
// bootstrap defaults intended to become governance-adjustable.
//
// Selection weights and the slash decay are integer fixed-point
// (scale posrPrecision), not float64. IEEE-754's basic operators (+,
// -, *, /) are bit-identical across every conforming platform, but
// math.Pow (previously used for the slash decay below) carries no
// such cross-implementation guarantee - different libm builds can
// legitimately disagree in the last bit, which would let
// independently-run nodes disagree about who gets selected. Integer
// arithmetic has no such ambiguity, so validator selection stays
// byte-for-byte reproducible across every node/OS/architecture.
const (
	posrPrecision = 1_000_000

	posrStakeWeightFixed      = 250_000 // 0.25 * posrPrecision
	posrReputationWeightFixed = 550_000 // 0.55 * posrPrecision
	posrUptimeWeightFixed     = 200_000 // 0.20 * posrPrecision

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
	// consequence on top of the reputation penalty. Represented as an
	// exact fraction (9/10) and applied via repeated integer
	// multiplication in validatorWeight, rather than math.Pow.
	slashDecayNumerator   = 9
	slashDecayDenominator = 10
)

// validatorWeight computes a validator's PoSR selection weight,
// fixed-point scaled by posrPrecision (a validator at max stake/
// reputation/uptime with no slashes returns exactly posrPrecision).
// Reputation is read from the validator's own Wallet - Validator has
// no separate reputation field (see validator.go).
//
// v.Uptime is sourced elsewhere (ValidatorStatus.Uptime in
// validator_store.go) as a float64 ratio of two basic divisions,
// which IEEE-754 already guarantees is bit-identical everywhere; it's
// converted to fixed-point here with a single multiply+truncate,
// itself a basic, deterministic operation.
func validatorWeight(v *Validator) uint64 {
	normalizedStake := v.Stake
	if normalizedStake > StakeCap {
		normalizedStake = StakeCap
	}
	stakeFixed := normalizedStake * posrPrecision / StakeCap

	reputationFixed := v.Wallet.Reputation * posrPrecision / MaxReputation

	uptimeFixed := uint64(v.Uptime * posrPrecision)

	raw := (stakeFixed*posrStakeWeightFixed +
		reputationFixed*posrReputationWeightFixed +
		uptimeFixed*posrUptimeWeightFixed) / posrPrecision

	// decay starts at "full weight" (posrPrecision) and is repeatedly
	// scaled by 9/10, dividing back down to posrPrecision scale at
	// every step so intermediate values stay bounded no matter how
	// large SlashCount is. It reaches 0 after roughly 130 strikes -
	// break out then instead of looping SlashCount times (SlashCount
	// is attacker-adjacent via repeated slashing, so this keeps the
	// loop's cost independent of how high it's been driven).
	decay := uint64(posrPrecision)
	for i := uint64(0); i < v.SlashCount && decay > 0; i++ {
		decay = decay * slashDecayNumerator / slashDecayDenominator
	}

	return raw * decay / posrPrecision
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

// isEligibleBootstrap is isEligibleRelaxed without the stake floor too
// (and, deliberately, without the uptime floor either - see below) -
// the last-resort tier SelectValidator reaches for only when NEITHER
// the strict NOR the relaxed pool has a single eligible validator.
// Same deadlock reasoning isEligibleRelaxed's doc comment already
// gives for reputation applies symmetrically to stake: if every
// registered validator's CCT stake sits below MinValidatorStake (e.g.
// every validator predates a stake-source migration and nobody has
// re-staked yet), block production stops entirely, which means no
// transaction of any kind - including a stake transaction - can ever
// confirm again. That's the same closed loop with no honest way out,
// just triggered by stake instead of reputation.
//
// Uptime is deliberately dropped too, not just stake/reputation - a
// version of this tier that still required v.Uptime >=
// MinValidatorUptime would itself fail to break a deadlock where
// every validator's uptime has also drifted below the floor (uptime
// only recovers by producing blocks, the same circular dependency as
// reputation/stake), silently defeating the one guarantee this tier
// exists to provide. As the tier of absolute last resort - reached
// only when both stricter tiers are completely empty - "genuinely
// registered and active" is the only condition that can never itself
// become permanently false through the chain's own operation.
// Selection weight (validatorWeight) still scores stake/reputation/
// uptime normally in this pool - this only stops any of them from
// being an absolute veto in the one situation where enforcing them
// strictly would be self-defeating.
func isEligibleBootstrap(v *Validator) bool {
	return v.Active
}

// weightedValidator pairs a pool index with its precomputed weight -
// used instead of a parallel weights slice so skipped (zero-weight)
// validators can't desync the index between the weight list and pool.
type weightedValidator struct {
	poolIndex int
	weight    uint64
}

// selectFrom runs the actual deterministic weighted-random draw over
// whichever pool (strict or relaxed) SelectValidator decided to use.
func selectFrom(pool []*Validator, previousBlockHash string) *Validator {
	weighted := make([]weightedValidator, 0, len(pool))
	var totalWeight uint64

	for i, v := range pool {
		w := validatorWeight(v)
		if w == 0 {
			continue
		}
		weighted = append(weighted, weightedValidator{poolIndex: i, weight: w})
		totalWeight += w
	}

	if totalWeight == 0 {
		return nil
	}

	seed := sha256.Sum256([]byte(previousBlockHash))
	seedInt := binary.BigEndian.Uint64(seed[:8])
	point := (seedInt % posrPrecision) * totalWeight / posrPrecision

	var cumulative uint64
	for _, wv := range weighted {
		cumulative += wv.weight
		if point <= cumulative {
			return pool[wv.poolIndex]
		}
	}
	return pool[weighted[len(weighted)-1].poolIndex]
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
// isEligibleRelaxed if the strict pool is empty, then to
// isEligibleBootstrap if the relaxed pool is ALSO empty - see each
// tier's own doc comment for why it exists. Only returns nil if
// literally no registered validator is Active at all.
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
	if len(relaxed) > 0 {
		return selectFrom(relaxed, previousBlockHash)
	}

	bootstrap := make([]*Validator, 0, len(validators))
	for _, v := range validators {
		if isEligibleBootstrap(v) {
			bootstrap = append(bootstrap, v)
		}
	}
	if len(bootstrap) == 0 {
		return nil
	}
	return selectFrom(bootstrap, previousBlockHash)
}

