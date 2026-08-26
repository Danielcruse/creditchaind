package api

import (
	"encoding/json"
	"net/http"
	"sort"
)

// executiveCommitteeSize is a judgment call, tunable - small enough
// that executive authority (see api/executive_proposal.go) is
// meaningfully narrower than the broad community governance quorum.
const executiveCommitteeSize = 5

// rcLeaderboardCap bounds GET /rc/leaderboard the same "cap it, don't
// return everything" discipline the reputation-mining sweep's own
// scaling already follows.
const rcLeaderboardCap = 100

type reputationHolder struct {
	Address string
	Balance uint64
}

// sortedReputationHolders ranks every wallet with a real, nonzero
// reptStore balance, highest first, address as a deterministic
// tiebreak - live-computed on every call, never persisted or cached,
// matching the "derived live" precedent Mining Power/Borrowing Power
// already established elsewhere in this codebase.
func sortedReputationHolders() []reputationHolder {
	holders := make([]reputationHolder, 0, len(reptStore.Balances))
	for addr, bal := range reptStore.Balances {
		if bal == 0 {
			continue
		}
		holders = append(holders, reputationHolder{Address: addr, Balance: bal})
	}
	sort.Slice(holders, func(i, j int) bool {
		if holders[i].Balance != holders[j].Balance {
			return holders[i].Balance > holders[j].Balance
		}
		return holders[i].Address < holders[j].Address
	})
	return holders
}

// topReputationHolders returns the top n addresses by real RC balance -
// the live-computed "executive committee," no election, no persisted
// role, no term.
func topReputationHolders(n int) []string {
	holders := sortedReputationHolders()
	if len(holders) > n {
		holders = holders[:n]
	}
	out := make([]string, len(holders))
	for i, h := range holders {
		out[i] = h.Address
	}
	return out
}

func isExecutive(address string) bool {
	if address == "" {
		return false
	}
	for _, addr := range topReputationHolders(executiveCommitteeSize) {
		if addr == address {
			return true
		}
	}
	return false
}

// HandleAmIExecutive tells the caller whether they're currently one of
// the top-N reputation holders - purely informational, so the frontend
// can show/hide the executive-only voting lane without the user finding
// out only after a rejected POST. The real gate is always the apply-
// time isExecutive re-check, never this response alone.
func HandleAmIExecutive(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	json.NewEncoder(w).Encode(map[string]any{
		"isExecutive":       isExecutive(address),
		"committeeSize":     executiveCommitteeSize,
	})
}

type rcLeaderboardEntry struct {
	Address     string `json:"address"`
	RCBalance   uint64 `json:"rcBalance"`
	IsExecutive bool   `json:"isExecutive"`
}

// HandleRCLeaderboard is a public read ranking wallets by real RC
// (REPT) balance, capped at rcLeaderboardCap - no fabricated numbers,
// matches this project's disclosed-stub discipline throughout.
func HandleRCLeaderboard(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	holders := sortedReputationHolders()
	if len(holders) > rcLeaderboardCap {
		holders = holders[:rcLeaderboardCap]
	}

	execs := make(map[string]bool, executiveCommitteeSize)
	for _, addr := range topReputationHolders(executiveCommitteeSize) {
		execs[addr] = true
	}

	out := make([]rcLeaderboardEntry, len(holders))
	for i, h := range holders {
		out[i] = rcLeaderboardEntry{
			Address:     h.Address,
			RCBalance:   h.Balance,
			IsExecutive: execs[h.Address],
		}
	}
	json.NewEncoder(w).Encode(out)
}
