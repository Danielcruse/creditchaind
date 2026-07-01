package api

import (
	"encoding/json"
	"net/http"
)

type ExplorerResponse struct {
	BlockHeight        int                `json:"blockHeight"`
	LatestBlockHash    string             `json:"latestBlockHash"`
	RecentBlocks       interface{}        `json:"recentBlocks"`
	RecentTransactions interface{}        `json:"recentTransactions"`
}

func HandleExplorer(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	var latestHash string

	if len(blockchain.Blocks) > 0 {
		latestHash =
			blockchain.
				Blocks[
				len(
					blockchain.Blocks,
				)-1].
				Hash
	}

	resp := ExplorerResponse{
		BlockHeight:        len(blockchain.Blocks),
		LatestBlockHash:    latestHash,
		RecentBlocks:       blockchain.Blocks,
		RecentTransactions: mempool.Transactions,
	}

	json.NewEncoder(w).Encode(resp)
}