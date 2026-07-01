package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"

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