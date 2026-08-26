package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var moderatorVerificationLog = core.NewModeratorVerificationLog()

type moderatorVerificationsRequest struct {
	Moderator string `json:"moderator"`
}

// HandleModeratorVerifications is the executive-only ledger read the
// Phase 4 plan calls for - "that document will ever only be available
// in a ledger in governance where top executive can view each
// moderator's log." Signed (POST, not a plain GET+query-param) so the
// caller's identity is actually proven via requireSignedRequest rather
// than trusted from an unauthenticated query string - the same
// requireSignedRequest mechanism every mutating endpoint already uses,
// reused here for its identity-proof property even though this handler
// performs no state mutation.
func HandleModeratorVerifications(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req moderatorVerificationsRequest
	_, callerAddress, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if !isExecutive(callerAddress) {
		http.Error(w, "only a current top-reputation executive may read the moderator verification ledger", http.StatusForbidden)
		return
	}
	if req.Moderator == "" {
		http.Error(w, "missing moderator", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"entries": moderatorVerificationLog.ForModerator(req.Moderator),
	})
}
