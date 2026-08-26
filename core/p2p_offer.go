package core

import "sync"

// autoReleaseAfterSeconds is how long the OTHER party gets to respond
// once one side has acted (buyer marks paid, or seller confirms
// receipt) before the trade resolves on its own in the buyer's favor
// - see StalledOfferOutcome's doc comment for why it's always the
// buyer, never the seller, that a stall defaults to.
const autoReleaseAfterSeconds = 3600

// StalledOfferOutcome is one offer SweepStalledSettlements decided to
// close - ReleaseToBuyer distinguishes the two ways that can happen:
// true means escrow goes to the buyer (either they claimed payment
// and the seller never responded, or the seller confirmed and the
// buyer never responded - either way, releasing to the buyer is the
// safe default, since a buyer sitting on a not-yet-released escrow
// they were always going to receive isn't a real risk to anyone).
// false means neither party acted at all, so escrow returns to
// whoever put it up (the seller).
type StalledOfferOutcome struct {
	Offer          *P2POffer
	ReleaseToBuyer bool
}

// SweepStalledSettlements finds every EXTERNAL offer whose settlement
// has stalled as of asOf and moves it to a terminal state - the
// trade-chat countdown timer's backend enforcement counterpart (the
// frontend timer alone was only ever cosmetic; nothing previously
// stopped a trade sitting open forever if a party went silent).
// windowMinutesFor is called per-offer so the caller can look up the
// right hub's own configured payment window (see
// core.HubRules.PaymentWindowFor) without this store needing to know
// about hubs at all. Three stall conditions, checked per offer:
//
//  1. AWAITING_PAYMENT, neither side has acted (BuyerMarkedPaidAt ==
//     0), and the hub's payment window has passed -> EXPIRED, refund
//     the seller (StalledOfferOutcome.ReleaseToBuyer = false).
//  2. AWAITING_PAYMENT, the buyer marked paid, and an hour has passed
//     with the seller still not having confirmed -> AUTO_RELEASED,
//     release to the buyer.
//  3. SELLER_CONFIRMED and an hour has passed with the buyer still
//     not having agreed or disputed -> AUTO_RELEASED, release to the
//     buyer.
//
// Returns the matched offers (already moved to their terminal status)
// so the caller can move escrow and notify both parties - mirrors
// StakingStore.ProcessMaturedUnbonds's find-then-let-the-caller-move-
// funds split.
func (s *P2POfferStore) SweepStalledSettlements(asOf int64, windowMinutesFor func(o *P2POffer) int) []StalledOfferOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()

	var stalled []StalledOfferOutcome
	for _, o := range s.Offers {
		switch o.SettlementStatus {
		case SettlementAwaitingPayment:
			if o.AwaitingPaymentSince == 0 {
				continue
			}
			if o.BuyerMarkedPaidAt > 0 {
				if asOf-o.BuyerMarkedPaidAt < autoReleaseAfterSeconds {
					continue
				}
				o.SettlementStatus = SettlementAutoReleased
				stalled = append(stalled, StalledOfferOutcome{Offer: o, ReleaseToBuyer: true})
				continue
			}
			minutes := windowMinutesFor(o)
			if o.AwaitingPaymentSince+int64(minutes)*60 >= asOf {
				continue
			}
			o.SettlementStatus = SettlementExpired
			stalled = append(stalled, StalledOfferOutcome{Offer: o, ReleaseToBuyer: false})

		case SettlementSellerConfirmed:
			if o.SellerConfirmedAt == 0 || asOf-o.SellerConfirmedAt < autoReleaseAfterSeconds {
				continue
			}
			o.SettlementStatus = SettlementAutoReleased
			stalled = append(stalled, StalledOfferOutcome{Offer: o, ReleaseToBuyer: true})
		}
	}
	return stalled
}

type OfferStatus string

const (
	OfferOpen      OfferStatus = "OPEN"
	OfferAccepted  OfferStatus = "ACCEPTED"
	OfferCancelled OfferStatus = "CANCELLED"
)

// PaymentMode picks which of two entirely different settlement paths
// a P2P offer follows. ONCHAIN (the original, default behavior) is a
// real atomic swap - both sides move on-chain the instant accept
// confirms, nothing left to trust. EXTERNAL is for a trade where the
// "want" side is a real-world payment outside the app (e.g. bank
// transfer) that this chain has no way to observe - settlement
// instead runs through SellerConfirmedPaid, the buyer's agree/dispute
// answer, and (only if they disagree) the assigned moderator's final
// call. See SettlementStatus's doc comment for the full state
// machine, and core/moderator.go's doc comment for what a moderator's
// call is (a disclosed human trust decision) and isn't (a
// cryptographic verification that any payment really happened).
type PaymentMode string

