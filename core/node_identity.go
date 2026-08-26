package core

import "crypto/ed25519"

// NodeIdentity is a node's own signing key - distinct from any user
// wallet - used to sign blocks it produces so peers can verify a
// gossiped block actually came from the validator it claims to.
type NodeIdentity struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

func NewNodeIdentity() NodeIdentity {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		panic(err)
	}

	return NodeIdentity{PublicKey: pub, PrivateKey: priv}
}

func (n NodeIdentity) SignBlock(block Block) []byte {
	return SignMessage(n.PrivateKey, []byte(block.Hash))
}

func VerifyBlockSignature(block Block, publicKey ed25519.PublicKey, signature []byte) bool {
	return VerifySignature(publicKey, []byte(block.Hash), signature)
}
