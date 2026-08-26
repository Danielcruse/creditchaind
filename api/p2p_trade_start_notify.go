package api

import (
	"fmt"

	"github.com/creditchain/creditchaind/core"
)

// p2p_trade_start_notify.go - best-effort email notification for the
// moment an EXTERNAL-payment P2P trade actually starts
// (applyP2PExternalAccept / core.P2POfferStore.BeginExternalSettlement) -
// both the acceptor (the customer) and the hub should know a real
// off-app payment window has just opened, without needing the app open
// to notice. Same trust model as every other email in this codebase
// (see api/p2p_dispute_notify.go's doc comment): never blocks or fails
// the underlying state transition, silently does nothing if SMTP isn't
// configured or a recipient has no verified email on file. ONCHAIN-mode
// offers (core.PaymentModeOnChain) settle atomically in one transaction -
// there's no meaningful "started, now in progress" moment for those the
// way there is for an EXTERNAL trade's off-app payment window, so this
// deliberately only fires for EXTERNAL trades.

func notifyTradeStarted(offer *core.P2POffer) {
	if !smtpConfigured() || offer == nil {
		return
	}
	subject := "CreditChain: your trade has started"
	body := fmt.Sprintf(
		"Trade %s has started - %d %s for %d %s.\n\n"+
			"Log in to your CreditChain dashboard to open the trade chat, see payment instructions, and track the payment countdown.",
		offer.ID, offer.OfferAmount, offer.OfferSymbol, offer.WantAmount, offer.WantSymbol,
	)
	emailOfferAcceptor(offer, subject, body)
	emailOfferHub(offer, subject, body)
}

// emailOfferAcceptor looks up the trade's acceptor (the customer who
// just started this settlement) the same way emailOfferModerator
// (api/p2p_dispute_notify.go) looks up a moderator - core.RecoveryStore,
// opt-in and only present once the wallet owner has verified an email
// against their own address.
func emailOfferAcceptor(offer *core.P2POffer, subject, body string) {
	if offer.AcceptedBy == "" {
		return
	}
	rec, ok := recoveryStore.Get(offer.AcceptedBy)
	if !ok || !rec.EmailVerified || rec.Email == "" {
		return
	}
	go func(to string) {
		if err := sendEmail(to, subject, body); err != nil {
			fmt.Println("email: failed to notify acceptor of trade start:", err)
		}
	}(rec.Email)
}
