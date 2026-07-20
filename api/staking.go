package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var stakingStore = core.NewStakingStore()

// stakeableSymbols is every token staking supports today, in the
// order the UI displays them.
var stakeableSymbols = []string{"CCT", "ETHTEST", "USDCTEST"}

type StakePosition struct {
	Symbol  string `json:"symbol"`
	Staked  uint64 `json:"staked"`
	Rewards uint64 `json:"rewards"`
}

type StakingResponse struct {
	Address     string          `json:"address"`
	Positions   []StakePosition `json:"positions"`
	OETHBalance uint64          `json:"oethBalance"`
	VotingPower uint64          `json:"votingPower"`

	// Unbonding is still counted in its symbol's staked position
	// (still bonded/at-risk until it matures - see
	// core/staking_logic.go), but broken out here so the UI can show
	// "X of your Y staked ETHTEST is unbonding, available at T"
	// instead of it just silently sitting there. Each entry carries
	// its own Symbol.
	Unbonding []core.UnbondingEntry `json:"unbonding"`
}

func HandleStaking(
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

	wallet := walletStore.Get(address)
	stakingStore.SyncWallet(wallet, address)

	positions := make([]StakePosition, 0, len(stakeableSymbols))
	for _, symbol := range stakeableSymbols {
		positions = append(positions, StakePosition{
			Symbol:  symbol,
			Staked:  stakingStore.StakeOf(address, symbol),
			Rewards: stakingStore.Reward(address, symbol),
		})
	}

	resp := StakingResponse{
		Address:     address,
		Positions:   positions,
		OETHBalance: wallet.OETHBalance,
		VotingPower: wallet.VotingPower,
		Unbonding:   stakingStore.UnbondingFor(address),
	}

	json.NewEncoder(w).Encode(resp)
}
