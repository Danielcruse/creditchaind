package api

import "net/http"

func HandleStablecoin(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}