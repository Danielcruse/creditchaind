package api

import (
	"encoding/json"
	"net/http"
)

// HandleGovernanceStatusForUser is the "social media manner" per-user
// log the user asked for - every status update one address has ever
// posted, oldest first (see core.GovernanceStatusStore.ForAddress).
// Public read, no auth - same convention as HandleRecentGovernanceStatuses.
func HandleGovernanceStatusForUser(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "address required", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(governanceStatusStore.ForAddress(address))
}

// HandleGovernanceVotesForUser is the permanent, public vote-history
// record - "community remembers you voted for X" per the user's own
// framing, never a punitive score, just a transparent record of past
// votes. Public read, no auth.
func HandleGovernanceVotesForUser(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "address required", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(proposalStore.VotesForAddress(address))
}
