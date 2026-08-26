package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var stakingStore = core.NewStakingStore()

// stakePurposeStore tracks how much of an address's staked CCT is
// earmarked for moderation stake, a Trading Hub's stake vault, or
// joining the validator pool (core.StakePurpose* constants) - separate
// from stakingStore's own "how much is staked" ledger, which has no
// concept of why. Nothing locks into this yet (wired up by the
// Trading Hub/moderator overhaul that builds on it) - declared now so
// persistence and the store itself are in place ahead of that work.
var stakePurposeStore = core.NewStakePurposeStore()

// stakeableSymbols is every token staking supports today, in the
// order the UI displays them. ETHTEST/USDCTEST were retired here -
// ETHB (real bridged ETH, testnet) and ETH_CCC1 (real vault-locked ETH,
// mainnet - see api/stake.go's doc comment) both replace ETHTEST as the
// stakeable "ETH" asset, and USDCTEST is repurposed as a co-payment
// asset alongside YEN-e rather than something staked. Listing both
// unconditionally (not network-gated) mirrors how ETHB itself was never
// gated out of this list on mainnet either - a caught-in-review gap:
// ETH_CCC1 was made stakeable via api/apply.go's stakeTokenStore
// without ALSO being added here, which would have made any successful
// ETH_CCC1 stake permanently invisible to this listing endpoint even
// though core.StakingStore correctly recorded it. Any wallet with stake
// still outstanding under the old symbols keeps it visible via
// StakingResponse.LegacyPositions below - see
// api/legacy_stake_migration.go for how that outstanding stake gets
// closed out.
var stakeableSymbols = []string{"CCT", "ETHB", "ETH_CCC1"}

// legacyStakeSymbols is what stakeableSymbols used to be - no longer
// offered for new stakes (api/apply.go's applyStake rejects them), but
// still read here so a wallet with an unredeemed legacy position isn't
// left wondering where it went.
var legacyStakeSymbols = []string{"ETHTEST", "USDCTEST"}

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

	// LegacyPositions surfaces any still-nonzero ETHTEST/USDCTEST stake
	// (legacyStakeSymbols) that the migration sweep hasn't redeemed yet
	// - either it's still queued for its next sweep pass, or it's
	// locked as an open loan's oETH collateral and is deliberately left
	// alone until that loan resolves (see
	// api/legacy_stake_migration.go). Omitted entirely (not just
	// zero-valued) once nothing legacy remains for this address.
	LegacyPositions []StakePosition `json:"legacyPositions,omitempty"`

	// DelegatedOutCCT is how much of this address's CCT is currently
	// delegated out to back a validator's PoSR weight (core.DelegationStore) -
	// a separate pool from Positions' own CCT entry, both funded from the
	// same liquid balance but tracked independently (see that store's
	// own doc comment). AvailableStakedCCT is the CCT position's Staked
	// amount minus this - what's actually still free for anything that
	// depends on a real, uncommitted stake (e.g. the moderator
	// registration floor, see api/moderators.go's availableStakedCct).
	DelegatedOutCCT    uint64 `json:"delegatedOutCCT"`
	AvailableStakedCCT uint64 `json:"availableStakedCCT"`
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
			Symbol: symbol,
			Staked: stakingStore.StakeOf(address, symbol),
			// Rewards is always 0 for current (non-legacy) positions -
			// staking CCT/ETHB no longer mints anything (see
			// api/apply.go's applyStake); it's a pure locking
			// mechanism now, with mining/block rewards paying
			// validators instead (PoSR). Showing a nonzero
			// stakingStore.Reward() figure here would promise a
			// payout that will never actually land.
			Rewards: 0,
		})
	}

	var legacyPositions []StakePosition
	for _, symbol := range legacyStakeSymbols {
		staked := stakingStore.StakeOf(address, symbol)
		if staked == 0 {
			continue
		}
		legacyPositions = append(legacyPositions, StakePosition{
			Symbol:  symbol,
			Staked:  staked,
			Rewards: stakingStore.Reward(address, symbol),
		})
	}

	resp := StakingResponse{
		Address:            address,
		Positions:          positions,
		OETHBalance:        wallet.OETHBalance,
		VotingPower:        wallet.VotingPower,
		Unbonding:          stakingStore.UnbondingFor(address),
		LegacyPositions:    legacyPositions,
		DelegatedOutCCT:    delegationStore.TotalDelegatedBy(address),
		AvailableStakedCCT: availableStakedCct(address),
	}

	json.NewEncoder(w).Encode(resp)
}
