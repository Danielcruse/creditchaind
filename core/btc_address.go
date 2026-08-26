package core

import "sync"

// BtcWatchedAddress records which self-custodial BTC testnet address
// (see website/js/hdwallet.js's deriveBtcTestnetAddress) a wallet has
// told this node to watch for deposits - unlike EthCustodialAccount,
// CreditChain never generates or holds this address's private key; the
// user's own browser derived it from their HD mnemonic, and only the
// public address (already visible on the Assets/Receive page) is ever
// sent here. This is what lets api/btc_bridge.go's deposit-detection
// loop know which address to poll for each wallet.
type BtcWatchedAddress struct {
	// CreditChainAddress is this record's key - the wallet that
	// registered the BTC address.
	CreditChainAddress string
	// CreditChainPublicKey is the hex-encoded public key behind
	// CreditChainAddress, captured from the signed request that
	// registered this address (never trusted from anywhere else, same
	// reasoning as EthCustodialAccount.CreditChainPublicKey) - needed
	// to compute this wallet's BTCB sub-address via
	// core.DeriveTokenAddress when a deposit is credited (see
	// api/apply.go's applyBridgeBtcDeposit), since the crediting
	// transaction itself is node-authored (its own AuthPublicKey is the
	// REPORTING NODE's key, not the depositor's).
	CreditChainPublicKey string
	// BtcAddress is the bech32 testnet (tb1...) address being watched -
	// supplied by the client, but always re-derivable by the user
	// themselves from their own mnemonic, so a wrong/malicious value
	// here can only ever misdirect crediting to the WRONG address (the
	// submitter's own BTCB balance would simply never update), never
	// let anyone claim funds they don't control - real BTC only ever
	// moves if the depositor sends to this exact string themselves.
	BtcAddress string
	// LastCreditedSats is the highest real testnet BTC balance this
	// address has already been credited for (see BTCB's deposit-
	// detection loop) - mirrors EthCustodialAccount.LastCreditedWei's
	// role exactly, just as a plain uint64 since satoshis never
	// overflow a uint64 the way wei can approach one for large ETH
	// amounts.
	LastCreditedSats uint64
	CreatedAt        int64

	// UncreditedExcessSats accumulates (never overwritten) the portion
	// of any deposit that would have pushed this account's total
	// BTCB+ETHB USD holdings over the admin-set
	// "AccountHoldingsCapUsdCents" limit at the time it was observed -
	// see api/apply.go's applyBridgeBtcDeposit and
	// api/account_holdings_cap.go. Same role as
	// EthCustodialAccount.UncreditedExcessWei - if the admin hasn't set
	// both EthUsdCentsPerEth/BtcUsdCentsPerBtc rates, this never
	// accumulates anything; real sats this address genuinely received
	// but only partially credited, recoverable via manual admin
	// follow-up (GET /admin/bridge/excess-deposits), not lost.
	UncreditedExcessSats uint64
}

type BtcAddressStore struct {
	mu        sync.Mutex
	Addresses map[string]*BtcWatchedAddress // keyed by CreditChainAddress
}

func NewBtcAddressStore() *BtcAddressStore {
	return &BtcAddressStore{Addresses: make(map[string]*BtcWatchedAddress)}
}

// Set registers or replaces which BTC address a wallet is watching -
// replacing is allowed (unlike EthCustodialStore.Create's one-time
// semantics) since there's no key-provisioning cost here, just a
// pointer update; re-registering the same address is a harmless no-op
// for the deposit loop's own baseline tracking.
func (s *BtcAddressStore) Set(a *BtcWatchedAddress) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.Addresses[a.CreditChainAddress]; ok && existing.BtcAddress == a.BtcAddress {
		return // same address already registered - keep the existing LastCreditedSats baseline
	}
	s.Addresses[a.CreditChainAddress] = a
}

func (s *BtcAddressStore) Get(address string) (*BtcWatchedAddress, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, ok := s.Addresses[address]
	return a, ok
}

func (s *BtcAddressStore) Snapshot() map[string]*BtcWatchedAddress {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*BtcWatchedAddress, len(s.Addresses))
	for k, v := range s.Addresses {
		out[k] = v
	}
	return out
}

func (s *BtcAddressStore) Restore(all map[string]*BtcWatchedAddress) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range all {
		s.Addresses[k] = v
	}
}
