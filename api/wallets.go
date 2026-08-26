package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

type WalletResponse struct {
	Address     string          `json:"address"`
	CCTBalance  uint64          `json:"cctBalance"`
	OETHBalance uint64          `json:"oethBalance"`
	StakedETH   uint64          `json:"stakedETH"`
	Rewards     uint64          `json:"rewards"`
	Reputation  uint64          `json:"reputation"`
	CreditTier  core.CreditTier `json:"creditTier"`
	VotingPower uint64          `json:"votingPower"`
	NodeID      string          `json:"nodeId"`
}

func HandleWallet(
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

	// keep the read-model in sync with the canonical staking ledger
	stakingStore.SyncWallet(wallet, address)

	resp := WalletResponse{
		Address:     wallet.Address,
		CCTBalance:  wallet.CCTBalance,
		OETHBalance: wallet.OETHBalance,
		StakedETH:   wallet.StakedETH,
		Rewards:     wallet.Rewards,
		Reputation:  wallet.Reputation,
		CreditTier:  core.TierForReputation(wallet.Reputation),
		VotingPower: wallet.VotingPower,
		NodeID:      wallet.NodeID,
	}

	json.NewEncoder(w).Encode(resp)
}
