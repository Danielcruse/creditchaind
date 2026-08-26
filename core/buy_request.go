package core

import "sync"

// BuyRequest is the reverse of a P2POffer: a verified Trading Hub (see
// core/company.go) posting that it wants to BUY crypto for real-world
// payment, rather than sell it. Whoever fulfills the request is the
// one who actually escrows the crypto (see
// api/apply.go's applyBuyRequestFulfill) - a hub never holds an
// inventory of everyone's tokens, it just posts a standing bid.
type BuyRequestStatus string

const (
	BuyRequestOpen      BuyRequestStatus = "OPEN"
	BuyRequestFulfilled BuyRequestStatus = "FULFILLED"
	BuyRequestCancelled BuyRequestStatus = "CANCELLED"
)

type BuyRequest struct {
	ID string

	Hub          string
	HubPublicKey string

	// WantSymbol/WantAmount is the crypto the hub wants to receive.
	WantSymbol string
	WantAmount uint64

	// PaySymbol/PayAmount is the real-world payment the hub will make
	// off-app in exchange - free-text/descriptive only, same as
	// P2POffer's EXTERNAL-mode WantSymbol/WantAmount.
	PaySymbol string
	PayAmount uint64

	Status BuyRequestStatus

	// FulfilledOfferID links to the P2POffer created at fulfillment
	// time (see applyBuyRequestFulfill) - "" until fulfilled.
	FulfilledOfferID string

	CreatedAt int64
}

type BuyRequestStore struct {
	mu       sync.Mutex
	Requests map[string]*BuyRequest
}

func NewBuyRequestStore() *BuyRequestStore {
	return &BuyRequestStore{Requests: make(map[string]*BuyRequest)}
}

func (s *BuyRequestStore) Create(r *BuyRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Requests[r.ID] = r
}

func (s *BuyRequestStore) Get(id string) (*BuyRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Requests[id]
	return r, ok
}

// List returns every request regardless of status, same reasoning as
// every other list store in this package.
func (s *BuyRequestStore) List() []*BuyRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]*BuyRequest, 0, len(s.Requests))
	for _, r := range s.Requests {
		list = append(list, r)
	}
	return list
}

// Fulfill marks an OPEN request FULFILLED, atomically with the check
// that it was still open - the actual P2POffer creation is the
// caller's job (see api/apply.go's applyBuyRequestFulfill).
func (s *BuyRequestStore) Fulfill(id, offerID string) (*BuyRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Requests[id]
	if !ok || r.Status != BuyRequestOpen {
		return nil, false
	}

	r.Status = BuyRequestFulfilled
	r.FulfilledOfferID = offerID
	return r, true
}

// Cancel lets only the hub withdraw a request that hasn't been
// fulfilled yet.
func (s *BuyRequestStore) Cancel(id, hub string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Requests[id]
	if !ok || r.Hub != hub || r.Status != BuyRequestOpen {
		return false
	}

	r.Status = BuyRequestCancelled
	return true
}
