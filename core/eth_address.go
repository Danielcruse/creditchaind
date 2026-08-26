package core

import "sync"

// EthWatchedAddress mirrors BtcWatchedAddress (see core/btc_address.go)
// exactly, for the same reason: this node never generates or holds this
// address's private key. The user's own browser derived it from their
// HD mnemonic (see website/js/hdwallet.js's deriveEthAddress), and only
// the public address (already visible on the Assets/Receive page) is
// ever sent here. This is what lets api/eth_bridge.go's deposit-
// detection loop know which address to poll for each wallet, without
// the paid AWS-KMS-backed custodial signer (core.EthCustodialAccount)
// being involved at all - a real ETH deposit to a user's own free
// self-custodial address is credited as ETHB the same way a BTC
// deposit already is, no separate paid address required.
type EthWatchedAddress struct {
	// CreditChainAddress is this record's key - the wallet that
	// registered the ETH address.
	CreditChainAddress string
	// CreditChainPublicKey is the hex-encoded public key behind
	// CreditChainAddress, captured from the signed request that
	// registered this address - needed to compute this wallet's ETHB
	// sub-address via resolveBalanceAddress when a deposit is credited,
	// since the crediting transaction itself is node-authored (its own
	// AuthPublicKey is the REPORTING NODE's key, not the depositor's).
	CreditChainPublicKey string
	// EthAddress is the real 0x... Ethereum address being watched -
	// supplied by the client, but always re-derivable by the user
	// themselves from their own mnemonic, so a wrong/malicious value
	// here can only ever misdirect crediting to the WRONG address (the
	// submitter's own ETHB balance would simply never update), never
	// let anyone claim funds they don't control - real ETH only ever
	// moves if the depositor sends to this exact address themselves.
	EthAddress string
	// LastCreditedWei is the highest real on-chain balance this address
	// has already been credited for - mirrors
	// EthCustodialAccount.LastCreditedWei's role exactly, decimal string
	// since wei amounts can exceed a plain uint64/JSON-number range.
	LastCreditedWei string
	CreatedAt       int64
	// UncreditedExcessWei mirrors EthCustodialAccount's field of the same
	// name exactly - see applyBridgeDeposit's doc comment in api/apply.go.
	UncreditedExcessWei string
}

type EthAddressStore struct {
	mu        sync.Mutex
	Addresses map[string]*EthWatchedAddress // keyed by CreditChainAddress
}

func NewEthAddressStore() *EthAddressStore {
	return &EthAddressStore{Addresses: make(map[string]*EthWatchedAddress)}
}

// Set registers or replaces which ETH address a wallet is watching -
// replacing is allowed (no key-provisioning cost here, just a pointer
// update); re-registering the same address is a harmless no-op that
// preserves the existing LastCreditedWei baseline.
func (s *EthAddressStore) Set(a *EthWatchedAddress) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.Addresses[a.CreditChainAddress]; ok && existing.EthAddress == a.EthAddress {
		return
	}
	s.Addresses[a.CreditChainAddress] = a
}

func (s *EthAddressStore) Get(address string) (*EthWatchedAddress, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Addresses[address]
	return a, ok
}

func (s *EthAddressStore) Snapshot() map[string]*EthWatchedAddress {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*EthWatchedAddress, len(s.Addresses))
	for k, v := range s.Addresses {
		out[k] = v
	}
	return out
}

func (s *EthAddressStore) Restore(all map[string]*EthWatchedAddress) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range all {
		s.Addresses[k] = v
	}
}
