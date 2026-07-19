package core

import "sync"

type LoanRequestStatus string

const (
	LoanRequestOpen      LoanRequestStatus = "OPEN"
	LoanRequestFulfilled LoanRequestStatus = "FULFILLED"
	LoanRequestCancelled LoanRequestStatus = "CANCELLED"
)

// MaxLoanRequestPurposeLen caps the optional free-text Purpose field,
// same length-bounding convention this codebase already applies to
// other user-supplied text (e.g. governance title/description) to
// prevent memory-exhaustion via oversized payloads.
const MaxLoanRequestPurposeLen = 280

// LoanRequest is the borrower-initiated mirror of LoanOffer - a
// borrower posts what they want to borrow, against what collateral,
// at what maximum rate, and lenders browse and fund it (see
// api/loan_requests.go, and the negotiation system's RequestID field
// in core/negotiation.go, which reuses the exact same chat/confirm
// flow LoanOffer draws already go through). Collateral is reserved at
// request-creation time (see api/apply.go's applyLoanRequestCreate),
// not at fulfillment time, so a request can never be posted against
// collateral the borrower doesn't actually have.
type LoanRequest struct {
	ID string

	Borrower          string
	BorrowerPublicKey string // needed to re-derive the borrower's derived sub-address at fulfillment time - see resolveBalanceAddress

	RequestedSymbol string // which token the borrower wants - YENE today, same default applyBorrow uses
	Amount          uint64

	CollateralSymbol string // "CCT" | "OETH" - same two options applyBorrow already supports
	CollateralAmount uint64

	MaxInterestPercent uint64 // the borrower will not accept a funding offer above this rate
	DurationSeconds    uint64 // 0 = never defaults, same convention as LoanOffer

	Purpose string // optional, capped at MaxLoanRequestPurposeLen

	Status    LoanRequestStatus
	CreatedAt int64
}

type LoanRequestStore struct {
	mu       sync.Mutex
	Requests map[string]*LoanRequest
}

func NewLoanRequestStore() *LoanRequestStore {
	return &LoanRequestStore{Requests: make(map[string]*LoanRequest)}
}

func (s *LoanRequestStore) Create(r *LoanRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Requests[r.ID] = r
}

func (s *LoanRequestStore) Get(id string) (*LoanRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Requests[id]
	return r, ok
}

// List returns every request regardless of status, same reasoning as
// LoanOfferStore.List - a fulfilled/cancelled request stays visible
// rather than disappearing.
func (s *LoanRequestStore) List() []*LoanRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]*LoanRequest, 0, len(s.Requests))
	for _, r := range s.Requests {
		list = append(list, r)
	}
	return list
}

// Cancel lets the borrower withdraw an OPEN request - only valid
// while still OPEN, mirroring LoanOfferStore.Close. The caller is
// responsible for releasing the reserved collateral (see
// api/apply.go's applyLoanRequestCancel), same separation of concerns
// LoanOfferStore.Draw/Close already keep from token-store mutation.
func (s *LoanRequestStore) Cancel(id, borrower string) (*LoanRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Requests[id]
	if !ok || r.Status != LoanRequestOpen || r.Borrower != borrower {
		return nil, false
	}

	r.Status = LoanRequestCancelled
	return r, true
}

// Fulfill atomically checks the request is still OPEN before flipping
// it to FULFILLED - same check-and-mutate-in-one-lock pattern
// LoanOfferStore.Draw uses, so a request can't be fulfilled twice by
// two racing negotiation confirmations.
func (s *LoanRequestStore) Fulfill(id string) (*LoanRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Requests[id]
	if !ok || r.Status != LoanRequestOpen {
		return nil, false
	}

	r.Status = LoanRequestFulfilled
	return r, true
}
