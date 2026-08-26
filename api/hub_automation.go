package api

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// Package doc: scoped Trading Hub automation, NOT a general smart-
// contract/scripting surface. A full arbitrary-hub-authored-logic
// execution environment would mean running third-party code with the
// ability to move funds - the single highest-risk category of feature
// this codebase could add, disproportionate to what was actually
// asked for ("automate chat response and payment... charge any hub
// that uses it"). What's built instead: a hub opts into a paid
// "Automation" tier subscription (this file), which lets its already-
// existing HubWorker delegation (previously rate-setting only) ALSO
// (a) post one of the hub's own pre-written CannedReplies
// (core.HubRules.CannedReplies) when a trade needs a chat response,
// and (b) auto-confirm-seller-paid on the hub's behalf, but only for
// a trade at or under a per-worker cap the hub sets themselves
// (core.HubWorker.AutoConfirmCapAmount). Both actions reuse the
// existing, already-audited HandleP2PMessage/HandleP2PSellerConfirmPaid
// code paths (api/p2p_external.go) - only newly reachable by an
// authorized automation worker identity instead of only the hub's own
// key. See core/hub_worker.go and core/hub_rules.go for the actual
// authorization/content checks.

var hubAutomationStore = core.NewHubAutomationStore()

// automationTierFeeAddress mirrors PLATFORM_FEE_ADDRESS's env-var-
// gated pattern (core/platform_fee.go) - a real, operator-supplied
// address the monthly Automation-tier fee is swept to. Empty means
// the fee is simply not collected (fails closed on revenue, not on
// the feature - a misconfigured deployment shouldn't block a hub from
// subscribing, same "fail open on the feature, fail closed on the
// money" precedent PlatformFee already sets).
func automationTierFeeAddress() string {
	return os.Getenv("AUTOMATION_TIER_FEE_ADDRESS")
}

// AutomationTierMonthlyFeeCCT is a judgment call (not derived from any
// specified pricing model) - a flat recurring cost, not per-trade,
// since per-trade fee stacking already exists via the 0.3% platform
// fee and shouldn't be double-charged for the same trade.
const AutomationTierMonthlyFeeCCT = 100

const automationSubscriptionSeconds = 30 * 24 * 3600 // 30 days

type hubAutomationSubscribeData struct{}

// HandleHubAutomationSubscribe charges the flat monthly fee from the
// hub's own CCT balance and extends its subscription to 30 days from
// now (block-timestamp "now" at apply time, not request time - see
// applyHubAutomationSubscribe).
func HandleHubAutomationSubscribe(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubAutomationSubscribeData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !companyStore.IsVerified(address) {
		http.Error(w, "only a verified Trading Hub can subscribe to Automation", http.StatusForbidden)
		return
	}
	if tokenStore.BalanceOf(address) < AutomationTierMonthlyFeeCCT {
		http.Error(w, "insufficient CCT balance for the Automation subscription fee", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxHubAutomationSubscribe, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

// HandleHubAutomationStatus is a public read - a hub's Automation
// subscription state is not sensitive, same as any other hub-facing
// lookup.
func HandleHubAutomationStatus(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	hub := r.URL.Query().Get("hub")
	sub, ok := hubAutomationStore.Get(hub)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{"active": false})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"active":      hubAutomationStore.IsActive(hub, time.Now().Unix()),
		"activeUntil": sub.ActiveUntil,
	})
}

type hubWorkerSetAutomationData struct {
	WorkerAddress        string `json:"workerAddress"`
	IsAutomationAgent    bool   `json:"isAutomationAgent"`
	AutoConfirmCapAmount uint64 `json:"autoConfirmCapAmount"`
}

// HandleSetWorkerAutomation lets a hub flag an EXISTING worker (see
// core.HubWorker, added via HandleAddHubWorker) as an automation agent
// and set its per-trade auto-confirm cap - a separate action from
// adding the worker in the first place, so the base
// pricing-delegation flow (HandleAddHubWorker/api/hub_workers.go)
// stays untouched by this feature.
func HandleSetWorkerAutomation(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubWorkerSetAutomationData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !companyStore.IsVerified(address) {
		http.Error(w, "only a verified Trading Hub can configure automation", http.StatusForbidden)
		return
	}
	if req.WorkerAddress == "" {
		http.Error(w, "workerAddress required", http.StatusBadRequest)
		return
	}
	if !hubAutomationStore.IsActive(address, time.Now().Unix()) {
		http.Error(w, "subscribe to the Automation tier first", http.StatusForbidden)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxHubWorkerSetAutomation, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

func applyHubAutomationSubscribe(tx core.Transaction) {
	if !companyStore.IsVerified(tx.From) {
		return
	}
	if !tokenStore.Transfer(tx.From, automationFeeSink(), AutomationTierMonthlyFeeCCT) {
		return
	}
	hubAutomationStore.Subscribe(tx.From, tx.Timestamp+automationSubscriptionSeconds)
}

// automationFeeSink swallows the monthly fee to a real address if
// configured, otherwise to a synthetic sink (same "FEE:*" escrow-
// address convention as every other fee in this codebase) so the CCT
// isn't silently destroyed even when no real operator address is set.
func automationFeeSink() string {
	if addr := automationTierFeeAddress(); addr != "" {
		return addr
	}
	return "FEE:AUTOMATION_TIER"
}

func applyHubWorkerSetAutomation(tx core.Transaction) {
	var data hubWorkerSetAutomationData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if !companyStore.IsVerified(tx.From) {
		return
	}
	if !hubAutomationStore.IsActive(tx.From, tx.Timestamp) {
		return
	}
	if !hubWorkerStore.IsActiveWorker(tx.From, data.WorkerAddress) {
		return
	}

	for _, w := range hubWorkerStore.ListForHub(tx.From) {
		if w.WorkerAddress == data.WorkerAddress && w.Active {
			w.IsAutomationAgent = data.IsAutomationAgent
			w.AutoConfirmCapAmount = data.AutoConfirmCapAmount
			return
		}
	}
}
