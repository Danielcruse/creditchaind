package api

import (
	"encoding/json"
	"net/http"
)

func HandlePeers(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	json.NewEncoder(w).Encode(
		peerStore.List(),
	)
}