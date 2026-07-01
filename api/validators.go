package api

import (
	"encoding/json"
	"net/http"
)

type ValidatorInfo struct {
	Address         string `json:"address"`
	Stake           uint64 `json:"stake"`
	Reputation      uint64 `json:"reputation"`
	Rewards         uint64 `json:"rewards"`
	BlocksProduced  uint64 `json:"blocksProduced"`
	VotingPower     uint64 `json:"votingPower"`
	Active          bool   `json:"active"`
}

func HandleValidators(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	/*
		TODO:
		Load validator set from
		validator manager when exposed.
	*/

	validators := []ValidatorInfo{}

	json.NewEncoder(w).Encode(
		validators,
	)
}