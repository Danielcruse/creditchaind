package api

import "net/http"

func HandleDefaultsAdmin(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}