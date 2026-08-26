package core

import "sync"

// ModeratorVerificationEntry is one append-only record of a moderator's
// certificate-review decision on a Trading Hub application - written
// alongside the existing company-review status update (see
// applyCompanyReview in api/apply.go), never mutated afterward. This is
// the "ledger in governance where top executive can view each
// moderator's log" the Phase 4 plan calls for - a moderator's own
// review history isn't visible anywhere else, so this is the only
// record of it.
type ModeratorVerificationEntry struct {
	ID               string
	ModeratorAddress string
	CompanyAddress   string
	CertificateHash  string
	Approved         bool
	Timestamp        int64
}

type ModeratorVerificationLog struct {
	mu          sync.Mutex
	byModerator map[string][]*ModeratorVerificationEntry
	all         []*ModeratorVerificationEntry
}

func NewModeratorVerificationLog() *ModeratorVerificationLog {
	return &ModeratorVerificationLog{
		byModerator: make(map[string][]*ModeratorVerificationEntry),
	}
}

func (l *ModeratorVerificationLog) Append(e *ModeratorVerificationEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.byModerator[e.ModeratorAddress] = append(l.byModerator[e.ModeratorAddress], e)
	l.all = append(l.all, e)
}

// ForModerator returns every entry ever recorded for moderator, newest
// first - same "nothing disappears, newest first" convention as
// NotificationStore.ListFor.
func (l *ModeratorVerificationLog) ForModerator(moderator string) []*ModeratorVerificationEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	src := l.byModerator[moderator]
	out := make([]*ModeratorVerificationEntry, len(src))
	for i, e := range src {
		out[len(src)-1-i] = e
	}
	return out
}

// Snapshot/Restore support persistence.go saving/loading the whole log
// across a restart, same pattern as every other store in this codebase.
func (l *ModeratorVerificationLog) Snapshot() []*ModeratorVerificationEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]*ModeratorVerificationEntry, len(l.all))
	copy(out, l.all)
	return out
}

func (l *ModeratorVerificationLog) Restore(all []*ModeratorVerificationEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, e := range all {
		l.byModerator[e.ModeratorAddress] = append(l.byModerator[e.ModeratorAddress], e)
		l.all = append(l.all, e)
	}
}
