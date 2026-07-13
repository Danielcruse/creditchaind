package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

var hubAgentApplicationStore = core.NewHubAgentApplicationStore()

func newHubAgentApplicationID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const maxAgentApplicationMessageLen = 500

type hubAgentApplyData struct {
	Hub     string `json:"hub"`
	Message string `json:"message"`
}

// HandleApplyHubAgent is the self-serve half core.HubWorker never had -
// any outernet wallet can apply to become one of a Trading Hub's
// agents/workers, subject to the hub's own MinReputationToApply gate
// (see core.HubRules) - "the hub owner sets the agent request rules."
// Applying costs nothing and grants nothing by itself; the hub still
// has to explicitly approve (see HandleApproveHubAgent).
func HandleApplyHubAgent(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubAgentApplyData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !companyStore.IsVerified(req.Hub) {
		http.Error(w, "not a verified Trading Hub", http.StatusBadRequest)
		return
	}
	if hubWorkerStore.IsActiveWorker(req.Hub, address) {
		http.Error(w, "already an active agent of this hub", http.StatusBadRequest)
		return
	}
	if hubAgentApplicationStore.HasPendingApplication(req.Hub, address) {
		http.Error(w, "you already have a pending application with this hub", http.StatusBadRequest)
		return
	}
	if len(req.Message) > maxAgentApplicationMessageLen {
		http.Error(w, "message must be 500 characters or fewer", http.StatusBadRequest)
		return
	}

	if rules, ok := hubRulesStore.Get(req.Hub); ok && rules.MinReputationToApply > 0 {
		if walletStore.Get(address).Reputation < rules.MinReputationToApply {
			http.Error(w, "your reputation is below this hub's minimum to apply as an agent", http.StatusForbidden)
			return
		}
	}

	hubAgentApplicationStore.Add(&core.HubAgentApplication{
		ID:                 newHubAgentApplicationID(),
		HubAddress:         req.Hub,
		ApplicantAddress:   address,
		ApplicantPublicKey: auth.PublicKey,
		Message:            req.Message,
		Status:             core.AgentApplicationPending,
		CreatedAt:          time.Now().Unix(),
	})

	json.NewEncoder(w).Encode(map[string]string{"status": "submitted"})
}

type hubAgentDecideData struct {
	ApplicationID string `json:"applicationId"`
	Name          string `json:"name"`
}

// HandleApproveHubAgent lets the hub itself approve a pending
// application - approval grants worker status through the exact same
// TxHubWorkerAdd path a manual invite already goes through (see
// api/hub_workers.go's HandleAddHubWorker), so the actual access grant
// stays behind that one already-audited, block-confirmed mechanism -
// this endpoint only decides which application triggers it.
func HandleApproveHubAgent(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubAgentDecideData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	app, ok := hubAgentApplicationStore.Get(req.ApplicationID)
	if !ok {
		http.Error(w, "application not found", http.StatusNotFound)
		return
	}
	if app.HubAddress != address {
		http.Error(w, "only the hub this application was submitted to can approve it", http.StatusForbidden)
		return
	}
	if !companyStore.IsVerified(address) {
		http.Error(w, "only a verified Trading Hub can approve agents", http.StatusForbidden)
		return
	}

	if _, ok := hubAgentApplicationStore.Decide(req.ApplicationID, true, time.Now().Unix()); !ok {
		http.Error(w, "application already decided", http.StatusBadRequest)
		return
	}

	addData, _ := json.Marshal(hubWorkerAddData{
		WorkerAddress:   app.ApplicantAddress,
		WorkerPublicKey: app.ApplicantPublicKey,
		Name:            req.Name,
	})
	tx := recordTx(auth, address, "", 0, core.TxHubWorkerAdd, string(addData))

	notifyUser(app.ApplicantAddress, "AGENT_APPROVED", "Your application to become a Trading Hub agent was approved", tx.ID)

	json.NewEncoder(w).Encode(map[string]string{"status": "approved", "txId": tx.ID})
}

// HandleRejectHubAgent lets the hub decline a pending application -
// no transaction needed, since nothing was ever granted.
func HandleRejectHubAgent(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubAgentDecideData

	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	app, ok := hubAgentApplicationStore.Get(req.ApplicationID)
	if !ok {
		http.Error(w, "application not found", http.StatusNotFound)
		return
	}
	if app.HubAddress != address {
		http.Error(w, "only the hub this application was submitted to can reject it", http.StatusForbidden)
		return
	}

	if _, ok := hubAgentApplicationStore.Decide(req.ApplicationID, false, time.Now().Unix()); !ok {
		http.Error(w, "application already decided", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "rejected"})
}

// HandleListHubAgentApplications is a public read of a hub's
// applications, same as every other hub-facing lookup (rates,
// workers) - meaningful in practice only to the hub reviewing its own.
func HandleListHubAgentApplications(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	hub := r.URL.Query().Get("hub")
	json.NewEncoder(w).Encode(hubAgentApplicationStore.ListForHub(hub))
}
