package core

import "sync"

// Peer is another node this one has handshaken with. Address is the
// peer's own HTTP base URL (e.g. "http://localhost:8082") - this
// codebase runs P2P messages over the same HTTP server as the public
// API, just on /peer/* routes, rather than a separate transport.
type Peer struct {
	ID string

	Address string

	PublicKey string

	// FailedAttempts counts consecutive failed/invalid interactions
	// (unreachable, or sent a chain that failed verification) - reset
	// to 0 by any successful one. Once it crosses MaxPeerFailures,
	// Suspended is set and this peer is excluded from ActiveList until
	// it either succeeds again on a retry or re-handshakes fresh -
	// a peer that's unreachable or sending bad data is actively
	// harmful to keep gossiping to (wasted sends) or syncing from
	// (risk of adopting garbage), not just unhelpful.
	FailedAttempts int
	Suspended      bool
}

// MaxPeerFailures is how many consecutive bad interactions a peer
// gets before being suspended from active gossip/sync.
const MaxPeerFailures = 5

// PeerStore is the canonical, shared registry of known peers.
type PeerStore struct {
	mu    sync.Mutex
	Peers map[string]*Peer // keyed by Address
}

func NewPeerStore() *PeerStore {
	return &PeerStore{Peers: make(map[string]*Peer)}
}

// Add (re-)registers a peer, always in good standing - a fresh
// handshake (the only caller of Add) is itself proof of successful
// contact, so this is also how a previously-suspended peer recovers:
// re-handshaking replaces its whole record, including
// FailedAttempts/Suspended back to zero/false.
func (ps *PeerStore) Add(p Peer) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.Peers[p.Address] = &p
}

// Get looks up a single known peer by address, regardless of
// suspension status - used by discovery to check "do we already know
// about this address" before trying a fresh handshake with it.
func (ps *PeerStore) Get(address string) (Peer, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	p, ok := ps.Peers[address]
	if !ok {
		return Peer{}, false
	}
	return *p, true
}

// List returns every known peer, including suspended ones - used
// where "give every known address a chance" is the right behavior
// (e.g. periodic re-handshake, which is exactly how a suspended peer
// gets a chance to recover).
func (ps *PeerStore) List() []Peer {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	list := make([]Peer, 0, len(ps.Peers))
	for _, p := range ps.Peers {
		list = append(list, *p)
	}
	return list
}

// ActiveList returns only non-suspended peers - what gossip and chain
// sync should actually trust and send to, as opposed to List's full
// membership (which includes peers currently being given a chance to
// recover, not ones it's safe to rely on right now).
func (ps *PeerStore) ActiveList() []Peer {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	list := make([]Peer, 0, len(ps.Peers))
	for _, p := range ps.Peers {
		if !p.Suspended {
			list = append(list, *p)
		}
	}
	return list
}

// RecordFailure and RecordSuccess track a peer's recent reliability -
// see MaxPeerFailures. Both are no-ops for an address this store
// doesn't know about (nothing to update).
func (ps *PeerStore) RecordFailure(address string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	p, ok := ps.Peers[address]
	if !ok {
		return
	}

	p.FailedAttempts++
	if p.FailedAttempts >= MaxPeerFailures {
		p.Suspended = true
	}
}

func (ps *PeerStore) RecordSuccess(address string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	p, ok := ps.Peers[address]
	if !ok {
		return
	}

	p.FailedAttempts = 0
	p.Suspended = false
}
