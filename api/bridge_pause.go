package api

// BridgePausedKey is the sole emergency-eligible governance param for
// now (see core.ProposalKindEmergency's doc comment) - a general-
// electorate Emergency Proposal setting this to 1 is the fast,
// still-decentralized way to halt new bridge activity without relying
// on the admin multisig, which is the real motivation for this lever
// (the bridge vault's on-chain signer threshold remains a separately
// tracked Critical finding - see audit/KNOWN_GAPS.md - this doesn't
// close that gap, it just gives the community a way to react fast if
// it's ever exploited). Deliberately binary (0/1), not a richer
// per-chain/per-direction flag set - the simplest lever that actually
// stops new value from moving, kept narrow on purpose per
// ProposalKindEmergency's own allowlist discipline.
const BridgePausedKey = "BridgePaused"

// bridgePaused reads the live governance param - false (not paused)
// until a PARAM_CHANGE or EMERGENCY proposal has ever set it, same
// "seeded default, governance can override" pattern as every other
// governanceParam read in this codebase.
func bridgePaused() bool {
	return governanceParam(BridgePausedKey, 0) == 1
}
