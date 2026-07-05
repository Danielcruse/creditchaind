package core

import (
	"crypto/ed25519"
)

func SignMessage(privateKey ed25519.PrivateKey, message []byte) []byte {
	return ed25519.Sign(privateKey, message)
}

func VerifySignature(
	publicKey ed25519.PublicKey,
	message []byte,
	signature []byte,
) bool {
	return ed25519.Verify(publicKey, message, signature)
}