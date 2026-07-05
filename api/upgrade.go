package api

import "net/http"

func HandleUpgrade(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}