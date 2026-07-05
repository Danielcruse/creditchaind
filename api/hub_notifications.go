package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var notificationStore = core.NewNotificationStore()

// HandleListHubNotifications is a public, address-keyed read - same
// "public read, private only by obscurity of the address" model as
// every other lookup in this app (see core.NegotiationMessage's doc
// comment) - meaningful in practice only to the hub checking its own
// address.
func HandleListHubNotifications(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	hub := r.URL.Query().Get("hub")
	if hub == "" {
		http.Error(w, "missing hub", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"notifications": notificationStore.ListFor(hub),
		"unread":        notificationStore.UnreadCount(hub),
	})
}

type markNotificationReadData struct {
	NotificationID string `json:"notificationId"`
}

// HandleMarkNotificationRead requires a signed request so only the
// hub itself can clear its own notifications - this is the one
// mutating action here, everything else is a public read.
func HandleMarkNotificationRead(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req markNotificationReadData

	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !notificationStore.MarkRead(address, req.NotificationID) {
		http.Error(w, "notification not found or not addressed to you", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "read"})
}
