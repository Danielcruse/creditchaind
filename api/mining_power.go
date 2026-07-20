package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// accountAgeNormalizationDays is how many days old a wallet needs to be
// to get full credit for the account-age factor below - a judgment
// call, not derived from any specified spec.
const accountAgeNormalizationDays = 180

// successfulLendingNormalizationCount is how many funded loans earns
// full credit for the successful-lending factor below - a judgment
// call.
const successfulLendingNormalizationCount = 20

// miningPowerResponse is the Reputation Credit "Mining Power" score -
// see HandleMiningPower's doc comment for what feeds it. Answers "how
// much RC is this wallet earning," a different question from
// Borrowing Power's "how much can this wallet borrow" - the two share
// formula code (repaymentFactorFor/stakingFactorFor/governanceFactorFor
// in api/borrowing_power.go) but stay independently shaped.
type miningPowerResponse struct {
	Address                string `json:"address"`
	Score                  uint64 `json:"score"`
	TrustScoreFactor       uint64 `json:"trustScoreFactor"`
	RepaymentFactor        uint64 `json:"repaymentFactor"`
	StakingFactor          uint64 `json:"stakingFactor"`
	GovernanceFactor       uint64 `json:"governanceFactor"`
	ValidatorFactor        uint64 `json:"validatorFactor"`
	SuccessfulLendingFactor uint64 `json:"successfulLendingFactor"`
	AccountAgeFactor       uint64 `json:"accountAgeFactor"`
	NetworkContribWeight   uint64 `json:"networkContributionsWeight"`
	NetworkContribStatus   string `json:"networkContributionsStatus"`
}

// miningPowerScore is the single source of truth for a wallet's Mining
// Power (0-100) - called by both HandleMiningPower and
// processReputationMining (api/reputation_mining.go), so the score
// used to decide how much REPT a wallet mines is identical to what the
// wallet sees on its own Mining Power view, never computed twice.
//
// Maps the Reputation Credit spec's 8 named mining factors onto real,
// already-tracked data:
//   - Trust Score (30%): Wallet.Reputation directly.
//   - Loan Repayment History (20%) / Staking Activity (15%) /
//     Governance Participation (10%): shared with Borrowing Power via
//     repaymentFactorFor/stakingFactorFor/governanceFactorFor.
//   - Validator Participation (10%): validator uptime if this address
//     IS a registered validator, else partial credit for delegating
//     stake to one.
//   - Successful Lending (10%): distinct from repayment history - this
//     is the LENDER side (LoansFunded), not the borrower side.
//   - Account Age (5%): Wallet.CreatedAt.
//   - Network Contributions (0%): no backing data exists anywhere in
//     this codebase for this factor - reported at weight 0/"not yet
//     active", the same convention Borrowing Power already uses for
//     Validator Endorsements. Nothing fabricated.
func miningPowerScore(address string, asOf int64) uint64 {
	wallet := walletStore.Get(address)

	trustScoreFactor := wallet.Reputation
	repaymentFactor := repaymentFactorFor(address)
	stakingFactor := stakingFactorFor(address)
	governanceFactor := governanceFactorFor(address)
	validatorFactor := validatorFactorFor(address)
	lendingFactor := successfulLendingFactorFor(address)
	ageFactor := accountAgeFactorFor(wallet, asOf)

	return trustScoreFactor*30/100 +
		repaymentFactor*20/100 +
		stakingFactor*15/100 +
		governanceFactor*10/100 +
		validatorFactor*10/100 +
		lendingFactor*10/100 +
		ageFactor*5/100
}

func validatorFactorFor(address string) uint64 {
	if v, ok := validatorStore.Validators[address]; ok && v.Active {
		return uint64(v.Uptime() * 100)
	}
	// Not a validator itself - partial credit for delegating stake to
	// one, capped the same way staking itself is (stakedCCTNormalizationCap).
	var delegated uint64
	for _, delegators := range delegationStore.Delegations {
		delegated += delegators[address]
	}
	factor := delegated * 50 / stakedCCTNormalizationCap // capped at 50, not 100 - delegating isn't the same as validating
	if factor > 50 {
		factor = 50
	}
	return factor
}

func successfulLendingFactorFor(address string) uint64 {
	funded := creditEngine.Profiles.Get(address).LoansFunded
	factor := funded * 100 / successfulLendingNormalizationCount
	if factor > 100 {
		factor = 100
	}
	return factor
}

func accountAgeFactorFor(wallet *core.Wallet, asOf int64) uint64 {
	if wallet.CreatedAt == 0 {
		return 0 // predates this field - unknown age, no credit rather than a fabricated one
	}
	ageDays := (asOf - wallet.CreatedAt) / int64(24*time.Hour/time.Second)
	if ageDays < 0 {
		return 0
	}
	factor := uint64(ageDays) * 100 / accountAgeNormalizationDays
	if factor > 100 {
		factor = 100
	}
	return factor
}

// HandleMiningPower returns a wallet's Reputation Credit Mining Power -
// see miningPowerScore's doc comment for the full factor breakdown.
func HandleMiningPower(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "missing address", http.StatusBadRequest)
		return
	}

	wallet := walletStore.Get(address)
	asOf := time.Now().Unix()

	json.NewEncoder(w).Encode(miningPowerResponse{
		Address:                 address,
		Score:                   miningPowerScore(address, asOf),
		TrustScoreFactor:        wallet.Reputation,
		RepaymentFactor:         repaymentFactorFor(address),
		StakingFactor:           stakingFactorFor(address),
		GovernanceFactor:        governanceFactorFor(address),
		ValidatorFactor:         validatorFactorFor(address),
		SuccessfulLendingFactor: successfulLendingFactorFor(address),
		AccountAgeFactor:        accountAgeFactorFor(wallet, asOf),
		NetworkContribWeight:    0,
		NetworkContribStatus:    "not yet active",
	})
}
