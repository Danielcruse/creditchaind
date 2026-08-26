package core

import (
	"sort"
	"sync"
)

// DisputeFaultChoiceStatus tracks where a refund-resolved dispute's
// at-fault-party accountability currently stands.
type DisputeFaultChoiceStatus string

const (
	DisputeFaultChoicePending   DisputeFaultChoiceStatus = "PENDING"
	DisputeFaultChoicePaid      DisputeFaultChoiceStatus = "PAID"
	DisputeFaultChoiceDeclined  DisputeFaultChoiceStatus = "DECLINED"
	DisputeFaultChoiceDefaulted DisputeFaultChoiceStatus = "DEFAULTED"
)

// DisputeFaultChoiceWindowSeconds is how long the at-fault party has to
// choose before both penalties apply automatically - see
// DisputeFaultChoice's own doc comment.
const DisputeFaultChoiceWindowSeconds = 72 * 3600

// DisputeFaultChoice is created the moment a moderator resolves a P2P
// dispute as a REFUND - a settlement that (deliberately, see
// core/platform_fee.go's own doc comment) collects no trade fee at
// all, so the moderator who did the real work of resolving it would
// otherwise earn nothing from it. Gives the at-fault party (whichever
// side was found to be lying) DisputeFaultChoiceWindowSeconds to
// either pay the moderator directly (PAID - no reputation penalty) or
// explicitly decline (DECLINED - reputation penalty only, no fee). If
// they do neither in time, both apply (DEFAULTED) - see
// api/apply.go's resolveExpiredDisputeFaultChoices.
type DisputeFaultChoice struct {
	OfferID            string
	AtFaultAddress     string
	ResolvingModerator string
	Symbol             string
	FeeAmount          uint64
	CreatedAt          int64
	Deadline           int64
	Status             DisputeFaultChoiceStatus
}

// DisputeFaultChoiceStore is keyed by OfferID - a dispute only ever
// produces one fault choice, since ResolveDispute (core/p2p_offer.go)
// already refuses to resolve the same offer's dispute twice.
type DisputeFaultChoiceStore struct {
	mu      sync.Mutex
	Choices map[string]*DisputeFaultChoice
}

func NewDisputeFaultChoiceStore() *DisputeFaultChoiceStore {
	return &DisputeFaultChoiceStore{Choices: make(map[string]*DisputeFaultChoice)}
}

func (s *DisputeFaultChoiceStore) Create(c *DisputeFaultChoice) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Choices[c.OfferID] = c
}

func (s *DisputeFaultChoiceStore) Get(offerID string) (*DisputeFaultChoice, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.Choices[offerID]
	return c, ok
}

// Pay resolves a pending choice as PAID - only succeeds while it's
// still pending and hasn't already passed its deadline (once
// DEFAULTED, a late attempt to pay doesn't undo the penalty already
// charged - see SweepExpired).
func (s *DisputeFaultChoiceStore) Pay(offerID, atFault string, asOf int64) (*DisputeFaultChoice, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.Choices[offerID]
	if !ok || c.AtFaultAddress != atFault || c.Status != DisputeFaultChoicePending || asOf > c.Deadline {
		return nil, false
	}
	c.Status = DisputeFaultChoicePaid
	return c, true
}

// Decline resolves a pending choice as DECLINED - the at-fault party
// explicitly accepting the reputation penalty instead of paying.
func (s *DisputeFaultChoiceStore) Decline(offerID, atFault string, asOf int64) (*DisputeFaultChoice, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.Choices[offerID]
	if !ok || c.AtFaultAddress != atFault || c.Status != DisputeFaultChoicePending || asOf > c.Deadline {
		return nil, false
	}
	c.Status = DisputeFaultChoiceDeclined
	return c, true
}

// SweepExpired returns (and marks DEFAULTED) every still-PENDING
// choice whose deadline has passed as of asOf, sorted by OfferID -
// deterministic order matters since the caller applies real side
// effects (a reputation penalty and a forced fee transfer) for each
// one returned, same "every node must visit these in the same order"
// reasoning as applyBlockReward's own sorted delegator walk.
func (s *DisputeFaultChoiceStore) SweepExpired(asOf int64) []*DisputeFaultChoice {
	s.mu.Lock()
	defer s.mu.Unlock()

	var expired []*DisputeFaultChoice
	for _, c := range s.Choices {
		if c.Status == DisputeFaultChoicePending && asOf > c.Deadline {
			c.Status = DisputeFaultChoiceDefaulted
			expired = append(expired, c)
		}
	}
	sort.Slice(expired, func(i, j int) bool { return expired[i].OfferID < expired[j].OfferID })
	return expired
}

// Snapshot/Restore persist choice state across restarts, same pattern
// as every other store's persistence helpers.
func (s *DisputeFaultChoiceStore) Snapshot() map[string]*DisputeFaultChoice {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*DisputeFaultChoice, len(s.Choices))
	for k, v := range s.Choices {
		out[k] = v
	}
	return out
}

func (s *DisputeFaultChoiceStore) Restore(all map[string]*DisputeFaultChoice) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range all {
		s.Choices[k] = v
	}
}
