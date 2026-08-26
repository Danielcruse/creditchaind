package core

import "sync"

// BridgeValidator is a sibling registry to AdminMultisigStore
// (core/admin_multisig.go), deliberately NOT the same signer set. Admin
// multisig authenticates internal governance actions (mint/treasury/
// param changes) with a plain CreditChain Ed25519 signature. Vault
// custody needs a SECOND, distinct cryptographic credential per
// signer - a real secp256k1 key the ETH vault contract can ecrecover,
// and a real secp256k1 key for a Bitcoin P2WSH witness - neither of
// which the admin-multisig boolean Approvals map can carry. Reusing
// ADMIN_MULTISIG_ADDRESSES for vault release would also silently
// expand the blast radius of routine internal-governance keys into
// real-value external custody keys, which need materially stricter
// operational hygiene. Same humans can staff both registries - that's
// an operator choice, not something this code enforces.
type BridgeValidator struct {
	// CreditChainAddress is this record's key - the validator's own
	// CreditChain wallet, already gated by BRIDGE_VALIDATOR_ADDRESSES
	// (see api/bridge_validator.go) before registration is even
	// accepted.
	CreditChainAddress string
	// EthSigningAddress is the real 0x... address whose key will be
	// registered as a vault contract "owner" for ecrecover-based
	// release signature verification (see contracts/CreditChainVault.sol).
	EthSigningAddress string
	// BtcPublicKeyHex is the compressed secp256k1 public key used in
	// the P2WSH multisig redeem script (see core/btc_vault.go).
	BtcPublicKeyHex string
	RegisteredAt    int64
}

type BridgeValidatorStore struct {
	mu         sync.Mutex
	Validators map[string]*BridgeValidator // keyed by CreditChainAddress
}

func NewBridgeValidatorStore() *BridgeValidatorStore {
	return &BridgeValidatorStore{Validators: make(map[string]*BridgeValidator)}
}

// Set registers or replaces a bridge validator's declared external-chain
// keys - replacing is allowed (a validator may need to rotate keys),
// re-registering the identical keys is a harmless no-op.
func (s *BridgeValidatorStore) Set(v *BridgeValidator) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.Validators[v.CreditChainAddress]; ok &&
		existing.EthSigningAddress == v.EthSigningAddress &&
		existing.BtcPublicKeyHex == v.BtcPublicKeyHex {
		return
	}
	s.Validators[v.CreditChainAddress] = v
}

func (s *BridgeValidatorStore) Get(address string) (*BridgeValidator, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.Validators[address]
	return v, ok
}

// Snapshot returns every registered bridge validator - used by contract
// deployment/setValidators tooling and by the P2WSH redeem-script
// builder (core/btc_vault.go), both of which need the full, current
// signer set.
func (s *BridgeValidatorStore) Snapshot() map[string]*BridgeValidator {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*BridgeValidator, len(s.Validators))
	for k, v := range s.Validators {
		out[k] = v
	}
	return out
}

func (s *BridgeValidatorStore) Restore(all map[string]*BridgeValidator) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range all {
		s.Validators[k] = v
	}
}
