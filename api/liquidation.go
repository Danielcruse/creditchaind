package api

import "net/http"

func HandleLiquidations(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)
}