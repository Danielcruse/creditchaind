package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var hubWorkerStore = core.NewHubWorkerStore()

type hubWorkerAddData struct {
	WorkerAddress   string `json:"workerAddress"`
	WorkerPublicKey string `json:"workerPublicKey"`
	Name            string `json:"name"`
}

// HandleAddHubWorker lets a verified Trading Hub delegate day-to-day
// pricing to another wallet - see core.HubWorker's doc comment. Only
// the hub's own signature can call this; a worker can never grant
// itself (or anyone else) worker status.
func HandleAddHubWorker(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubWorkerAddData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !companyStore.IsVerified(address) {
		http.Error(w, "only a verified Trading Hub can add workers", http.StatusForbidden)
		return
	}
	if req.WorkerAddress == "" || req.WorkerPublicKey == "" {
		http.Error(w, "workerAddress and workerPublicKey are required", http.StatusBadRequest)
		return
	}
	workerPubBytes, err := hex.DecodeString(req.WorkerPublicKey)
	if err != nil || core.GenerateAddress(workerPubBytes) != req.WorkerAddress {
		http.Error(w, "workerAddress does not match workerPublicKey", http.StatusBadRequest)
		return
	}
	if len(req.Name) > 100 {
		http.Error(w, "name must be 100 characters or fewer", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxHubWorkerAdd, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

type hubWorkerRemoveData struct {
	WorkerAddress string `json:"workerAddress"`
}

// HandleRemoveHubWorker lets a hub revoke a worker's ability to post
// rates on its behalf - the worker's own past rate posts stay exactly
// as they were (see core.HubRate's doc comment on PostedBy), only
// future ones are blocked.
func HandleRemoveHubWorker(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubWorkerRemoveData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !companyStore.IsVerified(address) {
		http.Error(w, "only a verified Trading Hub can remove workers", http.StatusForbidden)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxHubWorkerRemove, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

// HandleListHubWorkers is a public read of a hub's team, same as
// every other hub-facing lookup.
func HandleListHubWorkers(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	hub := r.URL.Query().Get("hub")
	json.NewEncoder(w).Encode(hubWorkerStore.ListForHub(hub))
}
