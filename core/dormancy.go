package core

// DormancyPeriodSeconds is how long a wallet can go with no signed
// activity from its own key before its balance is reclaimed and
// redistributed - the "unlike Bitcoin, this can't be lost forever"
// rule. The real product intent is 50 YEARS - compressed here to a
// demo-scale value (same reasoning as UnbondingPeriodSeconds) so the
// whole lifecycle can actually be exercised/verified in one sitting. A
// real deployment would set this to the intended 50 years, not
// minutes.
const DormancyPeriodSeconds = 300 // 5 minutes, demo-scale stand-in for 50 years

// IsDormant reports whether w has gone quiet long enough, as of asOf,
// to be swept. LastActivityAt == 0 means "never eligible" (see its
// doc comment) - deliberately not "dormant since the epoch," and a
// wallet that's already been reclaimed never becomes dormant again
// (there's nothing left in it to reclaim). dormancyPeriodSeconds is
// passed in rather than read from the package const directly - the
// api package's GovernanceParamsStore (which core can't import) may
// override it per-network, so core stays a pure function of its
// inputs and lets the caller resolve the effective value.
func (w *Wallet) IsDormant(asOf int64, dormancyPeriodSeconds int64) bool {
	if w.ReclaimedAt != 0 || w.LastActivityAt == 0 {
		return false
	}
	return asOf-w.LastActivityAt >= dormancyPeriodSeconds
}

// QualifiesForAirdrop reports whether w is a legitimate recipient of
// OTHER wallets' reclaimed balance right now - genuinely recent
// activity, full stop. This is deliberately NOT just "!IsDormant()":
// IsDormant() returns false for an already-reclaimed wallet
// specifically so it doesn't get swept a second time (nothing left to
// take), but that's an exemption from being reclaimed again, not
// proof of current activity - its LastActivityAt can still be
// arbitrarily stale. Without this separate, stricter check, a wallet
// that was reclaimed once and then never touched again could keep
// re-qualifying for every later airdrop forever, off the strength of
// activity from before it went dormant in the first place.
func (w *Wallet) QualifiesForAirdrop(asOf int64, dormancyPeriodSeconds int64) bool {
	if w.LastActivityAt == 0 {
		return false
	}
	return asOf-w.LastActivityAt < dormancyPeriodSeconds
}
