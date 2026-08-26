package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var hubStorefrontStore = core.NewHubStorefrontStore()

type hubStorefrontSetData struct {
	HTML string `json:"html"`
}

// HandleSetHubStorefront lets a verified Trading Hub post/replace its
// own custom front-page design - see core.HubStorefront's doc comment
// for why this is rendered in a sandboxed iframe, never directly into
// the page.
func HandleSetHubStorefront(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubStorefrontSetData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !companyStore.IsVerified(address) {
		http.Error(w, "only a verified Trading Hub can set a storefront design", http.StatusForbidden)
		return
	}
	if len(req.HTML) > core.HubStorefrontMaxBytes {
		http.Error(w, fmt.Sprintf("storefront HTML must be %d bytes or fewer", core.HubStorefrontMaxBytes), http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxHubStorefrontSet, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

// HandleGetHubStorefront is a public read, same as every other
// hub-facing lookup - "" if the hub hasn't designed one yet.
func HandleGetHubStorefront(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	hub := r.URL.Query().Get("hub")
	sf, ok := hubStorefrontStore.Get(hub)
	if !ok {
		json.NewEncoder(w).Encode(map[string]string{"html": ""})
		return
	}

	json.NewEncoder(w).Encode(sf)
}
