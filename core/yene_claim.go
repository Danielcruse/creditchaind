package core

import "sync"

// YeneClaimStore tracks the last time each address claimed testnet
// YEN-e via api/yene_claim.go - a time-cooldown gate, unlike the
// faucet's one-shot-forever seen-map, so it's its own small store
// rather than reusing that one.
type YeneClaimStore struct {
	mu            sync.Mutex
	LastClaimedAt map[string]int64
}

func NewYeneClaimStore() *YeneClaimStore {
	return &YeneClaimStore{LastClaimedAt: make(map[string]int64)}
}

// CanClaim reports whether address's last claim (if any) is more than
// cooldownSeconds before asOf.
func (s *YeneClaimStore) CanClaim(address string, asOf int64, cooldownSeconds int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	last, ok := s.LastClaimedAt[address]
	if !ok {
		return true
	}
	return asOf-last >= cooldownSeconds
}

// NextClaimAt returns when address may next claim (0 if it never has).
func (s *YeneClaimStore) NextClaimAt(address string, cooldownSeconds int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	last, ok := s.LastClaimedAt[address]
	if !ok {
		return 0
	}
	return last + cooldownSeconds
}

// RecordClaim stamps address's last-claimed time - called only after
// CanClaim has already been re-checked at apply time.
func (s *YeneClaimStore) RecordClaim(address string, asOf int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastClaimedAt[address] = asOf
}
