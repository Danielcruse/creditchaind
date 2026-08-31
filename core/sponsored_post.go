package core

import "sync"

// SponsoredPostStatus tracks a Sponsored Post through the same
// disclosed-trust moderator review model as CompanyApplication
// (company.go) - a post's text is never shown to anyone until a
// registered moderator has reviewed it.
type SponsoredPostStatus string

const (
	SponsoredPostPending  SponsoredPostStatus = "PENDING_REVIEW"
	SponsoredPostActive   SponsoredPostStatus = "ACTIVE"
	SponsoredPostRejected SponsoredPostStatus = "REJECTED"
	SponsoredPostExpired  SponsoredPostStatus = "EXPIRED"
)

// SponsoredPost is a single paid promotion an advertiser submitted for
// the governance feed (see api/sponsored_post.go's doc comment for the
// full flow: pay CCT -> moderator review -> serve -> impressions).
type SponsoredPost struct {
	ID         string
	Advertiser string
	// AdvertiserPublicKey is the advertiser's authenticated public key,
	// captured at creation time - same reasoning as every other record
	// in this codebase that needs to independently re-derive a
	// sub-address later (see core.P2POffer.CreatorPublicKey's doc
	// comment for the canonical explanation of this pattern).
	AdvertiserPublicKey string

	// ValidatorID is "" for global reach (shown to every viewer,
	// subject to the same Trust-Index ranking as a validator-scoped
	// post) or a specific validator's address - in which case only a
	// wallet that has delegated CCT to that validator
	// (DelegationStore.DelegatedBy, validator_delegation.go) is
	// eligible to see this post at all. This IS the "validator
	// community" - real, already-existing delegation state, not a new
	// follow/subscribe relationship.
	ValidatorID string

	Text     string
	Category SponsoredPostCategory

	// CCTPaid is the whole-CCT fee the advertiser paid at creation time
	// - already split (see api/apply.go's splitAdFee) before this
	// record exists, so this field is purely informational/display,
	// never re-spent from here.
	CCTPaid uint64

	Status SponsoredPostStatus
	// ModeratorAddress is assigned the same round-robin way as every
	// other moderator-reviewed item (see ModeratorStore.AssignNext).
	ModeratorAddress string
	ReviewedAt       int64

	ImpressionCount uint64

	CreatedAt int64
	// ExpiresAt is when this post stops being served even if still
	// ACTIVE - CCT buys a fixed run length, not unlimited display. Zero
	// only for a PENDING_REVIEW post that hasn't been approved yet (see
	// SponsoredPostStore.Review, which sets this on approval).
	ExpiresAt int64
}

type SponsoredPostStore struct {
	mu    sync.Mutex
	byID  map[string]*SponsoredPost
	order []string // insertion order, oldest first - mirrors GovernanceStatusStore
}

func NewSponsoredPostStore() *SponsoredPostStore {
	return &SponsoredPostStore{byID: make(map[string]*SponsoredPost)}
}

func (s *SponsoredPostStore) Create(p *SponsoredPost) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byID[p.ID] = p
	s.order = append(s.order, p.ID)
}

func (s *SponsoredPostStore) Get(id string) (*SponsoredPost, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.byID[id]
	return p, ok
}

