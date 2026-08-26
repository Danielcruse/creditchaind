package core

import "sync"

// UserNotification is the general-purpose counterpart to
// HubNotification (core/hub_notification.go) - that one is
// hub-only (keyed by HubAddress, always "TRADE_REQUEST"); this one is
// addressed to any wallet and carries a real Type so the frontend can
// render each kind differently (trade started, new chat message, login
// activity, REPT minted, ...). Same node-local, HTTP-request-time,
// not-block-confirmed reasoning as HubNotification - purely
// informational, never itself a source of truth for balances/state.
type UserNotification struct {
	ID        string
	Address   string
	Type      string
	Message   string
	RelatedID string
	Read      bool
	CreatedAt int64
}

type UserNotificationStore struct {
	mu        sync.Mutex
	byAddress map[string][]*UserNotification
	byID      map[string]*UserNotification
}

func NewUserNotificationStore() *UserNotificationStore {
	return &UserNotificationStore{
		byAddress: make(map[string][]*UserNotification),
		byID:      make(map[string]*UserNotification),
	}
}

func (s *UserNotificationStore) Add(n *UserNotification) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byAddress[n.Address] = append(s.byAddress[n.Address], n)
	s.byID[n.ID] = n
}

// ListFor returns every notification ever sent to address, newest
// first - same "nothing disappears" convention as
// NotificationStore.ListFor.
func (s *UserNotificationStore) ListFor(address string) []*UserNotification {
	s.mu.Lock()
	defer s.mu.Unlock()

	src := s.byAddress[address]
	out := make([]*UserNotification, len(src))
	for i, n := range src {
		out[len(src)-1-i] = n
	}
	return out
}

func (s *UserNotificationStore) MarkRead(address, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.byID[id]
	if !ok || n.Address != address {
		return false
	}
	n.Read = true
	return true
}

func (s *UserNotificationStore) UnreadCount(address string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, n := range s.byAddress[address] {
		if !n.Read {
			count++
		}
	}
	return count
}

func (s *UserNotificationStore) Snapshot() []*UserNotification {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*UserNotification, 0, len(s.byID))
	for _, n := range s.byID {
		out = append(out, n)
	}
	return out
}

func (s *UserNotificationStore) Restore(all []*UserNotification) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, n := range all {
		s.byAddress[n.Address] = append(s.byAddress[n.Address], n)
		s.byID[n.ID] = n
	}
}
