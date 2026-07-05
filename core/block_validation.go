package core

import "time"

// MaxFutureBlockDrift bounds how far ahead of this node's own wall
// clock a block's Timestamp may be - generous enough to tolerate real
// clock skew between independently-run nodes, but not so generous
// that a malicious or buggy peer can stamp a block arbitrarily far in
// the future. Timestamp-driven logic elsewhere (e.g.
// StakingStore.ProcessMaturedUnbonds, called with each confirmed
// block's own Timestamp) inherits whatever a validator's clock says,
// so an unbounded timestamp would let a single bad block fast-forward
// every node's view of which unbonds have matured.
const MaxFutureBlockDrift = 120 * time.Second

// ValidateBlockTimestamp reports whether candidate's Timestamp is
// plausible relative to previous (the block it directly builds on):
// time must not run backwards between consecutive blocks, and the new
// block must not claim to be from further in the future than
// MaxFutureBlockDrift allows. This is checked on every block a node
// RECEIVES (gossiped or during chain sync) - a node's own production
// already stamps time.Now() and always builds on its own latest
// block, so it can never fail this by construction; the check exists
// to reject a peer's out-of-bounds block, not to second-guess local
// production.
func ValidateBlockTimestamp(candidate, previous Block) bool {
	if candidate.Timestamp < previous.Timestamp {
		return false
	}

	maxAllowed := time.Now().Unix() + int64(MaxFutureBlockDrift.Seconds())
	return candidate.Timestamp <= maxAllowed
}
