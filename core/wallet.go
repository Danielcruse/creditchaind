package core

import (
	"crypto/rand"
	"encoding/hex"
)

type Wallet struct {
	Address string
	Keys    KeyPair

	Balance uint64

	CCTBalance  uint64
	OETHBalance uint64

	StakedETH      uint64
	StakedCCT      uint64
	StakedUSDCTEST uint64

	Rewards uint64

	Reputation  uint64
	CreditScore uint64
	VotingPower uint64

	// Trust splits Reputation above into six independently-tracked
	// dimensions (see trust.go) - Reputation itself is kept as a
	// live-synced MIRROR of ComputeTSI(Trust, DefaultTrustWeights),
	// updated on every post-cutover mutation (see reputation.go's
	// IncreaseWalletTrust/DecreaseWalletTrust), not the canonical value
	// anymore. Kept so every existing reader (frontend JS, other API
	// responses) that still reads the flat field keeps working
	// unchanged. A zero value here (Trust == TrustDimensions{}) means
	// "not yet seeded" for a wallet that predates this field - see
	// seedTrustIfNeeded in trust.go. NewWallet() below sets this
	// explicitly so a genuinely new wallet never has a zero value to
	// seed from.
	Trust TrustDimensions

	// GovernanceConviction is a 0-100 accumulated-trust score, distinct
	// from Trust.GovernanceTrust: GovernanceTrust is a live, point-in-time
	// dimension (can move instantly on a single event), while
	// GovernanceConviction chases it gradually over many sweep epochs
	// (grows slowly, decays faster than it grows) - see
	// api/governance_conviction.go's sweep for the exact formula. Exists
	// specifically so ProposalKindNodeAdmission votes (a Sybil-resistance
	// mechanism) are weighted by demonstrated, SUSTAINED governance
	// standing rather than a score an attacker could spike right before
	// a single vote. Not consumed by the general votingWeight formula
	// (api/governance_weight.go) - deliberately scoped to node-admission
	// votes only, see that file's own doc comment for why generalizing
	// it was considered and rejected.
	GovernanceConviction uint64

	// HighestTierReached is the watermark used to decide whether a
	// CreditTier crossing is a genuine first-time achievement (see
	// ReputationTierReward) - without it, reputation dropping below a
	// tier (e.g. a slash) and climbing back through it later would
	// re-mint the same REPT reward every time, turning progression
	// rewards into a farmable loop.
	HighestTierReached CreditTier

	NodeID string

	// CreatedAt is set once, at genuine wallet-creation time (see
	// api/wallet_create.go's HandleCreateWallet) - used as the
	// low-friction "security question" in the recovery/migration flow
	// (see core/recovery.go): "what year did you create this wallet."
	// Wallets that came into existence some other way (e.g. the
	// auto-created placeholder NewWallet() returns for an address seen
	// for the first time via WalletStore.Get) keep this at the zero
	// value, which recovery treats as "no year on record" rather than
	// matching any claimed year.
	CreatedAt int64

	// LastActivityAt is the last time this wallet's own key signed
	// something (set by api's applyTxOnce for the transaction's
	// AuthAddress, using the transaction's own timestamp for
	// cross-node determinism - never wall-clock). Zero means "no
	// activity ever recorded" - either this entry was auto-vivified as
	// a pass-through participant that was never a genuine key-holder
	// (see WalletStore.Get), or it predates this field existing at
	// all; either way, zero deliberately means "not eligible for
	// dormancy sweep" rather than "eligible since the beginning of
	// time" - see IsDormant in dormancy.go.
	LastActivityAt int64

	// ReclaimedAt is set once this wallet's balance has actually been
	// swept as dormant - a wallet that's already been reclaimed never
	// becomes dormant again (there's nothing left to reclaim).
	ReclaimedAt int64

	// ReferredBy is the address that referred this wallet at
	// registration time (see api/wallet_create.go) - empty if none.
	// Set once, never changed afterward. Referral "codes" are just the
	// referrer's own CreditChain address (shared as a link,
	// register.html?ref=<address>) - no separate code-issuing system,
	// matching this project's precedent of reusing an existing
	// identifier rather than minting a new one where possible.
	ReferredBy string

	// ConnectedHub is the verified Trading Hub (see core.CompanyStore)
	// this wallet last arrived through a hub's own shareable link
	// (website/hub-link.html?hub=<address>) - deliberately separate
	// from ReferredBy above: ReferredBy is a one-time referral-bonus
	// attribution fixed at registration, while ConnectedHub can be set
	// (and changed, if the wallet later follows a DIFFERENT hub's link)
	// at any point in a wallet's life, including long after it already
	// existed. Drives the P2P marketplace's "continue trading with
	// this hub" banner and listing sort (see website/js/p2p.js's
	// loadHubDirectory) - purely a UX recommendation, never a
	// permission or a cut of anything. Set directly at wallet-creation
	// time for a brand-new wallet arriving via a hub link (see
	// api/wallet_create.go, same unauthenticated-at-creation precedent
	// as ReferredBy), or via the signed core.TxWalletConnectHub
	// transaction for a wallet that already existed (see
	// api/apply.go's applyWalletConnectHub) - the signature requirement
	// there is what stops a stranger from silently "connecting" someone
	// else's wallet to a hub. Empty if this wallet has never followed a
	// hub link.
	ConnectedHub string

	// PublicKeyHex is captured the same place/way LastActivityAt is
	// (api/apply.go's applyTxOnce, from tx.AuthPublicKey, whenever this
	// address signs anything) - NOT populated at wallet creation, since
	// Address = SHA256(publicKey)[:20] is one-way and the real public
	// key otherwise only ever exists transiently inside a live signed
	// request/transaction, never persisted centrally anywhere else in
	// this codebase. This is what lets a pure block-driven sweep (which
	// only ever has a bare address on hand, never a live request) still
	// resolve a wallet's pubkey-derived sub-addresses (ETHTEST/USDCTEST/
	// YENE/ETHB/BTCB - see core.DeriveTokenAddress) - see
	// api/legacy_stake_migration.go for the sweep that needed this.
	// Empty for a wallet that has never yet signed anything with this
	// address as AuthAddress.
	PublicKeyHex string

	// RCInfusionWeightBps accumulates across every TxRCInfuse this
	// wallet has ever submitted (api/rc_infuse.go) - each infusion adds
	// RCAmount*RealWorldTrustWeightBps/10000 at the bps weight in effect
	// AT THE TIME of that infusion (never retroactively revalued if the
	// weight later changes - keeps each infusion's effect stable and
	// auditable). Feeds an additive, capped boost into miningPowerScore
	// (api/mining_power.go) - infusing RC as "real world trust" makes a
	// wallet's own future continuous mining a little stronger, bounded
	// so it can never alone produce unbounded emission.
	RCInfusionWeightBps uint64
}

func NewWallet() Wallet {
	bytes := make([]byte, 20)

	_, err := rand.Read(bytes)
	if err != nil {
		panic(err)
	}

	return Wallet{
		Address:     "CCT" + hex.EncodeToString(bytes),
		Keys:        NewKeyPair(),
		Balance:     0,
		CCTBalance:  0,
		OETHBalance: 0,
		StakedETH:   0,
		Rewards:     0,
		Reputation:  50,
		CreditScore: 50,
		VotingPower: 0,
		// Explicitly set (not left at the zero value) so a genuinely
		// new wallet never passes through seedTrustIfNeeded's lazy-seed
		// path at all - matches Reputation's own starting value.
		Trust: TrustDimensions{
			RepaymentTrust:    50,
			TradingTrust:      50,
			GovernanceTrust:   50,
			ContributionTrust: 50,
			VerificationTrust: 50,
			DisputeTrust:      50,
		},
		// Reputation starts at 50 (already within Silver's range), so
		// the watermark starts there too - otherwise every fresh
		// wallet would immediately "achieve" Silver for free the
		// moment anything nudges reputation at all, rather than REPT
		// only rewarding tiers actually earned past the starting point.
		HighestTierReached: TierForReputation(50),
		NodeID:             "",
	}
}
