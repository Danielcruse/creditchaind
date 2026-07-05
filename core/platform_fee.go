package core

// PlatformFeeBps is OUTERNET's cut of every completed trade in the P2P
// Trading Center - a direct P2POffer accept/release, a Hub Rate trade
// (either the ONCHAIN or off-app session), or a fulfilled Buy Request.
// 30 basis points = 0.3%, taken only from a trade actually settling
// (never from an escrow deposit, and never from a refund/cancel - see
// api/apply.go's applyPlatformFee for exactly where this is drawn).
// Disclosed to the trader wherever a trade quote is shown (see
// website/js/p2p.js), since a fee silently baked into a rate would
// defeat the whole point of a fee being "clearly stated."
const PlatformFeeBps = 30

// PlatformFeeAmount computes the fee slice of amount at PlatformFeeBps,
// rounding down - the trader is never charged a fraction of a unit
// more than the disclosed rate.
func PlatformFeeAmount(amount uint64) uint64 {
	return amount * PlatformFeeBps / 10000
}
