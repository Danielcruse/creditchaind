package core

import "sync"

// HubStorefrontMaxBytes bounds a hub's custom front-page design - big
// enough for a real hand-built card (some CSS, a logo data-URI, a
// little layout), small enough that this can't become a way to stuff
// arbitrary large payloads into every node's persisted state.
const HubStorefrontMaxBytes = 20000

// HubStorefront is a verified Trading Hub's own custom HTML/CSS (and,
// if they want, JS) for the "front page" corner of their hub profile -
// entirely their own design, rendered by every visitor's browser. This
// is genuinely arbitrary, untrusted markup from a third party, so it
// is NEVER rendered directly into the page DOM - see
// website/js/p2p.js's renderHubStorefront, which loads it into a
// sandboxed iframe (sandbox="allow-scripts" only, deliberately without
// allow-same-origin) so a hub's design can run its own JS for its own
// presentation but has no way to reach this site's origin, cookies, or
// localStorage - specifically, no way to ever read a visitor's wallet
// keys out of localStorage. That isolation is the whole point of this
// feature being safe to ship at all.
type HubStorefront struct {
	HubAddress string
	HTML       string
	UpdatedAt  int64
}

type HubStorefrontStore struct {
	mu          sync.Mutex
	Storefronts map[string]*HubStorefront // keyed by HubAddress
}

func NewHubStorefrontStore() *HubStorefrontStore {
	return &HubStorefrontStore{Storefronts: make(map[string]*HubStorefront)}
}

func (s *HubStorefrontStore) Set(hub, html string, at int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Storefronts[hub] = &HubStorefront{HubAddress: hub, HTML: html, UpdatedAt: at}
}

func (s *HubStorefrontStore) Get(hub string) (*HubStorefront, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, ok := s.Storefronts[hub]
	return sf, ok
}

func (s *HubStorefrontStore) Snapshot() map[string]*HubStorefront {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*HubStorefront, len(s.Storefronts))
	for k, v := range s.Storefronts {
		out[k] = v
	}
	return out
}

func (s *HubStorefrontStore) Restore(all map[string]*HubStorefront) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range all {
		s.Storefronts[k] = v
	}
}
