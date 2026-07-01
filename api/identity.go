package api

import "net/http"

func HandleIdentity(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}