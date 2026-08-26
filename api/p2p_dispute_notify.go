package api

import (
	"fmt"

	"github.com/creditchain/creditchaind/core"
)

// p2p_dispute_notify.go - best-effort email notifications for the
// EXTERNAL-payment P2P dispute flow (core/p2p_offer.go's
// SettlementStatus state machine). Two events, two recipients each:
//
//   - A dispute is OPENED (applyP2PSellerDispute/applyP2PBuyerDispute) -
//     the assigned moderator needs to know there's a case waiting on
//     them, and the hub the trade happened under should know one of
//     its trades is disputed.
//   - A dispute is RESOLVED (applyP2PModeratorResolve) - a receipt
//     confirming the moderator's own decision was recorded, and the
//     same notice to the hub.
//
// Same trust model as every other email in this codebase (see
// api/validator_join_request.go, api/hub_rate.go): never blocks or
// fails the underlying state transition, silently does nothing if SMTP
// isn't configured or the recipient has no email on file.

func notifyDisputeOpened(offer *core.P2POffer, disputedBy string) {
	if !smtpConfigured() || offer == nil {
		return
	}
	subject := "CreditChain: a dispute needs your attention"
	body := fmt.Sprintf(
		"A dispute was just opened on P2P trade %s by %s.\n\n"+
			"Log in to your CreditChain dashboard to review the chat history and resolve it.",
		offer.ID, disputedBy,
	)
	emailOfferModerator(offer, subject, body)
	emailOfferHub(offer, subject, body)
}

func notifyDisputeResolved(offer *core.P2POffer, release bool) {
	if !smtpConfigured() || offer == nil {
		return
	}
	outcome := "refunded back to the seller"
	if release {
		outcome = "released to the buyer"
	}
	subject := "CreditChain: dispute resolved"
	body := fmt.Sprintf(
		"The dispute on P2P trade %s has been resolved - escrow was %s.",
		offer.ID, outcome,
	)
	emailOfferModerator(offer, subject, body)
	emailOfferHub(offer, subject, body)
}

// emailOfferModerator looks the moderator up the same way every other
// per-address email in this codebase does - core.RecoveryStore, opt-in
// and only present once the wallet owner has verified an email against
// their own address (see api/validator_application.go's identical
// pattern). Silently does nothing if the moderator never set one.
func emailOfferModerator(offer *core.P2POffer, subject, body string) {
	if offer.ModeratorAddress == "" {
		return
	}
	rec, ok := recoveryStore.Get(offer.ModeratorAddress)
	if !ok || !rec.EmailVerified || rec.Email == "" {
		return
	}
	go func(to string) {
		if err := sendEmail(to, subject, body); err != nil {
			fmt.Println("email: failed to notify moderator of dispute event:", err)
		}
	}(rec.Email)
}

// emailOfferHub resolves the trade's hub the same way HubAddress's own
// doc comment describes: HubAddress itself when set (trade against an
// operator/worker), else Creator (Creator IS the hub - every EXTERNAL
// offer's Creator is already a verified company/hub, see
// applyP2POfferCreate's companyStore.IsVerified gate).
func emailOfferHub(offer *core.P2POffer, subject, body string) {
	hubAddr := offer.HubAddress
	if hubAddr == "" {
		hubAddr = offer.Creator
	}
	app, ok := companyStore.Get(hubAddr)
	if !ok || app.ContactEmail == "" {
		return
	}
	go func(to string) {
		if err := sendEmail(to, subject, body); err != nil {
			fmt.Println("email: failed to notify hub of dispute event:", err)
		}
	}(app.ContactEmail)
}
