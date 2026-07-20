package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var announcementStore = core.NewAnnouncementStore()

const (
	maxAnnouncementTitleLen = 200
	maxAnnouncementTextLen  = 2000
)

type announcementCreateData struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// HandleCreateAnnouncement posts a site-wide, admin-authored
// announcement - gated via requireAdminAuthed (api/admin.go), same as
// every other admin.html control (Treasury/Sale). Routed through
// recordTx/ApplyTransaction like every other admin action so every
// node shows the identical announcement, not just whichever node
// received this HTTP request.
func HandleCreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req announcementCreateData
	signedAuth, mode, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if req.Title == "" || len(req.Title) > maxAnnouncementTitleLen {
		http.Error(w, "title required and must be under 200 characters", http.StatusBadRequest)
		return
	}
	if req.Text == "" || len(req.Text) > maxAnnouncementTextLen {
		http.Error(w, "text required and must be under 2000 characters", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxAnnouncementCreate, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleListAnnouncements is public - no admin token needed, matches
// HandleGovernance's own auth (none). Every user should be able to
// read a site-wide announcement, only the operator can post one.
func HandleListAnnouncements(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	json.NewEncoder(w).Encode(announcementStore.Recent(20))
}

func applyAnnouncementCreate(tx core.Transaction) {
	var data announcementCreateData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	announcementStore.Add(&core.Announcement{
		ID:        tx.ID,
		Title:     data.Title,
		Text:      data.Text,
		CreatedAt: tx.Timestamp,
	})
}
