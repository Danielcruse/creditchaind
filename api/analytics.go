package api

import "net/http"

func HandleAnalytics(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}