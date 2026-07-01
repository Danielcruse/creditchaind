package api

import (
	"encoding/json"
	"net/http"
)

type ReputationResponse struct {
	Address      string `json:"address"`
	Score        uint64 `json:"score"`
	LoansRepaid  uint64 `json:"loansRepaid"`
	LoansDefault uint64 `json:"loansDefault"`
	LoansFunded  uint64 `json:"loansFunded"`
	TotalVolume  uint64 `json:"totalVolume"`
}

func HandleReputation(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	address := r.URL.Query().Get("address")

	if address == "" {
		w.WriteHeader(http.StatusBadRequest)

		json.NewEncoder(w).Encode(
			map[string]string{
				"error": "missing address",
			},
		)

		return
	}

	profile := creditEngine.
		Profiles.
		Get(address)

	resp := ReputationResponse{
		Address:      profile.Address,
		Score:        profile.Score,
		LoansRepaid:  profile.LoansRepaid,
		LoansDefault: profile.LoansDefault,
		LoansFunded:  profile.LoansFunded,
		TotalVolume:  profile.TotalVolume,
	}

	json.NewEncoder(w).Encode(resp)
}