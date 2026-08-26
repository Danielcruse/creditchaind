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
// Sepolia testnet ETH by default - real (worthless) testnet value, same
// "no real value" framing as CCT/ETHTEST/USDCTEST elsewhere in this app.
// A mainnet instance (ETH_EXPECTED_CHAIN_ID=1) is possible but hard-
// gated: api/eth_rpc.go's InitEthRPC refuses to enable this corner at
// all against any non-Sepolia chain unless ETH_CUSTODIAL_KMS_ENABLED=
// true with real AWS credentials - see that function's doc comment.
// This gate is an engineering safety rail only, NOT a substitute for
// the licensing/legal review flagged in audit/KNOWN_GAPS.md - custodying
// real mainnet ETH on a user's behalf is money transmission in most
// jurisdictions, and that review is still a separate, outstanding
// prerequisite regardless of what this code technically allows.
type EthCustodialAccount struct {
	// CreditChainAddress is this account's own wallet address (the one
	// that authenticated the request that created this custodial
	// account) - the key this store is indexed by.
	CreditChainAddress string

	// CreditChainPublicKey is the hex-encoded public key behind
	// CreditChainAddress, captured from the signed request that created
	// this account (never trusted from anywhere else - CreditChainAddress
	// is a one-way hash of it, so it can't be re-derived later). Needed
	// to compute this account's ETHB (bridged-ETH) sub-address via
	// core.DeriveTokenAddress when a deposit is credited - see
	// api/apply.go's applyBridgeDeposit.
	CreditChainPublicKey string

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

	// LastCreditedWei is the highest real Sepolia balance this
	// account's ETHB (bridged-ETH) sub-address has already been
	// credited for, as a decimal string (mirrors
	// EthCustodialWithdrawData.AmountWei's string convention - a
	// *big.Int doesn't survive JSON round-trips cleanly on its own).
	// Empty means never credited (treated as zero). See
	// api/eth_bridge.go's deposit-detection loop, which advances this
	// field only once a corresponding TxBridgeDeposit has actually
	// been applied - never optimistically bumped at report time.
	LastCreditedWei string

	// UncreditedExcessWei accumulates (never overwritten) the portion
	// of any deposit that would have pushed this account's total
	// BTCB+ETHB USD holdings over the admin-set
	// "AccountHoldingsCapUsdCents" limit at the time it was observed -
	// see api/apply.go's applyBridgeDeposit and
	// api/account_holdings_cap.go. If the admin hasn't set both
	// EthUsdCentsPerEth/BtcUsdCentsPerBtc rates, this never
	// accumulates anything (matching today's unlimited testnet
	// behavior). Real, on-chain wei this account's custodial address
	// genuinely received but that this ledger only partially credited
	// - not lost, just requiring manual admin follow-up (see GET
	// /admin/bridge/excess-deposits).
	UncreditedExcessWei string
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
