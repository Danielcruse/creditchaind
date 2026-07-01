package core

import "time"

type Block struct {
    Index        uint64
    Timestamp    int64
    PreviousHash string
    Hash         string
    Validator    string
    Transactions []Transaction
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
	return Block{
		Index:        index,
		Timestamp:    time.Now().Unix(),
		PreviousHash: previousHash,
		Hash:         "TEMP_HASH",
		Validator:    validator,
		Transactions: txs,
	}
}