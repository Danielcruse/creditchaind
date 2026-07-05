package api

import "net/http"

func HandleYield(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}