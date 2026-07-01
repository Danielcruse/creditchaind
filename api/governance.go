package api

import "net/http"

func HandleGovernance(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}