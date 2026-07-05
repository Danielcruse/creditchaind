package core

import "sync"

// HubNotification is a purely informational alert to a Trading Hub
// that a user started (not necessarily finished) a buy/sell request
// against one of its rates - like NegotiationMessage/P2POffer.Messages,
// this carries no economic value itself (the real trade is a separate,
// block-confirmed transaction), so it's node-local API state rather
// than something ApplyBlock needs to replicate identically. Recorded
// at HTTP-request time (see api/hub_rate.go's HandleRequestHubTrade),
// not gated behind block confirmation, so a hub sees it the instant a
// trader clicks Buy/Sell rather than after the next block.
type HubNotification struct {
	ID         string
	HubAddress string
	// Type is currently always "TRADE_REQUEST" - a distinct field
	// (rather than inferring from Message text) so a future
	// notification kind can be added without changing how existing
	// ones are read.
	Type      string
	Message   string
	OfferID   string
	Read      bool
	CreatedAt int64
}

type NotificationStore struct {
	mu    sync.Mutex
	byHub map[string][]*HubNotification
	byID  map[string]*HubNotification
}

func NewNotificationStore() *NotificationStore {
	return &NotificationStore{
		byHub: make(map[string][]*HubNotification),
		byID:  make(map[string]*HubNotification),
	}
}

func (s *NotificationStore) Add(n *HubNotification) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byHub[n.HubAddress] = append(s.byHub[n.HubAddress], n)
	s.byID[n.ID] = n
}

// ListFor returns every notification ever sent to hub, newest first -
// read-only history, same "nothing disappears" convention as every
// other List in this codebase (e.g. P2POfferStore.List).
func (s *NotificationStore) ListFor(hub string) []*HubNotification {
	s.mu.Lock()
	defer s.mu.Unlock()

	src := s.byHub[hub]
	out := make([]*HubNotification, len(src))
	for i, n := range src {
		out[len(src)-1-i] = n
	}
	return out
}

// MarkRead flips one notification to read - only the hub it was
// addressed to may do this, mirroring the "only the party concerned"
// gate every other one-shot state flip in this codebase uses (e.g.
// NegotiationStore.Cancel).
func (s *NotificationStore) MarkRead(hub, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.byID[id]
	if !ok || n.HubAddress != hub {
		return false
	}
	n.Read = true
	return true
}

func (s *NotificationStore) UnreadCount(hub string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, n := range s.byHub[hub] {
		if !n.Read {
			count++
		}
	}
	return count
}

// Snapshot/Restore support persistence.go saving/loading every
// notification across a restart, same pattern as every other store's
// snapshot method in this codebase.
func (s *NotificationStore) Snapshot() []*HubNotification {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*HubNotification, 0, len(s.byID))
	for _, n := range s.byID {
		out = append(out, n)
	}
	return out
}

func (s *NotificationStore) Restore(all []*HubNotification) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, n := range all {
		s.byHub[n.HubAddress] = append(s.byHub[n.HubAddress], n)
		s.byID[n.ID] = n
	}
}
