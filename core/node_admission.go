package core

import "sync"

// NodeAdmission records a governance-approved grant admitting one node
// identity to the peer gossip network - see ProposalKindNodeAdmission
// and api/node_admission_governance.go's applyNodeAdmissionFromProposal,
// the ONLY writer. Deliberately keyed by PublicKey, not NodeID: a
// handshake's verifyHandshakeSignature (api/peer.go) already proves
// possession of the public key, while NodeID is an unauthenticated,
// operator-chosen cosmetic label - the same reason verifyBlockProducer
// never trusts a peer's self-asserted NodeID for anything
// consensus-critical.
type NodeAdmission struct {
	PublicKey      string // hex - this store's key, matches handshakeMsg.PublicKey's own encoding
	NodeID         string // cosmetic label carried alongside, never itself authenticated
	SponsorAddress string // the CreditChain wallet that proposed this admission - permanent on-chain attribution NETWORK_SECRET never had
	ProposalID     string // traceability to the exact passed vote that granted this
	GrantedAt      int64  // deterministic tx/block timestamp, never wall-clock
	// Grandfathered is true ONLY for entries seeded once at the
	// cutover from NETWORK_SECRET-only admission (see
	// SeedGrandfatheredNodeAdmissions) - kept visible/auditable so a
	// grandfathered entry is never indistinguishable from a real,
	// governance-passed grant.
	Grandfathered bool
}

// NodeAdmissionStore is deliberately NOT time-limited/expiring - see
// ProposalKindNodeAdmission's own doc comment for why a permanent grant
// was chosen over an expiring one.
type NodeAdmissionStore struct {
	mu     sync.Mutex
	Grants map[string]*NodeAdmission // keyed by PublicKey hex
}

func NewNodeAdmissionStore() *NodeAdmissionStore {
	return &NodeAdmissionStore{Grants: make(map[string]*NodeAdmission)}
}

// Grant records a new admission - called exclusively from
// applyNodeAdmissionFromProposal (a passed NODE_ADMISSION proposal) or
// SeedGrandfatheredNodeAdmissions (the one-time cutover seed), never
// from any request-handling code path directly. Re-granting an
// already-admitted public key is a harmless no-op (first grant wins),
// matching BridgeValidatorStore.Set's idempotent-registration
// convention.
func (s *NodeAdmissionStore) Grant(a *NodeAdmission) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Grants[a.PublicKey]; exists {
		return
	}
	s.Grants[a.PublicKey] = a
}

func (s *NodeAdmissionStore) IsAdmitted(publicKeyHex string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.Grants[publicKeyHex]
	return ok
}

func (s *NodeAdmissionStore) Get(publicKeyHex string) (*NodeAdmission, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Grants[publicKeyHex]
	return a, ok
}

func (s *NodeAdmissionStore) Snapshot() map[string]*NodeAdmission {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*NodeAdmission, len(s.Grants))
	for k, v := range s.Grants {
		out[k] = v
	}
	return out
}

func (s *NodeAdmissionStore) Restore(all map[string]*NodeAdmission) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range all {
		s.Grants[k] = v
	}
}

// SeedGrandfatheredNodeAdmissions is called once at process startup on
// EVERY node, with the same hard-coded list compiled into every binary
// (see the doc comment on Grandfathered above and Phase 2 of the
// node-admission plan) - deliberately NOT derived from any node's own
// local PeerStore/PEERS config, since different nodes have different
// local peer lists and per-node seeding would make NodeAdmissionStore
// diverge node-to-node, breaking the "identical replicated state
// everywhere" property this store depends on. A no-op for any entry
// already granted (e.g. a real NODE_ADMISSION proposal that later
// re-admits the same key), matching Grant's own idempotency.
func (s *NodeAdmissionStore) SeedGrandfatheredNodeAdmissions(entries []NodeAdmission) {
	for i := range entries {
		entries[i].Grandfathered = true
		s.Grant(&entries[i])
	}
}
