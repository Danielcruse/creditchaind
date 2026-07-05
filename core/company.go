package core

import "sync"

// CompanyApplication is a TESTNET-ONLY placeholder for a much bigger
// feature: a real KYC/AML-verified trading-company directory,
// deferred until mainnet planning includes actual licensing, a real
// identity-verification vendor integration, and legal review - none
// of which exist yet. What's here is deliberately narrow: a seller
// submits some free-text context about who they are, a registered
// moderator (see core/moderator.go) manually reviews it and grants or
// denies a "Verified" badge, and that's it - no document upload/
// storage, no actual identity or certificate verification, no bridge
// to real-world fiat. The badge means "a human moderator looked at
// this and vouched for it," nothing more, same disclosed-trust model
// as every other moderator-gated feature in this package. Replace
// this wholesale before any real KYC claim is ever made to a user.
type CompanyStatus string

const (
	CompanyPending  CompanyStatus = "PENDING"
	CompanyVerified CompanyStatus = "VERIFIED"
	CompanyRejected CompanyStatus = "REJECTED"
)

type CompanyApplication struct {
	Address   string
	PublicKey string

	Name        string
	Description string
	// DocumentNote is free-text context the applicant provides (e.g.
	// "Certificate of Incorporation #12345, registered in X") - never a
	// real uploaded document, never independently verified beyond a
	// moderator reading it and deciding whether to trust it.
	DocumentNote string

	Status CompanyStatus
	// ModeratorAddress is assigned the same way as every other
	// moderator-reviewed item (see core.ModeratorStore.AssignNext) -
	// "" if no moderator had registered yet when this application was
	// submitted.
	ModeratorAddress string

	CreatedAt  int64
	ReviewedAt int64
}

type CompanyStore struct {
	mu           sync.Mutex
	Applications map[string]*CompanyApplication
}

func NewCompanyStore() *CompanyStore {
	return &CompanyStore{Applications: make(map[string]*CompanyApplication)}
}

// Apply registers a new application - a no-op (returns false) if this
// address already has one, since re-applying should go through Review
// again (a rejected applicant re-submitting) rather than silently
// creating a second parallel application.
func (s *CompanyStore) Apply(app *CompanyApplication) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Applications[app.Address]; exists {
		return false
	}
	s.Applications[app.Address] = app
	return true
}

func (s *CompanyStore) Get(address string) (*CompanyApplication, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Applications[address]
	return a, ok
}

// IsVerified is the one check every other feature (e.g. the P2P
// marketplace's "verified seller" badge) actually needs - callers
// shouldn't have to know the CompanyStatus enum themselves.
func (s *CompanyStore) IsVerified(address string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Applications[address]
	return ok && a.Status == CompanyVerified
}

// List returns every application regardless of status - a rejected
// application stays visible, same reasoning as every other list store
// in this package.
func (s *CompanyStore) List() []*CompanyApplication {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]*CompanyApplication, 0, len(s.Applications))
	for _, a := range s.Applications {
		list = append(list, a)
	}
	return list
}

// Review records the assigned moderator's approve/reject decision -
// only the assigned moderator may answer, and only once (a rejected
// applicant must submit a fresh application to be reviewed again,
// rather than the same moderator flip-flopping on one record).
func (s *CompanyStore) Review(address, moderator string, approve bool, at int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Applications[address]
	if !ok || a.ModeratorAddress != moderator || a.Status != CompanyPending {
		return false
	}

	if approve {
		a.Status = CompanyVerified
	} else {
		a.Status = CompanyRejected
	}
	a.ReviewedAt = at
	return true
}

// ReassignModerator lets any currently-registered moderator claim a
// still-PENDING application away from whichever moderator it was
// originally assigned to - the fix for an application getting stuck
// forever if its assigned moderator's key is lost or the address is
// otherwise unreachable, since assignment was previously permanent
// with no way back. Deliberately only works while still PENDING - once
// Review has recorded a real decision (VERIFIED/REJECTED), the record
// is final and this can't be used to relitigate it.
func (s *CompanyStore) ReassignModerator(address, newModerator string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Applications[address]
	if !ok || a.Status != CompanyPending {
		return false
	}

	a.ModeratorAddress = newModerator
	return true
}
