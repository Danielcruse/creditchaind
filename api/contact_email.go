package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/creditchain/creditchaind/core"
)

// This file is the fix for a real gap found in production: retiring
// wallet recovery (api/recovery.go's walletRecoveryDiscontinuedMessage)
// also retired HandleRequestEmailCode/HandleConfirmEmail, the ONLY
// path that ever set core.WalletRecovery.EmailVerified=true. Three
// separate, unrelated notification features already depend on that
// flag - api/validator_application.go's confirm-link email,
// api/p2p_trade_start_notify.go, api/p2p_dispute_notify.go - and none
// of them could have fired for any wallet created after the cutover,
// with no error surfaced anywhere: the notification code just silently
// found EmailVerified false and skipped. There was also no way to
// resend a code or change the email on file, since the entire request/
// confirm flow was gone, not just the final recovery step.
//
// Deliberately a NEW, separate transaction pair
// (TxRequestContactEmailCode/TxConfirmContactEmail, core/transactions.go)
// rather than reviving TxSetRecoveryEmail - that type's own apply
// function (applySetRecoveryEmail) has a hard cutover check baked in
// and stays that way, since wallet recovery itself is correctly still
// dead. This writes into the exact same core.WalletRecovery record
// (see SetPendingEmail/ConfirmPendingEmail, core/recovery.go) purely as
// a verified notification address - explicitly not a recovery
// credential, and nothing here reactivates HandleRecoverWallet or any
// other recovery capability.

const contactEmailCodeValiditySeconds = 15 * 60 // 15 minutes - short-lived, meant to be used interactively, not the 7-day link validator-application's own token uses

type requestContactEmailCodeData struct {
	Email string `json:"email"`
}

type confirmContactEmailData struct {
	Code string `json:"code"`
}

// HandleRequestContactEmailCode starts (or restarts - see
// SetPendingEmail's own doc comment) verification of a notification
// email. Re-callable at any time: this is both the "resend" and the
// "use a different email" action the missing UI needed - requesting
// again with the same email resends a fresh code, requesting with a
// different email switches what's being verified, and either way the
// previous pending code (if any) is simply overwritten and can no
// longer confirm anything.
func HandleRequestContactEmailCode(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req requestContactEmailCodeData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	email := strings.TrimSpace(req.Email)
	if email == "" || len(email) > 200 || !strings.Contains(email, "@") {
		http.Error(w, "a valid email is required", http.StatusBadRequest)
		return
	}
	if !smtpConfigured() {
		http.Error(w, "email is not configured on this server yet", http.StatusServiceUnavailable)
		return
	}

	data, _ := json.Marshal(requestContactEmailCodeData{Email: email})
	tx := recordTx(auth, address, "", 0, core.TxRequestContactEmailCode, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleConfirmContactEmail submits the code the user received by
// email, completing verification - see applyConfirmContactEmail
// (api/apply.go) for the actual check.
func HandleConfirmContactEmail(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req confirmContactEmailData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	code := strings.TrimSpace(req.Code)
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(confirmContactEmailData{Code: code})
	tx := recordTx(auth, address, "", 0, core.TxConfirmContactEmail, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleGetContactEmailStatus is a public, lightweight lookup - the
// settings page uses it to show whether the wallet already has a
// verified notification email, and what (if anything) is currently
// pending confirmation. Same "no confidentiality to protect" posture
// other self-service status endpoints in this codebase already take -
// only the wallet's own signed requests can ever change this state,
// so reading it back requires no signature either.
func HandleGetContactEmailStatus(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "missing address", http.StatusBadRequest)
		return
	}

	rec, ok := recoveryStore.Get(address)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{"verified": false, "email": "", "pending": false, "pendingEmail": ""})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"verified":     rec.EmailVerified,
		"email":        rec.Email,
		"pending":      rec.PendingEmailToken != "",
		"pendingEmail": rec.PendingEmail,
	})
}
