package api

import (
	"encoding/json"
	"net/http"
	"os"
)

// This file lets an admin configure outbound SMTP from admin.html
// instead of editing the server's environment directly over SSH -
// requested specifically to avoid the terminal/SSH friction of the
// EnvironmentFile approach api/email.go's own doc comment describes.
// Deliberately NOT stored in the replicated blockchain state
// (governanceParamsStore, treasuryStore, etc.) - unlike those, this is
// a live credential, and chain state gets gossiped to every peer and
// swept into backups (see ops/creditchain-backup.env.example) - the
// wrong place for a password to live. Instead this writes a small,
// node-local JSON file via dataPath (api/datadir.go), the same
// "local-only, not chain state" storage certificates already use
// (applyCompanyUploadCertificate), root-restricted the same way every
// other credential file this deployment uses already is.

type smtpAdminConfig struct {
	Host            string `json:"host"`
	Port            string `json:"port"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	From            string `json:"from"`
	NotifyRecipient string `json:"notifyRecipient"`
}

func smtpAdminConfigPath() string {
	return dataPath("smtp_config.json")
}

// loadSmtpAdminConfig reads the admin-submitted config, if any - a
// missing file is not an error (matches "not configured yet"), but a
// present-and-unreadable/malformed one fails closed (ok=false) rather
// than risk sending mail with a half-parsed credential.
func loadSmtpAdminConfig() (smtpAdminConfig, bool) {
	raw, err := os.ReadFile(smtpAdminConfigPath())
	if err != nil {
		return smtpAdminConfig{}, false
	}
	var cfg smtpAdminConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return smtpAdminConfig{}, false
	}
	if cfg.Host == "" || cfg.Username == "" || cfg.Password == "" || cfg.From == "" {
		return smtpAdminConfig{}, false
	}
	return cfg, true
}

// saveSmtpAdminConfig writes with 0600 from the start (not chmod'd
// after the fact) - there is no window where the file is briefly
// world-readable between creation and permission-tightening.
func saveSmtpAdminConfig(cfg smtpAdminConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(smtpAdminConfigPath(), raw, 0o600)
}

// HandleAdminSetSmtpConfig is a direct, synchronous config write, not
// a blockchain transaction - same reasoning as
// HandleSweepVaultReleaseSigningKeyEth's own doc comment: this is
// node-local operational config, not something that needs cross-node
// consensus agreement (every node sends its own mail, if it sends any
// at all - see smtpConfigured's doc comment on why even the existing
// env-var path never assumed a single shared mail identity).
func HandleAdminSetSmtpConfig(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req smtpAdminConfig
	if _, _, err := requireAdminAuthed(r, &req); err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if req.Host == "" || req.Username == "" || req.Password == "" || req.From == "" {
		http.Error(w, "host, username, password, and from are all required", http.StatusBadRequest)
		return
	}
	if req.Port == "" {
		req.Port = "587"
	}

	if err := saveSmtpAdminConfig(req); err != nil {
		http.Error(w, "failed to save: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}

// HandleAdminGetSmtpStatus never returns the password - only whether a
// config exists and its non-secret fields, so the admin page can show
// "configured, sending as X" without ever re-displaying the credential
// itself (same "write-only" convention every other secret in this
// codebase already follows - nothing that accepts a private key/
// password ever has a corresponding "read it back" endpoint).
func HandleAdminGetSmtpStatus(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if _, _, err := requireAdminAuthed(r, nil); err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	cfg, ok := loadSmtpAdminConfig()
	envConfigured := smtpConfiguredViaEnv()

	json.NewEncoder(w).Encode(map[string]any{
		"adminConfigured": ok,
		"envConfigured":   envConfigured,
		"host":            cfg.Host,
		"port":            cfg.Port,
		"username":        cfg.Username,
		"from":            cfg.From,
		"notifyRecipient": cfg.NotifyRecipient,
	})
}

// smtpNotifyRecipient resolves the address that should receive
// operational alerts (new payment order, new hub application, etc.) -
// distinct from the "From" address mail is sent as. Falls back to the
// SMTP_NOTIFY_RECIPIENT env var when the admin hasn't set one via the
// admin page, then to the From address itself so notifications still
// land somewhere once SMTP is configured at all.
func smtpNotifyRecipient() string {
	if envRecipient := os.Getenv("SMTP_NOTIFY_RECIPIENT"); envRecipient != "" {
		return envRecipient
	}
	if cfg, ok := loadSmtpAdminConfig(); ok && cfg.NotifyRecipient != "" {
		return cfg.NotifyRecipient
	}
	_, _, _, _, from, _ := effectiveSmtpConfig()
	return from
}
