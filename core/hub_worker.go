package core

import "sync"

// HubWorker is a wallet a verified Trading Hub has delegated to act on
// its behalf for day-to-day pricing - "customer care" staff who can
// post/update the hub's rates without ever holding the hub's own
// private key. Only the hub's own wallet can add or remove a worker
// (see api/apply.go's applyHubWorkerAdd/Remove) - a worker can never
// add itself, and can never delegate further.
type HubWorker struct {
	HubAddress      string
	WorkerAddress   string
	WorkerPublicKey string
	Name            string
	Active          bool
	AddedAt         int64
}

func hubWorkerKey(hub, worker string) string {
	return hub + ":" + worker
}

type HubWorkerStore struct {
	mu      sync.Mutex
	Workers map[string]*HubWorker // keyed by hubWorkerKey(hub, worker)
}

func NewHubWorkerStore() *HubWorkerStore {
	return &HubWorkerStore{Workers: make(map[string]*HubWorker)}
}

// Add posts or re-activates a worker record - always the whole
// record, same "no partial-field update" convention as HubRateStore.Set.
func (s *HubWorkerStore) Add(w *HubWorker) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Workers[hubWorkerKey(w.HubAddress, w.WorkerAddress)] = w
}

// Remove deactivates a worker rather than deleting the record, so a
// hub's own history of who it once delegated to stays visible - same
// "nothing disappears" convention as every other store in this app.
func (s *HubWorkerStore) Remove(hub, worker string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.Workers[hubWorkerKey(hub, worker)]
	if !ok {
		return false
	}
	w.Active = false
	return true
}

// IsActiveWorker reports whether worker is currently an active,
// non-revoked delegate of hub - the actual authorization check every
// worker-capable action (currently just posting/updating rates) uses.
func (s *HubWorkerStore) IsActiveWorker(hub, worker string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.Workers[hubWorkerKey(hub, worker)]
	return ok && w.Active
}

// ListForHub returns every worker a hub has ever added, active or not,
// for the hub's own team-management view.
func (s *HubWorkerStore) ListForHub(hub string) []*HubWorker {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]*HubWorker, 0)
	for _, w := range s.Workers {
		if w.HubAddress == hub {
			list = append(list, w)
		}
	}
	return list
}

// HubForWorker is the reverse lookup: given a wallet address, which
// verified hub (if any) is it a currently-active operator/worker of.
// Needed wherever an action is being authorized/attributed by the
// ACTOR's address alone (e.g. creating a trade under their own
// operator identity) and the caller doesn't already know which hub
// that operator belongs to.
func (s *HubWorkerStore) HubForWorker(worker string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, w := range s.Workers {
		if w.WorkerAddress == worker && w.Active {
			return w.HubAddress, true
		}
	}
	return "", false
}

func (s *HubWorkerStore) Snapshot() map[string]*HubWorker {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*HubWorker, len(s.Workers))
	for k, v := range s.Workers {
		out[k] = v
	}
	return out
}

func (s *HubWorkerStore) Restore(all map[string]*HubWorker) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range all {
		s.Workers[k] = v
	}
}