const (
	PaymentModeOnChain  PaymentMode = "ONCHAIN"
	PaymentModeExternal PaymentMode = "EXTERNAL"
)

// SettlementStatus is EXTERNAL-mode-only state - "" for every ONCHAIN
// offer, since those settle atomically on accept with nothing left to
// track. The path:
//
//	AWAITING_PAYMENT  - buyer accepted, needs to pay the seller off-app.
//	                    The seller can dispute a payment CLAIM from here
//	                    directly (see DisputeAwaitingPayment) once the
//	                    buyer has marked paid, without waiting to
//	                    confirm receipt first.
//	CONFIRMED         - the seller confirmed receiving payment -> escrow
//	                    released to the buyer immediately, done (see
//	                    ConfirmSellerPaid's doc comment - this used to
//	                    require a separate buyer confirmation step via
//	                    SELLER_CONFIRMED below; it no longer does)
//	SELLER_CONFIRMED  - legacy transitional state, kept only for offers
//	                    already sitting here when this behavior changed
//	                    - no new offer reaches it (see ConfirmBuyer's
//	                    doc comment)
//	DISPUTED          - either the seller disputed a payment claim
//	                    (from AWAITING_PAYMENT) or, for a legacy
//	                    SELLER_CONFIRMED offer, the buyer disagreed ->
//	                    only the assigned moderator can resolve this
//	                    from here
//	RESOLVED_RELEASE  - moderator sided with the buyer -> escrow released
//	RESOLVED_REFUND   - moderator sided with the seller -> escrow returned
//	EXPIRED           - the hub's payment window ran out while still
//	                    AWAITING_PAYMENT with NEITHER party having
//	                    acted at all -> escrow returned to the seller,
//	                    same as RESOLVED_REFUND (see api/apply.go's
//	                    processStalledP2PSettlements)
//	AUTO_RELEASED     - one party acted (buyer marked paid, or seller
//	                    confirmed) and the OTHER party then went
//	                    silent for a full hour -> escrow released to
//	                    the buyer by default, same as CONFIRMED (see
//	                    processStalledP2PSettlements) - a stall in
//	                    either direction still has to resolve
//	                    eventually, not sit open forever
type SettlementStatus string

const (
	SettlementAwaitingPayment SettlementStatus = "AWAITING_PAYMENT"
	SettlementSellerConfirmed SettlementStatus = "SELLER_CONFIRMED"
	SettlementConfirmed       SettlementStatus = "CONFIRMED"
	SettlementDisputed        SettlementStatus = "DISPUTED"
	SettlementResolvedRelease SettlementStatus = "RESOLVED_RELEASE"
	SettlementResolvedRefund  SettlementStatus = "RESOLVED_REFUND"
	SettlementExpired         SettlementStatus = "EXPIRED"
	SettlementAutoReleased    SettlementStatus = "AUTO_RELEASED"
)

