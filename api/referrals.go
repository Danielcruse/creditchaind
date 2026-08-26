package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

type referralsResponse struct {
	Address       string `json:"address"`
	ReferralCount uint64 `json:"referralCount"`
}

// HandleReferrals reports how many wallets an address has referred -
// "top community users have more voting rights, higher loans" is
// implemented via the referral bonuses already wired into
// votingWeight (api/governance_weight.go) and HandleBorrowingPower
// (api/borrowing_power.go); this endpoint just exposes the raw count
// for display. Public read, no auth - referral count carries no
// value at stake, same convention as every other public read.
func HandleReferrals(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "address required", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(referralsResponse{
		Address:       address,
		ReferralCount: core.ReferralCount(walletStore.Wallets, address),
	})
}
