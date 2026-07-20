package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

type ReputationResponse struct {
	Address      string          `json:"address"`
	Score        uint64          `json:"score"`
	CreditTier   core.CreditTier `json:"creditTier"`
	LoansRepaid  uint64          `json:"loansRepaid"`
	LoansDefault uint64          `json:"loansDefault"`
	LoansFunded  uint64          `json:"loansFunded"`
	TotalVolume  uint64          `json:"totalVolume"`

	// HighestTierReached/NextTier/NextTierReward describe REPT
	// progress (see core/reputation_reward.go) - HighestTierReached
	// can differ from CreditTier when reputation has since dropped
	// (e.g. a slash) below a tier already earned.
	HighestTierReached core.CreditTier `json:"highestTierReached"`
	NextTier            core.CreditTier `json:"nextTier,omitempty"`
	NextTierReward       uint64          `json:"nextTierReward,omitempty"`
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

	profile := creditEngine.Profiles.Get(address)
	wallet := walletStore.Get(address)

	resp := ReputationResponse{
		Address:            address,
		Score:               wallet.Reputation,
		CreditTier:          core.TierForReputation(wallet.Reputation),
		LoansRepaid:         profile.LoansRepaid,
		LoansDefault:        profile.LoansDefault,
		LoansFunded:         profile.LoansFunded,
		TotalVolume:         profile.TotalVolume,
		HighestTierReached: wallet.HighestTierReached,
	}

	if next, reward, ok := core.NextUnearnedTierReward(wallet.HighestTierReached); ok {
		resp.NextTier = next
		resp.NextTierReward = reward
	}

	json.NewEncoder(w).Encode(resp)
}
