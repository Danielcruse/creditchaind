package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var reputationEventStore = core.NewReputationEventStore()

// HandleGetReputationEvent is a small public lookup - given a
// transaction ID, reports whether it carried a reputation change and
// what it was. The explorer's own search/list responses already
// attach this inline (see api/explorer.go's buildTransactionSummary),
// so this endpoint exists mainly for a direct "does this tx ID have a
// reputation event" check without pulling the whole transaction.
func HandleGetReputationEvent(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	txID := r.URL.Query().Get("tx")
	event, ok := reputationEventStore.Get(txID)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{"found": false})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"found": true, "event": event})
}