// P2POffer is a direct trader-to-trader trade at a FIXED, agreed
// price - the deliberate counterpart to the AMM pools (see
// liquidity_pool.go), where price instead floats with reserves and a
// large trade moves it against you. Here, whatever the creator locked
// up and whatever they asked for is exactly what both sides get,
// unaffected by anyone else's activity - the "agreed value" a P2P
// trade implies that an AMM swap can't promise.
type P2POffer struct {
	ID string

	Creator string
	// CreatorPublicKey is the creator's authenticated public key,
	// captured at creation time - needed later at accept/cancel time to
	// independently re-derive the creator's ETHTEST/USDCTEST
	// sub-address (see api/subaddress.go's resolveBalanceAddress),
	// since only the ORIGINAL creation transaction's AuthPublicKey ever
	// proved the creator's key ownership; the accept/cancel transaction
	// is signed by a different party (or carries no new proof of the
	// creator's identity at all).
	CreatorPublicKey string
	OfferSymbol      string
	OfferAmount      uint64

	// WantSymbol/WantAmount are a real, resolvable token/amount for
	// PaymentMode ONCHAIN. For EXTERNAL they're purely descriptive
	// (e.g. "USD", 50) - never validated as a real token, never moved
	// on chain.
	WantSymbol string
	WantAmount uint64

	PaymentMode PaymentMode

	Status     OfferStatus
	AcceptedBy string
	// AcceptedByPublicKey captures the buyer's authenticated public
	// key at accept time - needed later to resolve their ETHTEST/
	// USDCTEST sub-address (see api/subaddress.go's
	// resolveBalanceAddress) on a transaction the buyer themselves
	// didn't sign, e.g. the MODERATOR resolving a dispute in the
	// buyer's favor.
	AcceptedByPublicKey string

	// SettlementStatus/SellerConfirmedPaid/Messages are EXTERNAL-mode-
	// only - see SettlementStatus's doc comment for the state machine.
	SettlementStatus    SettlementStatus
	SellerConfirmedPaid bool

	// AwaitingPaymentSince is when settlement entered AWAITING_PAYMENT
	// (see BeginExternalSettlement) - the anchor the trade chat's
	// countdown timer counts down from, against whatever payment
	// window the hub on the other side of this trade has set (see
	// core.HubRules.PaymentWindowFor). 0 for ONCHAIN offers, which
	// never enter this state.
	AwaitingPaymentSince int64

	// BuyerMarkedPaidAt is the buyer's own, non-binding signal that
	// they've sent the off-app payment (see MarkBuyerPaid) - purely
	// informational for the seller's benefit while deciding whether to
	// confirm receipt. It does NOT move SettlementStatus forward: only
	// the seller's own ConfirmSellerPaid (an irreversible on-chain
	// claim) does that. 0 if the buyer hasn't marked it yet. Also the
	// anchor for the 1-hour auto-release timeout (see
	// api/apply.go's processStalledP2PSettlements) - if the seller
	// still hasn't confirmed an hour after the buyer claimed payment,
	// escrow auto-releases to the buyer rather than staying stuck
	// waiting on a seller who's gone silent.
	BuyerMarkedPaidAt int64

	// SellerConfirmedAt is when the seller confirmed receiving payment
	// (see ConfirmSellerPaid) - the anchor for ITS OWN 1-hour
	// auto-release timeout: if the buyer hasn't agreed or disputed an
	// hour after the seller's confirmation, escrow auto-releases to
	// the buyer (a silent buyer is treated the same as one who agreed,
	// since staying silent forever would otherwise leave the seller's
	// already-claimed-as-paid trade stuck with no way to close). 0
	// until ConfirmSellerPaid is called.
	SellerConfirmedAt int64

	// Messages is a chat thread between the creator, acceptor, and
	// assigned moderator for an EXTERNAL trade - same "world-readable,
	// not a private channel" model as NegotiationMessage (see
	// core/negotiation.go), and mutated directly outside ApplyBlock
	// for the same reason: it carries no economic value, unlike every
	// other field on this struct.
	Messages []NegotiationMessage

	CreatedAt int64

	// ModeratorAddress is assigned deterministically at creation time
	// (see api/apply.go's applyP2POfferCreate), from whichever
	// registered moderator is next in round-robin order - "" if no
	// moderator had registered yet when this offer was created. Same
	// disclosed attestation-layer model as Negotiation.ModeratorAddress
	// (core/negotiation.go) - moderation never gates this offer's own
	// atomic accept/cancel flow, which already settles funds on its own.
	ModeratorAddress string
	ModerationStatus ModerationStatus

	// HubAddress is which verified Trading Hub this trade happened
	// under - "" for an ordinary hub-to-hub-address trade (where
	// Creator already IS the hub, so it's redundant), set explicitly
	// for a trade against one of that hub's operators/workers (see
	// core.HubRate.OperatorAddress), where Creator is the OPERATOR's
	// own address rather than the hub's, so this is the only way to
	// still know "which verified hub was this actually under" for
	// display/filtering (e.g. the hub's own control panel listing
	// every trade any of its operators are party to).
	HubAddress string
}

type P2POfferStore struct {
	mu     sync.Mutex
	Offers map[string]*P2POffer
}

func NewP2POfferStore() *P2POfferStore {
	return &P2POfferStore{Offers: make(map[string]*P2POffer)}
}

func (s *P2POfferStore) Create(o *P2POffer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Offers[o.ID] = o
}

func (s *P2POfferStore) Get(id string) (*P2POffer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	return o, ok
}

// List returns every offer regardless of status - open, accepted, and
// cancelled - so a trade's outcome stays visible after the fact
// rather than disappearing the moment it's no longer actionable.
func (s *P2POfferStore) List() []*P2POffer {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]*P2POffer, 0, len(s.Offers))
	for _, o := range s.Offers {
		list = append(list, o)
	}
	return list
}

