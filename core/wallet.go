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

	StakedETH uint64

	Rewards uint64

	Reputation uint64
	CreditScore uint64
	VotingPower uint64

	NodeID string
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
		NodeID:      "",
	}
}