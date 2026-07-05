package core

import (
	"crypto/ed25519"
)

type WalletAccount struct {
	Address    string
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

func CreateWallet() (
	*WalletAccount,
	error,
) {

	pub,
	priv,
	err :=
		GenerateKeyPair()

	if err != nil {
		return nil, err
	}

	address :=
		GenerateAddress(
			pub,
		)

	return &WalletAccount{
		Address:    address,
		PublicKey:  pub,
		PrivateKey: priv,
	}, nil
}