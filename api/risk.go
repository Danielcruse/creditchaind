package api

import "net/http"

func HandleRisk(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}