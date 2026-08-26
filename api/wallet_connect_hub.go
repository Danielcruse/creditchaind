package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

type walletConnectHubData struct {
	Hub string `json:"hub"`
}

// HandleConnectHub lets an already-existing wallet set
// core.Wallet.ConnectedHub after following a Trading Hub's shareable
// link - see core.TxWalletConnectHub's doc comment for why this is a
// signed transaction (a brand-new wallet sets this unauthenticated, at
// creation, in api/wallet_create.go instead).
func HandleConnectHub(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req walletConnectHubData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Hub == "" {
		http.Error(w, "missing hub", http.StatusBadRequest)
		return
	}
	if req.Hub == address {
		http.Error(w, "a wallet cannot connect to itself as a hub", http.StatusBadRequest)
		return
	}
	if !companyStore.IsVerified(req.Hub) {
		http.Error(w, "that address is not a verified Trading Hub", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxWalletConnectHub, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleGetConnectedHub is a public, lightweight lookup the P2P
// marketplace page uses to render its "continue trading with this hub"
// banner and sort - returns the wallet's ConnectedHub address (if any)
// plus that hub's display Name, so the frontend doesn't need a second
// round-trip to /companies just to show a name. GET /wallet/connected-hub?address=
func HandleGetConnectedHub(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "missing address", http.StatusBadRequest)
		return
	}

	wallet := walletStore.Get(address)
	if wallet.ConnectedHub == "" {
		json.NewEncoder(w).Encode(map[string]any{"connectedHub": ""})
		return
	}

	hubName := ""
	if app, ok := companyStore.Get(wallet.ConnectedHub); ok {
		hubName = app.Name
	}

	json.NewEncoder(w).Encode(map[string]any{
		"connectedHub": wallet.ConnectedHub,
		"hubName":      hubName,
	})
}
