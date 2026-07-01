package api

import "net/http"

func HandleTreasury(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}