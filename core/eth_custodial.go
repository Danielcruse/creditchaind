package core

import "sync"

// EthCustodialAccount is a TESTNET-ONLY (Sepolia) custodial Ethereum
// account: CreditChain generates and holds the signing key on the user's
// behalf, unlike every other cross-chain asset in this app (see
// core/hub_storefront.go's BTC-viewer comment for the contrast - that
// side is deliberately non-custodial). This is the one place in the
// entire codebase where a real private key CreditChain doesn't control
// via the user's own wallet signature exists - see audit/THREAT_MODEL.md
// and audit/KNOWN_GAPS.md for the security model this implies, and
// api/eth_signer.go for how the key itself is actually held (AWS KMS in
// production - the key material never enters this process's memory - or
// a clearly-labeled local dev-only fallback when KMS isn't configured).
//
// Scoped to Sepolia testnet ETH only - real (worthless) testnet value,
// same "no real value" framing as CCT/ETHTEST/USDCTEST elsewhere in this
// app. Do not point this at Ethereum mainnet without the licensing/legal
// review flagged in audit/KNOWN_GAPS.md - custodying real mainnet ETH on
// a user's behalf is money transmission in most jurisdictions.
type EthCustodialAccount struct {
	// CreditChainAddress is this account's own wallet address (the one
	// that authenticated the request that created this custodial
	// account) - the key this store is indexed by.
	CreditChainAddress string

	// EthAddress is the real Sepolia address this account controls -
	// derived from the signing key's own public key (KeyRef), never
	// chosen or supplied by the client.
	EthAddress string

	// KeyRef is an opaque reference to the actual signing key - an AWS
	// KMS key ID in production, a local dev-only key ID otherwise (see
	// api/eth_signer.go). Never a raw private key; this store never
	// holds key material itself.
	KeyRef string

	CreatedAt int64
}

type EthCustodialStore struct {
	mu       sync.Mutex
	Accounts map[string]*EthCustodialAccount // keyed by CreditChainAddress
}

func NewEthCustodialStore() *EthCustodialStore {
	return &EthCustodialStore{Accounts: make(map[string]*EthCustodialAccount)}
}

// Create registers a brand-new custodial account - false if this
// CreditChain address already has one, since a second custodial account
// per user is never meant to happen (one signing key per user, created
// once, used for the account's whole lifetime).
func (s *EthCustodialStore) Create(a *EthCustodialAccount) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Accounts[a.CreditChainAddress]; exists {
		return false
	}
	s.Accounts[a.CreditChainAddress] = a
	return true
}

func (s *EthCustodialStore) Get(address string) (*EthCustodialAccount, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Accounts[address]
	return a, ok
}

func (s *EthCustodialStore) Snapshot() map[string]*EthCustodialAccount {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*EthCustodialAccount, len(s.Accounts))
	for k, v := range s.Accounts {
		out[k] = v
	}
	return out
}

func (s *EthCustodialStore) Restore(all map[string]*EthCustodialAccount) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range all {
		s.Accounts[k] = v
	}
}
