package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// protocolStatsResponse is every number the lending marketplace's
// stats bar shows - each field is computed live from real store data
// below, never a hardcoded/placeholder value. There is deliberately
// no "bridgeVolume" field - no bridge implementation exists anywhere
// in this codebase (see audit/KNOWN_GAPS.md), so a bridge-volume stat
// would have no real data source at all.
type protocolStatsResponse struct {
	TotalLiquidity   uint64 `json:"totalLiquidity"`
	TotalBorrowed    uint64 `json:"totalBorrowed"`
	ActiveLoans      int    `json:"activeLoans"`
	DefaultRatePct   uint64 `json:"defaultRatePercent"`
	AverageInterest  uint64 `json:"averageInterestPercent"`
	AverageRepute    uint64 `json:"averageReputation"`
	ActiveValidators int    `json:"activeValidators"`
	TotalStaked      uint64 `json:"totalStaked"`
}

// HandleProtocolStats aggregates real, already-existing data across
// the loan/loan-offer/loan-request/staking/validator stores - nothing
// here is fabricated or hardcoded. AverageReputation and
// AverageInterest are scoped to wallets/loans that have actually
// participated in lending (not every registered wallet, which would
// otherwise be diluted by brand-new wallets sitting at the default
// Reputation=50 with no lending activity at all).
func HandleProtocolStats(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	resp := protocolStatsResponse{
		ActiveValidators: countActiveValidators(),
	}

	participants := make(map[string]bool)
	var interestSum, interestCount uint64
	var defaulted, concluded int

	for _, loan := range loanStore.Loans {
		participants[loan.Borrower] = true
		if loan.Lender != "" {
			participants[loan.Lender] = true
		}

		switch loan.Status {
		case core.LoanFunded:
			resp.ActiveLoans++
			resp.TotalBorrowed += loan.Amount
			interestSum += loan.Interest
			interestCount++
		case core.LoanRepaid:
			concluded++
		case core.LoanDefault:
			concluded++
			defaulted++
		}
	}
	if interestCount > 0 {
		resp.AverageInterest = interestSum / interestCount
	}
	if concluded > 0 {
		resp.DefaultRatePct = uint64(defaulted) * 100 / uint64(concluded)
	}

	if len(participants) > 0 {
		var repSum uint64
		for address := range participants {
			repSum += walletStore.Get(address).Reputation
		}
		resp.AverageRepute = repSum / uint64(len(participants))
	}

	for _, offer := range loanOfferStore.List() {
		if offer.Status == core.LoanOfferOpen {
			resp.TotalLiquidity += offer.RemainingAmount
		}
	}
	// Open loan requests represent demand, not supplied liquidity - not
	// added to TotalLiquidity (which is specifically "capital a
	// borrower could draw against right now").

	for _, symbols := range stakingStore.Stakes {
		for _, amount := range symbols {
			resp.TotalStaked += amount
		}
	}

	json.NewEncoder(w).Encode(resp)
}
