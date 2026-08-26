package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"
)

// This file lets a trader submit a phone number + government ID to a
// specific Trading Hub, so hubs operating under jurisdictions that
// require it (KYC/AML) can meet that obligation. Deliberately NOT
// written as a blockchain transaction like the platform-wide
// self-attested KYC in api/kyc.go - phone numbers and government ID
// numbers are exactly the PII data-protection law wants access-
// controlled and deletable, and this codebase's replicated chain
// state is neither (every confirmed tx is gossiped to every node and
// kept forever). Instead this is node-local storage (see
// api/datadir.go's dataPath, the same pattern api/smtp_admin_config.go
// already uses for a different category of sensitive data),
// AES-256-GCM encrypted at rest, and readable through the API ONLY by
// a signed request from the exact hub address the record was
// submitted to - matching HandleApproveHubAgent's own "only the hub
// this was submitted to" ownership check (api/hub_agents.go).
//
// The at-rest encryption defends against the data leaking if the
// underlying disk/backup is exposed - it is NOT end-to-end encryption
// the operator of this node is locked out of; whoever holds
// HUB_KYC_ENCRYPTION_KEY_HEX (the node operator) can technically
// decrypt any hub's records. API-level access control (the signature
// check in HandleListHubKYC) is what actually keeps one hub from
// reading another's - same trust model as every other signature-gated
// endpoint in this codebase.

type hubKYCRecord struct {
	TraderAddress string `json:"traderAddress"`
	Phone         string `json:"phone"`
	IDType        string `json:"idType"`
	IDNumber      string `json:"idNumber"`
	SubmittedAt   int64  `json:"submittedAt"`
}

func hubKYCPath(hub string) string {
	return dataPath("hub_kyc_" + hub + ".json.enc")
}

func hubKYCEncryptionKey() ([]byte, error) {
	hexKey := os.Getenv("HUB_KYC_ENCRYPTION_KEY_HEX")
	if hexKey == "" {
		return nil, errors.New("HUB_KYC_ENCRYPTION_KEY_HEX not configured on this node")
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("HUB_KYC_ENCRYPTION_KEY_HEX must be 64 hex characters (32 bytes)")
	}
	return key, nil
}

func encryptHubKYC(plaintext []byte) ([]byte, error) {
	key, err := hubKYCEncryptionKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decryptHubKYC(ciphertext []byte) ([]byte, error) {
	key, err := hubKYCEncryptionKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("corrupt hub KYC file")
	}
	nonce, sealed := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, sealed, nil)
}

func loadHubKYCRecords(hub string) ([]hubKYCRecord, error) {
	raw, err := os.ReadFile(hubKYCPath(hub))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	plaintext, err := decryptHubKYC(raw)
	if err != nil {
		return nil, err
	}
	var records []hubKYCRecord
	if err := json.Unmarshal(plaintext, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func saveHubKYCRecords(hub string, records []hubKYCRecord) error {
	plaintext, err := json.Marshal(records)
	if err != nil {
		return err
	}
	ciphertext, err := encryptHubKYC(plaintext)
	if err != nil {
		return err
	}
	return os.WriteFile(hubKYCPath(hub), ciphertext, 0o600)
}

// hasSubmittedHubKYC reports whether a trader has an on-file phone/ID
// record with a specific hub - shared by HandleHubKYCStatus and, more
// importantly, HandleRequestHubTrade's RequireKYC enforcement gate
// (api/hub_rate.go). A read error (corrupt/misconfigured storage)
// fails closed (false), same reasoning as every other security check
// in this codebase preferring "block the action" over "silently allow
// it" when something unexpected happens.
func hasSubmittedHubKYC(hub, traderAddress string) bool {
	records, err := loadHubKYCRecords(hub)
	if err != nil {
		return false
	}
	for _, rec := range records {
		if rec.TraderAddress == traderAddress {
			return true
		}
	}
	return false
}

type hubKYCSubmitData struct {
	Hub      string `json:"hub"`
	Phone    string `json:"phone"`
	IDType   string `json:"idType"`
	IDNumber string `json:"idNumber"`
}

// HandleSubmitHubKYC lets any wallet submit their own phone/ID to a
// specific verified Trading Hub - authenticated by the TRADER's own
// signature (so the record is genuinely tied to the wallet that sent
// it, not spoofable), not gated behind hub approval since submitting
// your own information isn't a privileged action. Replaces any prior
// submission from the same trader to the same hub rather than
// accumulating duplicates.
func HandleSubmitHubKYC(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubKYCSubmitData
	_, traderAddress, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !companyStore.IsVerified(req.Hub) {
		http.Error(w, "not a verified Trading Hub", http.StatusBadRequest)
		return
	}
	if req.Phone == "" || req.IDType == "" || req.IDNumber == "" {
		http.Error(w, "phone, idType, and idNumber are all required", http.StatusBadRequest)
		return
	}

	if _, err := hubKYCEncryptionKey(); err != nil {
		http.Error(w, "hub KYC storage is not configured on this node yet - contact the network operator", http.StatusServiceUnavailable)
		return
	}

	records, err := loadHubKYCRecords(req.Hub)
	if err != nil {
		http.Error(w, "failed to read existing records: "+err.Error(), http.StatusInternalServerError)
		return
	}

	replaced := false
	for i := range records {
		if records[i].TraderAddress == traderAddress {
			records[i] = hubKYCRecord{TraderAddress: traderAddress, Phone: req.Phone, IDType: req.IDType, IDNumber: req.IDNumber, SubmittedAt: time.Now().Unix()}
			replaced = true
			break
		}
	}
	if !replaced {
		records = append(records, hubKYCRecord{TraderAddress: traderAddress, Phone: req.Phone, IDType: req.IDType, IDNumber: req.IDNumber, SubmittedAt: time.Now().Unix()})
	}

	if err := saveHubKYCRecords(req.Hub, records); err != nil {
		http.Error(w, "failed to save: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "submitted"})
}

type hubKYCHubOnlyData struct {
	Hub string `json:"hub"`
}

// HandleListHubKYC returns every trader's submitted phone/ID for one
// hub - gated so ONLY a signed request from that exact hub address can
// read them back, same ownership check as HandleApproveHubAgent
// (api/hub_agents.go): "only the hub this was submitted to" may ever
// see it.
func HandleListHubKYC(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubKYCHubOnlyData
	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if req.Hub != address {
		http.Error(w, "only the hub these records were submitted to can view them", http.StatusForbidden)
		return
	}

	records, err := loadHubKYCRecords(req.Hub)
	if err != nil {
		http.Error(w, "failed to read records: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(records)
}

// HandleHubKYCStatus lets a trader check whether they personally have
// already submitted KYC to a given hub, without exposing the hub's
// full list to them - used to render "already submitted" in the
// trade flow instead of re-showing the form every time.
func HandleHubKYCStatus(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req hubKYCHubOnlyData
	_, traderAddress, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"submitted": hasSubmittedHubKYC(req.Hub, traderAddress)})
}
