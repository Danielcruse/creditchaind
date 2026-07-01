package core

import (
	"crypto/ed25519"
	"fmt"
)

func (tx *Transaction) Message() []byte {
	msg := fmt.Sprintf(
		"%s|%s|%d|%s|%d|%s",
		tx.From,
		tx.To,
		tx.Amount,
		tx.Type,
		tx.Timestamp,
		tx.Data,
	)

	return []byte(msg)
}

func (tx *Transaction) Sign(privateKey ed25519.PrivateKey) {
	tx.Signature = SignMessage(
		privateKey,
		tx.Message(),
	)
}

func (tx *Transaction) Verify(
	publicKey ed25519.PublicKey,
) bool {
	return VerifySignature(
		publicKey,
		tx.Message(),
		tx.Signature,
	)
}