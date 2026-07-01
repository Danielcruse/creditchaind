package api

import "net/http"

func HandleAudit(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}