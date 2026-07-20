package core

import "sync"

// Announcement is an admin-posted, site-wide message - shown to
// every user (outernet.html banner) and on the public governance
// feed, distinct from GovernanceStatus (which is posted by any
// governance-activated wallet, not just the operator). Same simple
// node-local-store-fed-by-a-replicated-transaction shape as
// GovernanceStatus.
type Announcement struct {
	ID        string
	Title     string
	Text      string
	CreatedAt int64
}

type AnnouncementStore struct {
	mu    sync.Mutex
	byID  map[string]*Announcement
	order []string // insertion order, oldest first
}

func NewAnnouncementStore() *AnnouncementStore {
	return &AnnouncementStore{byID: make(map[string]*Announcement)}
}

func (s *AnnouncementStore) Add(a *Announcement) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byID[a.ID] = a
	s.order = append(s.order, a.ID)
}

// Recent returns up to n most-recently-posted announcements, newest
// first.
func (s *AnnouncementStore) Recent(n int) []*Announcement {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*Announcement, 0, n)
	for i := len(s.order) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, s.byID[s.order[i]])
	}
	return out
}

// Snapshot/Restore support persistence.go saving/loading every
// announcement across a restart, in original post order.
func (s *AnnouncementStore) Snapshot() []*Announcement {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*Announcement, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.byID[id])
	}
	return out
}

func (s *AnnouncementStore) Restore(all []*Announcement) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, a := range all {
		s.byID[a.ID] = a
		s.order = append(s.order, a.ID)
	}
}
