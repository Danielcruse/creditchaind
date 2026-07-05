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

	Reputation uint64
	CreditScore uint64
	VotingPower uint64

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
		// Reputation starts at 50 (already within Silver's range), so
		// the watermark starts there too - otherwise every fresh
		// wallet would immediately "achieve" Silver for free the
		// moment anything nudges reputation at all, rather than REPT
		// only rewarding tiers actually earned past the starting point.
		HighestTierReached: TierForReputation(50),
		NodeID:              "",
	}
}