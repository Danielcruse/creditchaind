package core

import "sync"

// ModeratorStatus is a reputation-vetted community member who gets
// automatically assigned to observe and attest to trade agreements
// (loan negotiations and P2P offers) as they conclude. This is a
// disclosed, human-trust-based attestation layer - a moderator
// confirming a trade succeeded is a judgment call, not a
// cryptographic guarantee that any off-app payment actually happened
// (the same category of limitation as a slash report's missed-block
// case in slashing.go: trust is spent somewhere, but at least it's
// spent transparently and the outcome is replicated identically to
// every node once confirmed).
type ModeratorStatus struct {
	Address      string
	PublicKey    string
	Confirmations uint64
	Disputes      uint64
	RegisteredAt  int64
}

// ModeratorReputationBonus is the one-time reputation increase for
// registering as a moderator - since reputation is 55% of PoSR
// validator selection weight (see consensus.go's validatorWeight),
// this mechanically increases a moderator's validator power too,
// without needing a second, separate "moderator power" mechanism.
const ModeratorReputationBonus = 5

// ModeratorConfirmReputationBonus rewards a moderator's own reputation
// a small amount each time they actually do the work of confirming a
// trade - so a moderator's standing keeps growing with real activity,
// not just the one-time registration bonus.
const ModeratorConfirmReputationBonus = 1

type ModeratorStore struct {
	mu         sync.Mutex
	Moderators map[string]*ModeratorStatus

	// order/next implement round-robin assignment - preserved so trade
	// oversight load spreads evenly across registered moderators rather
	// than piling onto whichever one registered first.
	order []string
	next  int
}

func NewModeratorStore() *ModeratorStore {
	return &ModeratorStore{Moderators: make(map[string]*ModeratorStatus)}
}

// Register adds a new moderator - a no-op (returns false) if this
// address already registered, since re-registering shouldn't grant a
// second reputation bonus or a second slot in the round-robin order.
func (s *ModeratorStore) Register(address, publicKey string, at int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Moderators[address]; exists {
		return false
	}

	s.Moderators[address] = &ModeratorStatus{
		Address:      address,
		PublicKey:    publicKey,
		RegisteredAt: at,
	}
	s.order = append(s.order, address)
	return true
}

func (s *ModeratorStore) Get(address string) (*ModeratorStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.Moderators[address]
	return m, ok
}

func (s *ModeratorStore) IsModerator(address string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.Moderators[address]
	return ok
}

// List returns every registered moderator - stable order not
// guaranteed, callers that need round-robin order use AssignNext
// instead.
func (s *ModeratorStore) List() []*ModeratorStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]*ModeratorStatus, 0, len(s.Moderators))
	for _, m := range s.Moderators {
		list = append(list, m)
	}
	return list
}

// AssignNext round-robins across registered moderators, advancing the
// shared cursor - called only from block-confirmation-time apply
// logic (never directly from an HTTP handler) so every node that
// applies the same sequence of transactions reaches the identical
// assignment. Returns "" if nobody has registered yet - a disclosed
// gap on a brand new network with zero moderators, where a trade
// simply goes unassigned until someone registers.
func (s *ModeratorStore) AssignNext() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.order) == 0 {
		return ""
	}

	addr := s.order[s.next%len(s.order)]
	s.next++
	return addr
}

// RecordConfirmation updates a moderator's own track record - visible
// on their profile so counterparties can weigh a moderator's history
// before relying on their assignment (a confirm-heavy vs. a
// dispute-heavy record means very different things).
func (s *ModeratorStore) RecordConfirmation(address string, disputed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.Moderators[address]
	if !ok {
		return
	}
	if disputed {
		m.Disputes++
	} else {
		m.Confirmations++
	}
}

// Snapshot/Restore persist moderator state (including the round-robin
// cursor) across restarts, same pattern as every other store's
// persistence helpers.
type ModeratorSnapshot struct {
	Moderators []*ModeratorStatus
	Order      []string
	Next       int
}

func (s *ModeratorStore) Snapshot() ModeratorSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]*ModeratorStatus, 0, len(s.Moderators))
	for _, m := range s.Moderators {
		list = append(list, m)
	}
	return ModeratorSnapshot{Moderators: list, Order: append([]string{}, s.order...), Next: s.next}
}

func (s *ModeratorStore) Restore(snap ModeratorSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, m := range snap.Moderators {
		s.Moderators[m.Address] = m
	}
	s.order = append([]string{}, snap.Order...)
	s.next = snap.Next
}
