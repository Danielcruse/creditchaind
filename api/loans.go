package api

import (
	"encoding/json"
	"net/http"
)

func HandleGetLoans(
	w http.ResponseWriter,
	r *http.Request,
) {
	json.NewEncoder(w).Encode(
		loanStore.Loans,
	)
}