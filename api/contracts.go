package api

import "net/http"

func HandleContracts(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}