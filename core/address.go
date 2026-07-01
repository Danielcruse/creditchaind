package core

import (
	"crypto/sha256"
	"encoding/hex"
)

func GenerateAddress(
	publicKey []byte,
) string {

	hash :=
		sha256.Sum256(
			publicKey,
		)

	return hex.EncodeToString(
		hash[:20],
	)
}