// ListActive returns every currently-ACTIVE, unexpired post scoped to
// validatorID ("" for global-reach posts) as of asOf, newest first -
// callers combine a "" call (global reach) with a specific-validator
// call to get everything one viewer is eligible for (see
// api/sponsored_post.go's HandleSponsoredPostFeed).
func (s *SponsoredPostStore) ListActive(validatorID string, asOf int64) []*SponsoredPost {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []*SponsoredPost
	for i := len(s.order) - 1; i >= 0; i-- {
		p := s.byID[s.order[i]]
		if p.Status != SponsoredPostActive {
			continue
		}
		if p.ExpiresAt != 0 && asOf >= p.ExpiresAt {
			continue
		}
		if p.ValidatorID != validatorID {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ForAdvertiser returns every post advertiser has ever submitted,
// regardless of status, newest first - an advertiser's own submission
// history, same "nothing disappears, newest first" convention as
// NotificationStore.ListFor.
func (s *SponsoredPostStore) ForAdvertiser(advertiser string) []*SponsoredPost {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []*SponsoredPost
	for i := len(s.order) - 1; i >= 0; i-- {
		p := s.byID[s.order[i]]
		if p.Advertiser == advertiser {
			out = append(out, p)
		}
	}
	return out
}

// ForModerator returns every post currently assigned to moderator,
// regardless of status - the moderator's own review queue, same shape
// as CompanyStore's implicit per-moderator filtering (api/companies.go
// filters CompanyStore.List() by ModeratorAddress client-side; this
// does the equivalent server-side since a moderator's ad queue is
// expected to be a more frequent read).
func (s *SponsoredPostStore) ForModerator(moderator string) []*SponsoredPost {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []*SponsoredPost
	for _, id := range s.order {
		p := s.byID[id]
		if p.ModeratorAddress == moderator {
			out = append(out, p)
		}
	}
	return out
}

// Review records the assigned moderator's approve/reject decision -
// only the assigned moderator may answer, and only once, same "final,
// re-apply to relitigate" rule as CompanyStore.Review. runLengthSeconds
// is only used on approval, to compute ExpiresAt from at.
func (s *SponsoredPostStore) Review(id, moderator string, approve bool, runLengthSeconds, at int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.byID[id]
	if !ok || p.ModeratorAddress != moderator || p.Status != SponsoredPostPending {
		return false
	}

	if approve {
		p.Status = SponsoredPostActive
		p.ExpiresAt = at + runLengthSeconds
	} else {
		p.Status = SponsoredPostRejected
	}
	p.ReviewedAt = at
	return true
}

// ExpireStale flips every ACTIVE post whose ExpiresAt has passed to
// EXPIRED as of asOf - block-timestamp-driven, same determinism
// reasoning as every other sweep in this codebase (e.g.
// processLoanDefaults). Returns nothing: this is a pure housekeeping
// pass, ListActive already excludes an expired-but-not-yet-swept post
// on its own via the ExpiresAt check, so nothing downstream depends on
// this having already run - it just keeps Status accurate for display.
func (s *SponsoredPostStore) ExpireStale(asOf int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range s.order {
		p := s.byID[id]
		if p.Status == SponsoredPostActive && p.ExpiresAt != 0 && asOf >= p.ExpiresAt {
			p.Status = SponsoredPostExpired
		}
	}
}

// RecordImpression increments an ACTIVE post's display counter -
// node-local only (not consensus-tracked), same "purely informational"
// status as HubNotification - see api/sponsored_post.go's doc comment
// for why v1 doesn't put impressions on chain.
func (s *SponsoredPostStore) RecordImpression(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p, ok := s.byID[id]; ok {
		p.ImpressionCount++
	}
}

func (s *SponsoredPostStore) Snapshot() []*SponsoredPost {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*SponsoredPost, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.byID[id])
	}
	return out
}

func (s *SponsoredPostStore) Restore(all []*SponsoredPost) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range all {
		s.byID[p.ID] = p
		s.order = append(s.order, p.ID)
	}
}

// sponsoredPostRcSpendTiers pairs a minimum REPT (RC) balance with the
// most CCT an advertiser at that tier may spend on a single Sponsored
// Post - mirrors BorrowLimit's tier-lookup shape (borrow_limits.go)
// exactly. Highest-first so MaxAdSpendForRept can short-circuit on the
// first match. A disclosed judgment call, not a locked spec value -
// the point is that spend scales with earned reputation, discouraging
// a fresh, unreputable wallet from buying a large campaign, not the
// exact numbers.
var sponsoredPostRcSpendTiers = []struct {
	MinRept  uint64
	MaxSpend uint64
}{
	{500, 50000},
	{100, 10000},
	{20, 2000},
	{0, 200},
}

// MaxAdSpendForRept returns the most CCT a wallet holding reptBalance
// RC may spend on a single Sponsored Post - the "RC gates eligibility
// and reach, CCT pays" split this feature was explicitly designed
// around (RC is never itself spent or burned to buy an ad).
func MaxAdSpendForRept(reptBalance uint64) uint64 {
	for _, t := range sponsoredPostRcSpendTiers {
		if reptBalance >= t.MinRept {
			return t.MaxSpend
		}
	}
	return 0
}
