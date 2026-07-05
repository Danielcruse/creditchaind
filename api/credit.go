package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var creditStore = core.NewCreditStore()

type CreditResponse struct {
	Address        string          `json:"address"`
	Reputation     uint64          `json:"reputation"`
	CreditTier     core.CreditTier `json:"creditTier"`
	LoansTaken     uint64          `json:"loansTaken"`
	LoansRepaid    uint64          `json:"loansRepaid"`
	LoansDefaulted uint64          `json:"loansDefaulted"`
}

func HandleCredit(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	address := r.URL.Query().Get("address")

	profile := creditStore.Get(address)
	wallet := walletStore.Get(address)

	resp := CreditResponse{
		Address:        address,
		Reputation:     wallet.Reputation,
		CreditTier:     core.TierForReputation(wallet.Reputation),
		LoansTaken:     profile.LoansTaken,
		LoansRepaid:    profile.LoansRepaid,
		LoansDefaulted: profile.LoansDefaulted,
	}

	json.NewEncoder(w).Encode(resp)
}
