package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

var userNotificationStore = core.NewUserNotificationStore()

func newUserNotificationID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// notifyUser is the one call site every notification-triggering event
// in this package goes through - keeps ID generation/CreatedAt
// consistent instead of every call site rolling its own.
func notifyUser(address, notifType, message, relatedID string) {
	userNotificationStore.Add(&core.UserNotification{
		ID:        newUserNotificationID(),
		Address:   address,
		Type:      notifType,
		Message:   message,
		RelatedID: relatedID,
		CreatedAt: time.Now().Unix(),
	})
}

// HandleListUserNotifications is a public, address-keyed read - same
// "public read, private only by obscurity of the address" model as
// HandleListHubNotifications - meaningful in practice only to the
// wallet checking its own address.
func HandleListUserNotifications(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "missing address", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"notifications": userNotificationStore.ListFor(address),
		"unread":        userNotificationStore.UnreadCount(address),
	})
}

type markUserNotificationReadData struct {
	NotificationID string `json:"notificationId"`
}

// HandleMarkUserNotificationRead requires a signed request so only the
// wallet itself can clear its own notifications.
func HandleMarkUserNotificationRead(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req markUserNotificationReadData

	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !userNotificationStore.MarkRead(address, req.NotificationID) {
		http.Error(w, "notification not found or not addressed to you", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "read"})
}

// HandleLoginPing records a "new login activity" notification for the
// caller's own wallet - called once per browser session from the
// frontend right after a wallet is unlocked/loaded, not on every page
// view. Signed so nobody can spam a login notification onto a wallet
// they don't hold the key for.
func HandleLoginPing(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req struct{}

	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	notifyUser(address, "LOGIN", "New login activity on your wallet", "")

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