// Accept marks an OPEN offer ACCEPTED by acceptor, atomically with
// the check that it was still open - the actual fund movement is the
// caller's job (see api/apply.go's applyP2PAccept), this just owns
// the offer's own state transition so two simultaneous accept
// attempts can't both succeed.
func (s *P2POfferStore) Accept(id, acceptor string) (*P2POffer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok || o.Status != OfferOpen {
		return nil, false
	}

	o.Status = OfferAccepted
	o.AcceptedBy = acceptor
	return o, true
}

// Cancel marks an OPEN offer CANCELLED - only valid if it's still
// open and the caller confirms (see api/apply.go's applyP2PCancel)
// the requester is actually the creator before calling this.
func (s *P2POfferStore) Cancel(id string) (*P2POffer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok || o.Status != OfferOpen {
		return nil, false
	}

	o.Status = OfferCancelled
	return o, true
}

// BeginExternalSettlement marks an EXTERNAL-mode offer accepted and
// awaiting the buyer's off-app payment - unlike Accept, this moves no
// on-chain funds for the "want" side (there's nothing on-chain to
// move for EXTERNAL mode). The seller's escrowed OfferAmount stays
// locked until settlement concludes one of four ways (see
// SettlementStatus's doc comment).
func (s *P2POfferStore) BeginExternalSettlement(id, acceptor, acceptorPublicKey string, at int64) (*P2POffer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok || o.Status != OfferOpen || o.PaymentMode != PaymentModeExternal {
		return nil, false
	}

	o.Status = OfferAccepted
	o.AcceptedBy = acceptor
	o.AcceptedByPublicKey = acceptorPublicKey
	o.SettlementStatus = SettlementAwaitingPayment
	o.AwaitingPaymentSince = at
	return o, true
}

// MarkBuyerPaid records the buyer's own claim that they've sent the
// off-app payment - only the buyer may set this, and only while still
// awaiting payment. Deliberately does NOT advance SettlementStatus:
// it's a courtesy signal for the seller, not a substitute for the
// seller's own ConfirmSellerPaid (which is what actually unlocks the
// buyer's later confirm/dispute step).
func (s *P2POfferStore) MarkBuyerPaid(id, buyer string, at int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok || o.AcceptedBy != buyer || o.SettlementStatus != SettlementAwaitingPayment {
		return false
	}

	o.BuyerMarkedPaidAt = at
	return true
}

// ConfirmSellerPaid records the seller's own claim that they received
// the off-app payment - only the creator may answer this, and only
// once, while settlement is still awaiting it. Moves straight to
// CONFIRMED (the caller then releases escrow immediately) rather than
// waiting on a separate buyer confirmation - the receiving side's own
// confirmation IS the closing action now; a buyer who thinks the
// receiving side is confirming falsely has no recourse to contest
// this after the fact, same as any other irreversible on-chain claim
// in this flow. The buyer's own protection is upstream of this call:
// DisputeAwaitingPayment lets the receiving side reject a payment
// claim it says never arrived, and ConfirmBuyer/SettlementSellerConfirmed
// are kept only so any offer already in that transitional state before
// this behavior changed still has a way to resolve (see their own doc
// comments) - no new offer reaches SellerConfirmed going forward.
func (s *P2POfferStore) ConfirmSellerPaid(id, seller string, at int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok || o.Creator != seller || o.SettlementStatus != SettlementAwaitingPayment {
		return false
	}

	o.SellerConfirmedPaid = true
	o.SettlementStatus = SettlementConfirmed
	o.SellerConfirmedAt = at
	return true
}

// DisputeAwaitingPayment lets the receiving side (Creator - whoever's
// escrowed asset is on the line) reject a payment claim before ever
// confirming it, instead of only being able to disagree AFTER already
// claiming receipt (which ConfirmBuyer used to gate). Only valid while
// still AWAITING_PAYMENT, only by the Creator, and only once the
// payer has actually made a claim (BuyerMarkedPaidAt > 0) - nothing to
// dispute before that. Moves straight to DISPUTED, the same terminal-
// pending status ResolveDispute already knows how to adjudicate.
func (s *P2POfferStore) DisputeAwaitingPayment(id, seller string) (*P2POffer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok || o.Creator != seller || o.SettlementStatus != SettlementAwaitingPayment || o.BuyerMarkedPaidAt == 0 {
		return nil, false
	}

	o.SettlementStatus = SettlementDisputed
	return o, true
}

