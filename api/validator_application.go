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
// application; once SupportWeight crosses validatorApplicationRequiredWeight
// (a stepped count, not a flat percentage - see that function's doc
// comment), the application advances to PENDING_NODE_PROOF and an
// email link is sent to the applicant's on-file recovery address.
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

const validatorApplicationTokenValiditySeconds = 7 * 24 * 3600 // 7 days

// validatorApplicationRequiredWeight is a stepped, count-based bar
// rather than a flat percentage of total governance weight - the
// user's explicit ask: the very first-ever applicant needs just one
// supporting vote (any activated wallet's weight, which floors at 1 -
// see votingWeight's doc comment), and each applicant after that needs
// one more than the last, keyed off how many have already been
// approved. A flat percentage of governanceTotalWeight() was either
// unreachable (near-zero activated governance weight early on) or
// meaninglessly easy later - this scales with real adoption instead,
// mirroring the same per-count-scaling precedent already used for
// validatorRegisterFeeForCount/minValidatorStakeForCount.
func validatorApplicationRequiredWeight() uint64 {
	return uint64(validatorApplicationStore.ApprovedCount()) + 1
}

// Reputation floor + per-wallet vote cap on supporting validator
// applications - the user's own explicit numbers ("if needed
// reputation is 30 a user with reputation up to 90 can make 3
// votes"). Read literally: 30 is the minimum Trust Score to support
// any application at all, and 3 is the maximum number of distinct
// applications one wallet can ever throw its support behind - a flat
// cap for every wallet clearing the 30 floor, not a second reputation-
// scaled tier, since only one bracket was actually specified. Flagged
// as a judgment call on the exact wording, not a guess at unspecified
// tiers above reputation 90.
const validatorApplicationMinReputationToVote = 30
const validatorApplicationBaseMaxVotesPerWallet = 3

// rcPerExtraVoteCapSlot/maxExtraVoteCapSlots let RC (REPT) balance
// raise a wallet's flat support-vote cap above the base 3 - a judgment
// call, not derived from any spec: +1 extra vote slot per 50 RC held,
// capped at +5 extra slots (ceiling of 8 total) so RC can meaningfully
// help but never remove the cap entirely.
const rcPerExtraVoteCapSlot = 50
const maxExtraVoteCapSlots = 5

// validatorApplicationVoteCapFor is the RC-scaled replacement for the
// old flat validatorApplicationMaxVotesPerWallet constant - every call
// site that used to read the constant directly now calls this instead.
func validatorApplicationVoteCapFor(address string) uint64 {
	extra := reptStore.BalanceOf(address) / rcPerExtraVoteCapSlot
	if extra > maxExtraVoteCapSlots {
		extra = maxExtraVoteCapSlots
	}
	return validatorApplicationBaseMaxVotesPerWallet + extra
}

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
	wallet := walletStore.Get(address)
	if wallet.VotingPower == 0 {
		http.Error(w, "activate governance before supporting an application", http.StatusForbidden)
		return
	}
	// Governance Trust specifically - supporting a validator application
	// is a governance act, same reasoning as votingWeight's own
	// redirect (api/governance_weight.go).
	governanceTrust := core.TrustDimensionValue(wallet, core.DimensionGovernance)
	if governanceTrust < validatorApplicationMinReputationToVote {
		http.Error(w, fmt.Sprintf("reputation %d is below the minimum (%d) to support a validator application", governanceTrust, validatorApplicationMinReputationToVote), http.StatusForbidden)
		return
	}
	voteCap := validatorApplicationVoteCapFor(address)
	if uint64(validatorApplicationStore.SupportCountFor(address)) >= voteCap {
		http.Error(w, fmt.Sprintf("you've already used all %d of your validator-application votes", voteCap), http.StatusForbidden)
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
		"requiredWeight": validatorApplicationRequiredWeight(),
	})
}

// HandleListValidatorApplications is the "intending validators" list
// governance.html's new tab reads - every OPEN application, so anyone
// can see who's asking for community support and vote for them. A
// public read, same "an application's support level carries no value
// to protect" reasoning as HandleGetValidatorApplication above.
func HandleListValidatorApplications(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	required := validatorApplicationRequiredWeight()
	open := validatorApplicationStore.ListOpen()
	out := make([]map[string]any, 0, len(open))
	for _, a := range open {
		out = append(out, map[string]any{
			"address":        a.Address,
			"reputation":     walletStore.Get(a.Address).Reputation,
			"supportWeight":  a.SupportWeight,
			"requiredWeight": required,
			"createdAt":      a.CreatedAt,
		})
	}
	json.NewEncoder(w).Encode(out)
}

// HandleMyValidatorVotes tells the caller's own eligibility/remaining
// votes for the intending-validators list - purely informational, so
// the frontend can show "2 of 3 votes remaining" or explain why the
// Support button is disabled, rather than the user finding out only
// after a rejected POST.
func HandleMyValidatorVotes(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "address required", http.StatusBadRequest)
		return
	}

	wallet := walletStore.Get(address)
	votesUsed := uint64(validatorApplicationStore.SupportCountFor(address))
	voteCap := validatorApplicationVoteCapFor(address)
	// voteCap can, in principle, fall (e.g. RC spent via promote/infuse
	// after votes were already cast under a higher cap) - clamp rather
	// than underflow the unsigned subtraction below.
	var votesRemaining uint64
	if voteCap > votesUsed {
		votesRemaining = voteCap - votesUsed
	}
	governanceTrust := core.TrustDimensionValue(wallet, core.DimensionGovernance)
	json.NewEncoder(w).Encode(map[string]any{
		"reputation":       governanceTrust,
		"minReputation":    validatorApplicationMinReputationToVote,
		"eligible":         wallet.VotingPower != 0 && governanceTrust >= validatorApplicationMinReputationToVote,
		"votesUsed":        votesUsed,
		"maxVotes":         voteCap,
		"votesRemaining":   votesRemaining,
		"governanceActive": wallet.VotingPower != 0,
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
	wallet := walletStore.Get(tx.From)
	if wallet.VotingPower == 0 {
		return
	}
	if core.TrustDimensionValue(wallet, core.DimensionGovernance) < validatorApplicationMinReputationToVote {
		return
	}
	if uint64(validatorApplicationStore.SupportCountFor(tx.From)) >= validatorApplicationVoteCapFor(tx.From) {
		return
	}

	weight := votingWeight(tx.From)
	if _, counted := validatorApplicationStore.Support(data.Address, tx.From, weight); !counted {
		return
	}

	requiredWeight := int64(validatorApplicationRequiredWeight())
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
