package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// HandleHDWalletMigrate is signed by the OLD keypair (still controlled
// by the caller - unlike HandleRecoverWallet, which exists for when
// that key is lost) to move a pre-Multi-Chain-HD-Wallet legacy address
// onto a newly-derived HD address. See core/transactions.go's
// TxHDWalletMigrate doc comment and api/apply.go's applyHDWalletMigrate
// for the actual asset movement.
func HandleHDWalletMigrate(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hdWalletMigrateData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	newPub, err := hex.DecodeString(req.NewPublicKey)
	if err != nil || core.GenerateAddress(newPub) != req.NewAddress {
		http.Error(w, "newAddress does not match newPublicKey", http.StatusBadRequest)
		return
	}

	if recoveryStore.IsMigrated(address) {
		http.Error(w, "this address has already been migrated", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, req.NewAddress, 0, core.TxHDWalletMigrate, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}
