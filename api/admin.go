package api

import (
	"crypto/subtle"
	"net/http"
	"os"
)

// requireAdmin checks the X-Admin-Token header against ADMIN_TOKEN.
// If ADMIN_TOKEN isn't set at all, admin endpoints are disabled
// entirely (fail closed) rather than silently accepting any caller -
// an operator has to deliberately opt in to having a mint faucet at all.
func requireAdmin(r *http.Request) bool {
	expected := os.Getenv("ADMIN_TOKEN")
	if expected == "" {
		return false
	}

	got := r.Header.Get("X-Admin-Token")
	if got == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}
