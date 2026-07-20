package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// Package doc: the staged, harder-to-reach validator-onboarding
// pipeline. Literal cryptographic proof that a wallet "owns a
// corresponding data machine with storage" is not something a website
// can verify from the browser side - any claim the client makes about
// its own hardware is self-reported and spoofable. This codebase
// already has the honest, real equivalent: core.NodeIdentity +
// AuthorizedNodeID binding (core/node_identity.go, api/peer.go's
// verifyBlockProducer) already requires a real, independently-running
// creditchaind process with its own Ed25519 node key that must
// successfully sign and have its blocks accepted by peers - a website
// click alone cannot forge this. This pipeline makes clearing that
// existing mechanism (via the unmodified /validators/register flow)
// a precondition of approval, not something checked only after the
// fact.
//
// Stage 1: any activated-governance wallet can support an open
// application; once SupportWeight crosses 1% of governanceTotalWeight,
// the application advances to PENDING_NODE_PROOF and an email link is
// sent to the applicant's on-file recovery address.
// Stage 2: the applicant signs a confirm request carrying the emailed
// token, proving both wallet control and receipt of the email: this
// records TxValidatorApplicationConfirm, which only succeeds if the
// applicant has ALSO already completed real node-binding (the
// existing, unmodified /validators/register flow, itself gated on the
// stepped fee/stake table below) and separately locked stake.
// Stage 3: validatorRegisterFeeForCount/minValidatorStakeForCount
// (api/validators.go) apply the actual per-validator-count-scaling
// requirement at that final registration step.

var validatorApplicationStore = core.NewValidatorApplicationStore()

// validatorApplicationSupportThresholdPercent is "gains at least 1
// percent trust of total blockchain or governance count" per the
// user's own wording - integer basis, not a specified model.
const validatorApplicationSupportThresholdPercent = 1

const validatorApplicationTokenValiditySeconds = 7 * 24 * 3600 // 7 days

func newConfirmToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// HandleCreateValidatorApplication opens a fresh application for the
// caller - no request body needed, the caller's own signed identity
// is the applicant.
func HandleCreateValidatorApplication(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req struct{}
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxValidatorApplicationCreate, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

type validatorApplicationSupportData struct {
	Address string `json:"address"`
}

// HandleSupportValidatorApplication lets any activated-governance
// wallet add its own real voting weight in support of an open
// application - one support per wallet per application (see
// core.ValidatorApplicationStore.Support).
func HandleSupportValidatorApplication(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req validatorApplicationSupportData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if req.Address == "" {
		http.Error(w, "address required", http.StatusBadRequest)
		return
	}
	if walletStore.Get(address).VotingPower == 0 {
		http.Error(w, "activate governance before supporting an application", http.StatusForbidden)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxValidatorApplicationSupport, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

type validatorApplicationConfirmData struct {
	Token string `json:"token"`
}

// HandleConfirmValidatorApplication is what the emailed link's page
// submits - the applicant signs a request carrying the token from
// their email, proving both wallet control and email receipt at once.
func HandleConfirmValidatorApplication(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req validatorApplicationConfirmData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if req.Token == "" {
		http.Error(w, "token required", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxValidatorApplicationConfirm, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

// HandleGetValidatorApplication is a public read - an application's
// status/support level carries no value to protect.
func HandleGetValidatorApplication(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "address required", http.StatusBadRequest)
		return
	}

	app, ok := validatorApplicationStore.Get(address)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{"exists": false})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"exists":         true,
		"status":         app.Status,
		"supportWeight":  app.SupportWeight,
		"requiredWeight": governanceTotalWeight() * validatorApplicationSupportThresholdPercent / 100,
	})
}

func applyValidatorApplicationCreate(tx core.Transaction) {
	validatorApplicationStore.Create(tx.From, tx.Timestamp)
}

func applyValidatorApplicationSupport(tx core.Transaction) {
	var data validatorApplicationSupportData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if walletStore.Get(tx.From).VotingPower == 0 {
		return
	}

	weight := votingWeight(tx.From)
	if _, counted := validatorApplicationStore.Support(data.Address, tx.From, weight); !counted {
		return
	}

	requiredWeight := int64(governanceTotalWeight() * validatorApplicationSupportThresholdPercent / 100)
	token := newConfirmToken()
	expiresAt := tx.Timestamp + validatorApplicationTokenValiditySeconds
	if !validatorApplicationStore.AdvanceToNodeProof(data.Address, token, expiresAt, requiredWeight) {
		return
	}

	// Best-effort email, same "purely informational, doesn't gate
	// anything by itself" reasoning as api/hub_rate.go's trade-alert
	// email - fires only if this node has live SMTP configured.
	// Disclosed limitation: this runs inside ApplyTransaction, which
	// every node in a multi-node deployment replays identically, so a
	// multi-node network sends this email once PER NODE that applies
	// the crossing block, not once total - same "node-local side
	// effect, not gossiped/deduped across nodes" caveat this project
	// already discloses for other apply-time notifications.
	if smtpConfigured() {
		if rec, ok := recoveryStore.Get(data.Address); ok && rec.EmailVerified && rec.Email != "" {
			link := fmt.Sprintf("validator-confirm.html?token=%s", token)
			go func(to, link string) {
				subject := "CreditChain: your validator application needs one more step"
				body := fmt.Sprintf(
					"Your validator application has reached the required community support.\n\n"+
						"To continue, open this link from the browser holding your CreditChain wallet, "+
						"complete node registration, and lock your stake:\n\n%s\n\n"+
						"This link expires in 7 days.", link)
				if err := sendEmail(to, subject, body); err != nil {
					fmt.Println("email: failed to notify validator applicant:", err)
				}
			}(rec.Email, link)
		}
	}
}

func applyValidatorApplicationConfirm(tx core.Transaction) {
	var data validatorApplicationConfirmData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	validatorApplicationStore.Approve(tx.From, data.Token, tx.Timestamp)
}
