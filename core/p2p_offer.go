package core

import "sync"

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
//	AWAITING_PAYMENT  - buyer accepted, needs to pay the seller off-app
//	SELLER_CONFIRMED  - seller says they received it
//	CONFIRMED         - buyer agreed -> escrow released to buyer, done
//	DISPUTED          - buyer disagreed -> only the assigned moderator
//	                    can resolve this from here
//	RESOLVED_RELEASE  - moderator sided with the buyer -> escrow released
//	RESOLVED_REFUND   - moderator sided with the seller -> escrow returned
type SettlementStatus string

const (
	SettlementAwaitingPayment SettlementStatus = "AWAITING_PAYMENT"
	SettlementSellerConfirmed SettlementStatus = "SELLER_CONFIRMED"
	SettlementConfirmed       SettlementStatus = "CONFIRMED"
	SettlementDisputed        SettlementStatus = "DISPUTED"
	SettlementResolvedRelease SettlementStatus = "RESOLVED_RELEASE"
	SettlementResolvedRefund  SettlementStatus = "RESOLVED_REFUND"
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
	// claim) does that. 0 if the buyer hasn't marked it yet.
	BuyerMarkedPaidAt int64

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
// once, while settlement is still awaiting it.
func (s *P2POfferStore) ConfirmSellerPaid(id, seller string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok || o.Creator != seller || o.SettlementStatus != SettlementAwaitingPayment {
		return false
	}

	o.SellerConfirmedPaid = true
	o.SettlementStatus = SettlementSellerConfirmed
	return true
}

// ConfirmBuyer records the buyer's answer once the seller has claimed
// payment - agreeing moves to CONFIRMED (the caller then releases
// escrow), disagreeing moves to DISPUTED, where only the assigned
// moderator can resolve it from here (see ResolveDispute) - nothing
// automated can adjudicate two parties giving conflicting answers.
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
