package api

import "net/http"

func HandleDefaults(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}