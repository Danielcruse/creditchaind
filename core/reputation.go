package core

// Reputation floor/cap per the locked CreditChain PoSR spec:
// reputation lives on a 1-100 scale and can NEVER reach zero -
// every participant always has a path back through honest
// participation.
const (
	MinReputation = 1
	MaxReputation = 100
)

// clampReputation enforces the 1-100 floor/cap on any mutation.
func clampReputation(score uint64) uint64 {
	if score < MinReputation {
		return MinReputation
	}
	if score > MaxReputation {
		return MaxReputation
	}
	return score
}

// IncreaseWalletReputation is the single entry point for raising an
// address's reputation. Wallet is the canonical owner of Reputation
// for every participant, including validators (a Validator's
// reputation IS its embedded Wallet's reputation - see validator.go).
//
// Returns the REPT reward earned (0 if none) - non-zero only when
// this increase pushes the wallet's reputation into a CreditTier
// higher than any it has reached before (see HighestTierReached /
// ReputationTierReward). Minting the actual token is the caller's
// job; core only tracks the trust score and which tiers have already
// been rewarded.
func IncreaseWalletReputation(w *Wallet, amount uint64) uint64 {
	if w.Reputation+amount < w.Reputation {
		// overflow guard
		w.Reputation = MaxReputation
	} else {
		w.Reputation = clampReputation(w.Reputation + amount)
	}

	newTier := TierForReputation(w.Reputation)
	if TierRank(newTier) <= TierRank(w.HighestTierReached) {
		return 0
	}

	reward := cumulativeTierReward(w.HighestTierReached, newTier)
	w.HighestTierReached = newTier
	return reward
}

// DecreaseWalletReputation is the single entry point for lowering an
// address's reputation. It floors at 1, never 0 - no participant is
// ever mathematically unrecoverable.
func DecreaseWalletReputation(w *Wallet, amount uint64) {
	if amount >= w.Reputation {
		w.Reputation = MinReputation
		return
	}
	w.Reputation = clampReputation(w.Reputation - amount)
}

// DecreaseReputation operates on a Validator by mutating its embedded
// Wallet - Validator.Reputation is a reference to Wallet.Reputation,
// not a second copy. (There is no IncreaseReputation counterpart -
// the live block-production reward path works off wallets directly,
// via walletStore, not a *Validator; see api/apply.go's
// applyBlockReward.)
func DecreaseReputation(v *Validator, amount uint64) {
	DecreaseWalletReputation(&v.Wallet, amount)
}

// IncreaseWalletTrust/DecreaseWalletTrust are the dimension-aware
// counterparts to IncreaseWalletReputation/DecreaseWalletReputation
// above - see trust.go's TrustDimensions doc comment for why the flat
// score is being split. These are the NEW behavior; the old flat
// functions above are kept unchanged and still used for any
// transaction timestamped before TrustDimensionsCutoverUnixSeconds
// (api/apply.go) - every call site picks between the two based on its
// own timestamp check, the same pattern this codebase already
// established for H01FixCutoverUnixSeconds/H03FixCutoverUnixSeconds.
// These functions are NOT themselves cutover-aware - that decision
// belongs to the caller, matching the existing convention (the
// cutover constants live in api/apply.go, not core, and core has no
// business importing api's cutover schedule).
//
// Wallet.Reputation is kept in sync as a live MIRROR on every call
// (ComputeTSI(w.Trust, DefaultTrustWeights)) - see wallet.go's doc
// comment on the Reputation field for why. Tier-crossing REPT reward
// logic is unchanged from IncreaseWalletReputation above: it still
// keys off the mirrored Reputation value crossing into a new
// CreditTier, since CreditTier/BorrowLimit/InterestRate reading
// overall TSI (not a single dimension) is this pass's explicitly
// deferred scope, not something changed here.
func IncreaseWalletTrust(w *Wallet, dim TrustDimension, amount uint64) uint64 {
	seedTrustIfNeeded(w)

	current := w.Trust.get(dim)
	var next uint64
	if current+amount < current {
		// overflow guard, same as IncreaseWalletReputation above.
		next = MaxReputation
	} else {
		next = clampReputation(current + amount)
	}
	w.Trust.set(dim, next)
	w.Reputation = ComputeTSI(w.Trust, DefaultTrustWeights)

	newTier := TierForReputation(w.Reputation)
	if TierRank(newTier) <= TierRank(w.HighestTierReached) {
		return 0
	}
	reward := cumulativeTierReward(w.HighestTierReached, newTier)
	w.HighestTierReached = newTier
	return reward
}

func DecreaseWalletTrust(w *Wallet, dim TrustDimension, amount uint64) {
	seedTrustIfNeeded(w)

	current := w.Trust.get(dim)
	var next uint64
	if amount >= current {
		next = MinReputation
	} else {
		next = clampReputation(current - amount)
	}
	w.Trust.set(dim, next)
	w.Reputation = ComputeTSI(w.Trust, DefaultTrustWeights)
}
