package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"
)

// This file is a lightweight, node-local (not chain-state) queue for
// "I want to join your validator network" requests - see
// website/validator-setup.html's guided onboarding flow. Submitting a
// request does NOT hand out NETWORK_SECRET or add anyone as a peer -
// those stay manual, out-of-band operator actions (never transmitted
// through any system that logs or stores them, same rule this
// project has followed for every other secret all session) - this
// just gives the operator a real, persisted list of who's asking,
// instead of whatever ad-hoc channel they'd otherwise rely on (chat,
// email, or nothing at all).

type validatorJoinRequest struct {
	ID        string `json:"id"`
	Address   string `json:"address"`
	NodeURL   string `json:"nodeUrl"`
	Note      string `json:"note"`
	Status    string `json:"status"` // "PENDING" | "HANDLED"
	CreatedAt int64  `json:"createdAt"`
	HandledAt int64  `json:"handledAt,omitempty"`
}

var (
	validatorJoinRequestsMu sync.Mutex
	validatorJoinRequests   = map[string]*validatorJoinRequest{}
)

func validatorJoinRequestsPath() string {
	return dataPath("validator_join_requests.json")
}

func loadValidatorJoinRequests() {
	validatorJoinRequestsMu.Lock()
	defer validatorJoinRequestsMu.Unlock()

	raw, err := os.ReadFile(validatorJoinRequestsPath())
	if err != nil {
		return
	}
	var list []*validatorJoinRequest
	if err := json.Unmarshal(raw, &list); err != nil {
		return
	}
	for _, r := range list {
		validatorJoinRequests[r.ID] = r
	}
}

func saveValidatorJoinRequestsLocked() {
	list := make([]*validatorJoinRequest, 0, len(validatorJoinRequests))
	for _, r := range validatorJoinRequests {
		list = append(list, r)
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return
	}
	os.WriteFile(validatorJoinRequestsPath(), raw, 0o600)
}

func init() {
	loadValidatorJoinRequests()
}

func newValidatorJoinRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type validatorJoinRequestSubmitData struct {
	NodeURL string `json:"nodeUrl"`
	Note    string `json:"note"`
}

// HandleSubmitValidatorJoinRequest - any signed-in wallet can submit
// one; no admin gate needed since submitting your own request isn't a
// privileged action (mirrors api/hub_agents.go's
// HandleApplyHubAgent). Notifies the configured admin recipient by
// email if SMTP is set up (see api/smtp_admin_config.go) - best
// effort, never blocks or fails the submission if it's not configured
// or the send fails.
func HandleSubmitValidatorJoinRequest(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req validatorJoinRequestSubmitData
	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if req.NodeURL == "" {
		http.Error(w, "nodeUrl is required", http.StatusBadRequest)
		return
	}
	if len(req.NodeURL) > 300 || len(req.Note) > 1000 {
		http.Error(w, "nodeUrl/note too long", http.StatusBadRequest)
		return
	}

	entry := &validatorJoinRequest{
		ID:        newValidatorJoinRequestID(),
		Address:   address,
		NodeURL:   req.NodeURL,
		Note:      req.Note,
		Status:    "PENDING",
		CreatedAt: time.Now().Unix(),
	}

	validatorJoinRequestsMu.Lock()
	validatorJoinRequests[entry.ID] = entry
	saveValidatorJoinRequestsLocked()
	validatorJoinRequestsMu.Unlock()

	if smtpConfigured() {
		if to := smtpNotifyRecipient(); to != "" {
			go sendEmail(to, "New validator join request",
				"Wallet "+address+" wants to join as a validator.\nNode URL: "+req.NodeURL+"\nNote: "+req.Note)
		}
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "submitted", "id": entry.ID})
}

// HandleListValidatorJoinRequests is admin-gated - see
// api/admin.go's requireAdminAuthed.
func HandleListValidatorJoinRequests(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if _, _, err := requireAdminAuthed(r, nil); err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	validatorJoinRequestsMu.Lock()
	defer validatorJoinRequestsMu.Unlock()

	list := make([]*validatorJoinRequest, 0, len(validatorJoinRequests))
	for _, r := range validatorJoinRequests {
		list = append(list, r)
	}
	json.NewEncoder(w).Encode(list)
}

type validatorJoinRequestMarkHandledData struct {
	ID string `json:"id"`
}

// HandleMarkValidatorJoinRequestHandled is a direct, synchronous
// node-local mutation, not a blockchain transaction - same reasoning
// as api/smtp_admin_config.go's own handlers: this is purely
// operational bookkeeping, not something that needs cross-node
// consensus.
func HandleMarkValidatorJoinRequestHandled(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req validatorJoinRequestMarkHandledData
	if _, _, err := requireAdminAuthed(r, &req); err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	validatorJoinRequestsMu.Lock()
	entry, ok := validatorJoinRequests[req.ID]
	if ok && entry.Status != "HANDLED" {
		entry.Status = "HANDLED"
		entry.HandledAt = time.Now().Unix()
		saveValidatorJoinRequestsLocked()
	}
	validatorJoinRequestsMu.Unlock()

	if !ok {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "handled"})
}