// DisputeEligibleAfterSeconds is how long a buyer's own MarkBuyerPaid
// claim has to sit unanswered (no ConfirmSellerPaid, no
// DisputeAwaitingPayment from the seller) before the buyer may escalate
// to a moderator themselves - see DisputeAwaitingPaymentByBuyer. Gives
// the seller a real window to check their account before the buyer can
// summon a moderator, rather than letting either side dispute the
// instant a claim is made.
const DisputeEligibleAfterSeconds = 20 * 60

// DisputeAwaitingPaymentByBuyer is the buyer's own escalation path,
// complementing DisputeAwaitingPayment (the seller's immediate right to
// reject a claim they say never arrived): if the buyer marked payment
// sent and the seller has neither confirmed nor disputed it within
// DisputeEligibleAfterSeconds, the buyer can summon a moderator
// themselves instead of waiting indefinitely on a silent seller. Only
// valid while still AWAITING_PAYMENT, only by AcceptedBy (the buyer),
// and only once their own claim is actually on record.
func (s *P2POfferStore) DisputeAwaitingPaymentByBuyer(id, buyer string, now int64) (*P2POffer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok || o.AcceptedBy != buyer || o.SettlementStatus != SettlementAwaitingPayment || o.BuyerMarkedPaidAt == 0 {
		return nil, false
	}
	if now-o.BuyerMarkedPaidAt < DisputeEligibleAfterSeconds {
		return nil, false
	}

	o.SettlementStatus = SettlementDisputed
	return o, true
}

// ConfirmBuyer is EXTERNAL-only-during-a-narrow-transitional-window
// dead weight, kept solely so an offer already sitting in
// SELLER_CONFIRMED at the moment ConfirmSellerPaid's behavior changed
// (see its doc comment) still has a way to resolve - no offer created
// after that change ever reaches SELLER_CONFIRMED again, since
// ConfirmSellerPaid now goes straight to CONFIRMED itself. Agreeing
// moves to CONFIRMED (the caller then releases escrow), disagreeing
// moves to DISPUTED, exactly as before.
func (s *P2POfferStore) ConfirmBuyer(id, buyer string, agree bool) (*P2POffer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok || o.AcceptedBy != buyer || o.SettlementStatus != SettlementSellerConfirmed {
		return nil, false
	}

	if agree {
		o.SettlementStatus = SettlementConfirmed
	} else {
		o.SettlementStatus = SettlementDisputed
	}
	return o, true
}

// ResolveDispute is the assigned moderator's final call once a
// dispute has been raised - the one place in this whole flow where a
// genuine disagreement actually gets settled. release=true sides with
// the buyer (escrow releases to them), release=false sides with the
// seller (escrow returns to them) - either way, this is a disclosed
// human trust decision, not a cryptographic verification of what
// actually happened off-app (see core/moderator.go's doc comment).
func (s *P2POfferStore) ResolveDispute(id, moderator string, release bool) (*P2POffer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok || o.ModeratorAddress != moderator || o.SettlementStatus != SettlementDisputed {
		return nil, false
	}

	if release {
		o.SettlementStatus = SettlementResolvedRelease
	} else {
		o.SettlementStatus = SettlementResolvedRefund
	}
	return o, true
}

// AddMessage appends a chat message to an EXTERNAL-mode offer's
// thread - callers (api/p2p_external.go) confirm the sender is the
// creator, acceptor, or assigned moderator before calling this.
func (s *P2POfferStore) AddMessage(id string, msg NegotiationMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok || o.PaymentMode != PaymentModeExternal || o.Status != OfferAccepted {
		return false
	}

	o.Messages = append(o.Messages, msg)
	return true
}

// Moderate records the assigned moderator's on-chain attestation for
// this P2P offer - same one-shot, assigned-address-only rule as
// NegotiationStore.Moderate.
func (s *P2POfferStore) Moderate(id, moderator string, disputed bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok {
		return false
	}
	if o.ModeratorAddress == "" || o.ModeratorAddress != moderator {
		return false
	}
	if o.ModerationStatus != "" && o.ModerationStatus != ModerationPending {
		return false
	}

	if disputed {
		o.ModerationStatus = ModerationDisputed
	} else {
		o.ModerationStatus = ModerationConfirmed
	}
	return true
}
