package api

import "net/http"

func HandleBridge(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}