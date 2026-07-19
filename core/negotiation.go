package core

import "sync"

type NegotiationStatus string

const (
	NegotiationOpen      NegotiationStatus = "OPEN"
	NegotiationExecuted  NegotiationStatus = "EXECUTED"
	NegotiationCancelled NegotiationStatus = "CANCELLED"
)

// ModerationStatus tracks a registered moderator's own attestation
// about a trade, entirely separate from the trade's own
// NegotiationStatus/OfferStatus - a negotiation can be EXECUTED (the
// on-chain funds already moved) while its moderation is still PENDING
// (nobody has attested to it yet), since moderator oversight never
// gates the actual fund movement here (see core/moderator.go's doc
// comment on why this is a disclosed attestation layer, not an
// escrow-release mechanism).
type ModerationStatus string

const (
	ModerationPending   ModerationStatus = "PENDING"
	ModerationConfirmed ModerationStatus = "CONFIRMED"
	ModerationDisputed  ModerationStatus = "DISPUTED"
)

// NegotiationMessage is one chat message in a negotiation thread.
// Deliberately plain text, world-readable via the same public-GET
// pattern every other query in this app already uses (e.g.
// /collateral?address=, /staking?address=) - this is NOT a private/
// encrypted DM system, and should never be treated as one.
type NegotiationMessage struct {
	Sender    string
	Text      string
	Timestamp int64
}

// Negotiation is the chat-first negotiation a borrower opens against a
// specific LoanOffer before anything is recorded on chain - the
// borrower proposes an amount (already signing the exact draw request
// they want, stored in BorrowerAuth), both sides can message back and
// forth, and only once the LENDER explicitly agrees does the server
// actually submit the underlying TxLoanOfferDraw transaction using the
// borrower's already-collected signature. Nothing here touches the
// blockchain/consensus model until that final step - a negotiation
// existing, its messages, and its confirmation state are ordinary
// node-local API state, not part of ApplyBlock.
type Negotiation struct {
	ID string

	// Exactly one of OfferID/RequestID is set, never both - OfferID for
	// the original lender-posts-offer/borrower-draws direction,
	// RequestID for the newer borrower-posts-request/lender-funds
	// direction (see core.LoanRequest's doc comment). Both directions
	// share this same chat/confirm machinery; only who initiates and
	// what gets executed on confirm differs (see api/negotiations.go).
	OfferID   string
	RequestID string
	Lender    string
	Borrower  string

	Symbol string
	Amount uint64

	// RatePercent is only meaningful for a RequestID-backed negotiation
	// (the rate the lender is proposing, must be <= the request's
	// MaxInterestPercent) - a OfferID-backed negotiation's rate is
	// already fixed by the offer itself and this field is unused/0.
	RatePercent uint64

	Messages []NegotiationMessage

	// BorrowerAuth is the borrower's own already-verified signed draw
	// request (envelope.Verify() already passed when this was
	// collected) - kept exactly as originally signed so it can be used
	// to build the real transaction later, however long the
	// negotiation takes. Re-verifying it again at execution time only
	// checks the signature itself (see core.VerifyTransactionAuth),
	// never a freshness window - staleness only ever mattered at the
	// moment it was first collected. Only populated for an OfferID-
	// backed negotiation (borrower-initiated) - a RequestID-backed
	// negotiation is lender-initiated, so the fund-moving signature is
	// instead captured fresh at confirm time from the lender (see
	// HandleConfirmNegotiation), since the borrower's own consent was
	// already given, signed, and block-confirmed when they created the
	// LoanRequest itself.
	BorrowerAuth SignedEnvelope `json:"-"`

	Status    NegotiationStatus
	CreatedAt int64

	// ModeratorAddress is assigned once, at negotiation-creation time
	// (see api/negotiations.go's HandleStartNegotiation), from whichever
	// registered moderator is next in round-robin order - "" if no
	// moderator had registered yet when this negotiation was opened.
	ModeratorAddress string
	ModerationStatus ModerationStatus
}

type NegotiationStore struct {
	mu           sync.Mutex
	Negotiations map[string]*Negotiation
}

func NewNegotiationStore() *NegotiationStore {
	return &NegotiationStore{Negotiations: make(map[string]*Negotiation)}
}

func (s *NegotiationStore) Create(n *Negotiation) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Negotiations[n.ID] = n
}

func (s *NegotiationStore) Get(id string) (*Negotiation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.Negotiations[id]
	return n, ok
}

// List returns every negotiation regardless of status, same reasoning
// as every other offer/list store in this app - a closed-out
// negotiation's history stays visible.
func (s *NegotiationStore) List() []*Negotiation {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]*Negotiation, 0, len(s.Negotiations))
	for _, n := range s.Negotiations {
		list = append(list, n)
	}
	return list
}

func (s *NegotiationStore) AddMessage(id string, msg NegotiationMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.Negotiations[id]
	if !ok || n.Status != NegotiationOpen {
		return false
	}

	n.Messages = append(n.Messages, msg)
	return true
}

// Execute atomically transitions an OPEN negotiation to EXECUTED -
// the caller (api/negotiations.go) still has to actually submit and
// apply the real transaction; this just guards against the same
// negotiation being executed or cancelled twice.
func (s *NegotiationStore) Execute(id string) (*Negotiation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.Negotiations[id]
	if !ok || n.Status != NegotiationOpen {
		return nil, false
	}

	n.Status = NegotiationExecuted
	return n, true
}

// Cancel lets either party withdraw before execution.
func (s *NegotiationStore) Cancel(id, caller string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.Negotiations[id]
	if !ok || n.Status != NegotiationOpen {
		return false
	}
	if caller != n.Lender && caller != n.Borrower {
		return false
	}

	n.Status = NegotiationCancelled
	return true
}

// Moderate records the assigned moderator's on-chain attestation -
// only the address this negotiation was actually assigned to may
// confirm or dispute it, and only once (a moderator can't flip their
// attestation back and forth after the fact).
func (s *NegotiationStore) Moderate(id, moderator string, disputed bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.Negotiations[id]
	if !ok {
		return false
	}
	if n.ModeratorAddress == "" || n.ModeratorAddress != moderator {
		return false
	}
	if n.ModerationStatus != "" && n.ModerationStatus != ModerationPending {
		return false
	}

	if disputed {
		n.ModerationStatus = ModerationDisputed
	} else {
		n.ModerationStatus = ModerationConfirmed
	}
	return true
}
