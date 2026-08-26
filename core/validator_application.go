package core

import (
	"sort"
	"sync"
)

// ValidatorApplication is the staged, harder-to-reach validator
// onboarding pipeline - see api/validator_application.go's doc
// comment for the full three-stage design (governance pre-vote ->
// email-link + real node-identity proof -> stepped fee/stake table).
// One application per address at a time - a wallet that lets its
// application go stale can always start a fresh one (Create
// overwrites a non-APPROVED prior attempt).
type ValidatorApplicationStatus string

const (
	// ValidatorApplicationOpen is gathering governance support - the
	// only status where Support() still does anything.
	ValidatorApplicationOpen ValidatorApplicationStatus = "OPEN"
	// ValidatorApplicationPendingNodeProof means the 1%-of-total-
	// governance-weight threshold was reached and an email link was
	// sent - the applicant must now complete real node-binding
	// (existing /validators/register flow) and lock stake before the
	// token expires.
	ValidatorApplicationPendingNodeProof ValidatorApplicationStatus = "PENDING_NODE_PROOF"
	// ValidatorApplicationApproved means the applicant has completed
	// every stage and is now a real, eligible validator via the
	// existing, unmodified HandleRegisterValidator path.
	ValidatorApplicationApproved ValidatorApplicationStatus = "APPROVED"
)

type ValidatorApplication struct {
	Address       string
	Status        ValidatorApplicationStatus
	SupportWeight uint64
	// Supporters dedups support - one governance-weighted "yes" per
	// wallet per application, same convention as
	// core.ProposalStore.Voted.
	Supporters map[string]bool
	// ConfirmToken/TokenExpiresAt gate the stage-2 email link - a
	// random, unguessable token (see api/validator_application.go's
	// HandleValidatorApplicationConfirm), valid for 7 days.
	ConfirmToken   string
	TokenExpiresAt int64
	CreatedAt      int64
	ApprovedAt     int64
}

type ValidatorApplicationStore struct {
	mu   sync.Mutex
	Apps map[string]*ValidatorApplication // keyed by Address
}

func NewValidatorApplicationStore() *ValidatorApplicationStore {
	return &ValidatorApplicationStore{Apps: make(map[string]*ValidatorApplication)}
}

// Create opens a fresh application for address, overwriting any prior
// non-APPROVED attempt (an applicant who let a previous try lapse can
// always start over) - returns false if address is already APPROVED
// (nothing to re-apply for).
func (s *ValidatorApplicationStore) Create(address string, at int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.Apps[address]; ok && existing.Status == ValidatorApplicationApproved {
		return false
	}
	s.Apps[address] = &ValidatorApplication{
		Address:    address,
		Status:     ValidatorApplicationOpen,
		Supporters: make(map[string]bool),
		CreatedAt:  at,
	}
	return true
}

func (s *ValidatorApplicationStore) Get(address string) (*ValidatorApplication, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Apps[address]
	return a, ok
}

// Support adds one supporter's governance weight to an OPEN
// application, once per supporter - returns the application's
// resulting SupportWeight and whether this call actually counted
// (false if already open-supported by this wallet, or the
// application isn't OPEN).
func (s *ValidatorApplicationStore) Support(address, supporter string, weight uint64) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Apps[address]
	if !ok || a.Status != ValidatorApplicationOpen || a.Supporters[supporter] {
		return 0, false
	}
	a.Supporters[supporter] = true
	a.SupportWeight += weight
	return a.SupportWeight, true
}

// AdvanceToNodeProof flips an OPEN application to
// PENDING_NODE_PROOF once its SupportWeight has crossed the caller-
// computed 1% threshold, stamping the email-confirm token - a no-op
// (returns false) if the application isn't OPEN or hasn't reached
// the threshold, so this is always safe to call speculatively after
// every Support().
func (s *ValidatorApplicationStore) AdvanceToNodeProof(address, token string, tokenExpiresAt, requiredWeight int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Apps[address]
	if !ok || a.Status != ValidatorApplicationOpen {
		return false
	}
	if int64(a.SupportWeight) < requiredWeight {
		return false
	}
	a.Status = ValidatorApplicationPendingNodeProof
	a.ConfirmToken = token
	a.TokenExpiresAt = tokenExpiresAt
	return true
}

// Approve flips a PENDING_NODE_PROOF application to APPROVED, only
// if token matches and hasn't expired - the actual gate
// HandleValidatorApplicationConfirm relies on.
func (s *ValidatorApplicationStore) Approve(address, token string, at int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Apps[address]
	if !ok || a.Status != ValidatorApplicationPendingNodeProof {
		return false
	}
	if a.ConfirmToken == "" || a.ConfirmToken != token || at > a.TokenExpiresAt {
		return false
	}
	a.Status = ValidatorApplicationApproved
	a.ApprovedAt = at
	return true
}

// IsApproved is the one gate api/validators.go's HandleValidators
// checks before returning real leaderboard data to an unapproved
// caller - see the staged-pipeline design's §7.4.
func (s *ValidatorApplicationStore) IsApproved(address string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Apps[address]
	return ok && a.Status == ValidatorApplicationApproved
}

// ApprovedCount is how many applications have ever been approved -
// the input to the stepped, count-based support-weight bar (see
// api/validator_application.go's validatorApplicationRequiredWeight):
// the first-ever applicant needs 1, the next needs 2, and so on.
func (s *ValidatorApplicationStore) ApprovedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, a := range s.Apps {
		if a.Status == ValidatorApplicationApproved {
			count++
		}
	}
	return count
}

// SupportCountFor is how many distinct applications this wallet has
// already thrown its support behind, across every application ever
// created (not just currently-OPEN ones) - the input to the per-wallet
// vote cap (see api/validator_application.go's
// validatorApplicationMaxVotesPerWallet). Computed live from the
// existing per-application Supporters map rather than a second
// counter, so there's only ever one source of truth to keep in sync.
func (s *ValidatorApplicationStore) SupportCountFor(wallet string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, a := range s.Apps {
		if a.Supporters[wallet] {
			count++
		}
	}
	return count
}

// ListOpen returns every OPEN application - the "gathering support"
// stage where Support() still does anything - sorted by CreatedAt
// then Address for a stable, deterministic display order across
// repeated calls (this is a plain read, not consensus-critical, but a
// list that reorders itself on every poll is a bad user experience).
func (s *ValidatorApplicationStore) ListOpen() []*ValidatorApplication {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*ValidatorApplication, 0, len(s.Apps))
	for _, a := range s.Apps {
		if a.Status == ValidatorApplicationOpen {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].Address < out[j].Address
	})
	return out
}

func (s *ValidatorApplicationStore) Snapshot() map[string]*ValidatorApplication {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*ValidatorApplication, len(s.Apps))
	for k, v := range s.Apps {
		out[k] = v
	}
	return out
}

func (s *ValidatorApplicationStore) Restore(all map[string]*ValidatorApplication) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range all {
		s.Apps[k] = v
	}
}
