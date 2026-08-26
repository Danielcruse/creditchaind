package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

type Block struct {
    Index        uint64
    Timestamp    int64
    PreviousHash string
    Hash         string
    Validator    string
    Transactions []Transaction

    // ProducerNodeID/Signature identify and authenticate which node
    // actually produced this block, distinct from Validator (the
    // wallet address credited with producing it) - a peer verifies
    // Signature against the public key ProducerNodeID advertised
    // during the P2P handshake before accepting a gossiped block.
    ProducerNodeID string
    Signature      []byte
}

func NewGenesisBlock() Block {
    return Block{
        Index:        0,
        Timestamp:    time.Now().Unix(),
        PreviousHash: "0",
        Hash:         "GENESIS",
        Validator:    "creditchain",
        Transactions: []Transaction{},
    }
}
func NewBlock(index uint64, previousHash string, validator string, txs []Transaction) Block {
	timestamp := time.Now().Unix()

	b := Block{
		Index:        index,
		Timestamp:    timestamp,
		PreviousHash: previousHash,
		Validator:    validator,
		Transactions: txs,
	}

	b.Hash = ComputeBlockHash(b)

	return b
}

// ComputeBlockHash derives a block's hash from its own content, so
// PreviousHash linkage between blocks (used for fork resolution and
// double-sign detection) actually means something. A block's Hash
// must be computed with its own Hash field left empty/ignored, so this
// only ever reads Index/Timestamp/PreviousHash/Validator/Transactions.
func ComputeBlockHash(b Block) string {
	msg := fmt.Sprintf(
		"%d|%d|%s|%s",
		b.Index, b.Timestamp, b.PreviousHash, b.Validator,
	)

	for _, tx := range b.Transactions {
		msg += "|" + tx.ID
	}

	sum := sha256.Sum256([]byte(msg))

	return hex.EncodeToString(sum[:])
}