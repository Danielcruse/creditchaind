package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

func HandleCreateWallet(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	wallet,
	err :=
		core.CreateWallet()

	if err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	// Register a canonical core.Wallet (balances/reputation/etc.)
	// under this same address so the rest of the system - staking,
	// reputation, credit tier - has somewhere to persist state for
	// this address going forward.
	registered := core.NewWallet()
	registered.Address = wallet.Address
	registered.CreatedAt = time.Now().Unix()
	// Starts the dormancy clock (see core/dormancy.go) - wallet
	// creation isn't part of block-replay determinism (same reasoning
	// CreatedAt above already relies on), so wall-clock time here is
	// fine; every later signed action re-stamps this with the
	// transaction's own timestamp instead.
	registered.LastActivityAt = time.Now().Unix()
	walletStore.Register(&registered)

	json.NewEncoder(w).Encode(
		map[string]string{
			"address":
				wallet.Address,
			"publicKey":
				hex.EncodeToString(
					wallet.PublicKey,
				),
			"privateKey":
				hex.EncodeToString(
					wallet.PrivateKey,
				),
		},
	)
}