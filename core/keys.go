package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
)

type KeyPair struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

func NewKeyPair() KeyPair {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}

	return KeyPair{
		PublicKey:  pub,
		PrivateKey: priv,
	}
}

func (k KeyPair) PublicKeyHex() string {
	return hex.EncodeToString(k.PublicKey)
}