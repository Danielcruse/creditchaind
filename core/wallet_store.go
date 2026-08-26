package core

// WalletStore is the canonical, address-keyed registry of Wallets.
// Per the CreditChain "one owner per concept" rule, Wallet is the
// single source of truth for an address's trust score (Trust's six
// dimensions post-TrustDimensionsCutoverUnixSeconds, the flat
// Reputation field before it - see trust.go/reputation.go) and (via
// StakingStore sync) its StakedETH balance. Every other subsystem
// (Validator, CreditProfile, staking handlers, etc.) should read
// reputation through here rather than keeping its own copy.
type WalletStore struct {
	Wallets map[string]*Wallet
}

func NewWalletStore() *WalletStore {
	return &WalletStore{
		Wallets: make(map[string]*Wallet),
	}
}

// Get returns the wallet for an address, creating a fresh one
// (Reputation starting at 50, per existing convention) if this is
// the first time the address has been seen.
func (ws *WalletStore) Get(address string) *Wallet {
	w, ok := ws.Wallets[address]
	if ok {
		return w
	}

	nw := NewWallet()
	nw.Address = address
	ws.Wallets[address] = &nw

	return &nw
}

// Register inserts an already-constructed wallet (e.g. one created
// via CreateWallet() at wallet-creation time) into the store under
// its own address, without overwriting an existing entry.
func (ws *WalletStore) Register(w *Wallet) {
	if _, exists := ws.Wallets[w.Address]; exists {
		return
	}
	ws.Wallets[w.Address] = w
}
