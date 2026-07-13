package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var hubRulesStore = core.NewHubRulesStore()

type hubRulesSetData struct {
	Text string `json:"text"`
	// PaymentWindowMinutes: only "BUY_FROM_HUB"/"SELL_TO_HUB" keys are
	// meaningful - see core.HubRules.PaymentWindowFor.
	PaymentWindowMinutes map[string]int `json:"paymentWindowMinutes"`
	// MinReputationToApply gates HubAgentApplication submission - see
	// core.HubRules.MinReputationToApply's doc comment. 0 = no floor.
	MinReputationToApply uint64 `json:"minReputationToApply"`
}

// maxPaymentWindowMinutes caps a hub-set payment window at 24 hours -
// long enough for any real off-app payment method, short enough that
// an escrowed trade can't sit open indefinitely.
const maxPaymentWindowMinutes = 1440

// HandleSetHubRules lets a verified Trading Hub post/replace its own
// preset trading rules - shown to a trader before they can submit a
// trade request against this hub (see website/js/p2p.js). See
// core.HubRules's doc comment for why this is a disclosure gate, not a
// security boundary.
func HandleSetHubRules(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubRulesSetData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !companyStore.IsVerified(address) {
		http.Error(w, "only a verified Trading Hub can post trading rules", http.StatusForbidden)
		return
	}
	if len(req.Text) > 4000 {
		http.Error(w, "rules text must be 4000 characters or fewer", http.StatusBadRequest)
		return
	}
	for direction, minutes := range req.PaymentWindowMinutes {
		if direction != "BUY_FROM_HUB" && direction != "SELL_TO_HUB" {
			http.Error(w, "paymentWindowMinutes keys must be BUY_FROM_HUB or SELL_TO_HUB", http.StatusBadRequest)
			return
		}
		if minutes <= 0 || minutes > maxPaymentWindowMinutes {
			http.Error(w, "paymentWindowMinutes must be between 1 and 1440", http.StatusBadRequest)
			return
		}
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxHubRulesSet, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleGetHubRules is a public read, same as every other hub-facing
// lookup (rates, offers) - "" text if the hub hasn't set any yet.
func HandleGetHubRules(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	hub := r.URL.Query().Get("hub")
	rules, ok := hubRulesStore.Get(hub)
	if !ok {
		json.NewEncoder(w).Encode(map[string]string{"text": ""})
		return
	}

	json.NewEncoder(w).Encode(rules)
}
