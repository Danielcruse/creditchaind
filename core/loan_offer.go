package core

import "sync"

type LoanOfferStatus string

const (
	LoanOfferOpen   LoanOfferStatus = "OPEN"
	LoanOfferClosed LoanOfferStatus = "CLOSED"
)

// LoanOffer is a lender's standing offer to lend, at a rate and up to
// a capital amount THEY choose - the missing half of peer-funded
// lending that plain /borrow never had (see api/apply.go's
// applyBorrow, which self-funds from newly minted USDCTEST with no
// lender involved at all). A borrower discovers open offers (see
// api/loan_offers.go's HandleListLoanOffers) and draws whatever amount
// they want against one, up to whatever capital remains - the
// borrower picks the amount, not the lender, but always at the rate
// the lender already committed to.
type LoanOffer struct {
	ID string

	Lender          string
	LenderPublicKey string // needed to re-derive the lender's derived sub-address (ETHTEST/USDCTEST) at draw/close time - see resolveBalanceAddress

	Symbol      string // which token this offer lends - CCT, ETHTEST, USDCTEST, REPT, or a developer-registered token
	RatePercent uint64

	// DurationSeconds is how long a borrower has to repay once they
	// draw against this offer, set by the lender when posting it - the
	// clock that makes a real "default" possible (see
	// api/apply.go's processLoanDefaults). Zero means no due date is
	// enforced on loans drawn against this offer - kept as an explicit
	// opt-in rather than defaulting every offer to some duration, so
	// existing offers created before this field existed stay exempt.
	DurationSeconds uint64

	TotalAmount     uint64
	RemainingAmount uint64

	Status    LoanOfferStatus
	CreatedAt int64
}

type LoanOfferStore struct {
	mu     sync.Mutex
	Offers map[string]*LoanOffer
}

func NewLoanOfferStore() *LoanOfferStore {
	return &LoanOfferStore{Offers: make(map[string]*LoanOffer)}
}

func (s *LoanOfferStore) Create(o *LoanOffer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Offers[o.ID] = o
}

func (s *LoanOfferStore) Get(id string) (*LoanOffer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	return o, ok
}

// List returns every offer regardless of status, same reasoning as
// P2POfferStore.List - a closed/exhausted offer stays visible rather
// than disappearing.
func (s *LoanOfferStore) List() []*LoanOffer {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]*LoanOffer, 0, len(s.Offers))
	for _, o := range s.Offers {
		list = append(list, o)
	}
	return list
}

// Draw atomically checks the offer is OPEN and has enough remaining
// capital before deducting amount - the same check-and-mutate-in-one-
// lock pattern P2POfferStore.Accept uses, so two simultaneous draws
// against the same offer can't both succeed if only one of them
// actually fits in the remaining capital. Auto-closes the offer once
// its capital is fully drawn.
func (s *LoanOfferStore) Draw(id string, amount uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok || o.Status != LoanOfferOpen || amount == 0 || amount > o.RemainingAmount {
		return false
	}

	o.RemainingAmount -= amount
	if o.RemainingAmount == 0 {
		o.Status = LoanOfferClosed
	}
	return true
}

// Close lets the lender withdraw whatever capital is still
// undrawn, same as P2POfferStore.Cancel - only valid while still OPEN.
func (s *LoanOfferStore) Close(id, lender string) (*LoanOffer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.Offers[id]
	if !ok || o.Status != LoanOfferOpen || o.Lender != lender {
		return nil, false
	}

	o.Status = LoanOfferClosed
	return o, true
}
