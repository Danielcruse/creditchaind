package core

import "sync"

// VaultLockAttestation is deliberately REAL CONSENSUS STATE, not the
// node-local watched-address pattern EthWatchedAddress/BtcWatchedAddress
// use (core/eth_address.go, core/btc_address.go) - and that difference
// is the entire point of the vault redesign. Those stores are
// node-local/non-gossiped by design (see their own doc comments), which
// is exactly the gap audit/KNOWN_GAPS.md's bridge-deposit entries name:
// a single node's self-attested report stands in for "the deposit
// happened," with no other node independently verifying it. A vault's
// whole value proposition is that MULTIPLE INDEPENDENT bridge
// validators each personally verify the same on-chain lock event
// before anything mints - so this type, and every transaction that
// touches it (TxVaultLockAttest), is wallet-signed by each attesting
// validator individually and travels through the normal gossiped/
// block-confirmed transaction pipeline like any other consensus-
// critical state, not a per-node-only side table.
type VaultLockAttestation struct {
	// ID is the dedup key every validator attesting to the same real
	// lock event must converge on - see api/vault_lock.go's
	// vaultLockAttestationID for exactly how it's computed (audit
	// finding C-05's full fix, 2026-08-16: content-derived - folds in
	// the claimed recipient/amount, not just chain+external-tx-
	// identifier - for any genuinely new event, falling back to the
	// original chain+":"+external-tx-identifier scheme only for an
	// event already in flight under it before that fix shipped).
	// Prevents the same real lock event from being attested/minted
	// twice regardless of how many validators report it, and - since
	// the fix - prevents a fraudulent claim from ever colliding with
	// (and blocking) an honest one for the same external event.
	ID                 string
	Chain              string // "ETH" | "BTC" | "USDC"
	ExternalTxHash     string
	DepositorCcAddress string
	DepositorPublicKey string
	// AmountSmallestUnit is a decimal string (wei / sats / USDC-6dp) -
	// the same "amount as base-10 string" convention already used for
	// ETHB's wei amounts (EthCustodialAccount.LastCreditedWei) and CCT's
	// smallest-unit balances (CctStore), since *big.Int doesn't survive
	// JSON round-trips and wei/USDC amounts can exceed a JS-safe integer.
	AmountSmallestUnit string
	// Attestors is the set of bridge validators (by CreditChainAddress)
	// who have independently confirmed observing this lock at the
	// required confirmation depth - mirrors AdminMultisigProposal's
	// Approvals map shape (core/admin_multisig.go) exactly.
	Attestors map[string]bool
	Status    VaultLockAttestationStatus
	CreatedAt int64
	MintedAt  int64
}

type VaultLockAttestationStatus string

const (
	VaultLockPending VaultLockAttestationStatus = "PENDING"
	VaultLockMinted  VaultLockAttestationStatus = "MINTED"
)

// VaultLockAttestationStore is kept forever (never pruned) - the same
// permanent-audit-trail convention AdminMultisigStore already
// established (core/admin_multisig.go), appropriate here since this is
// literally the record of what backs every CCC1 token ever minted.
type VaultLockAttestationStore struct {
	mu           sync.Mutex
	Attestations map[string]*VaultLockAttestation
}

func NewVaultLockAttestationStore() *VaultLockAttestationStore {
	return &VaultLockAttestationStore{Attestations: make(map[string]*VaultLockAttestation)}
}

func (s *VaultLockAttestationStore) Get(id string) (*VaultLockAttestation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.Attestations[id]
	return a, ok
}

func (s *VaultLockAttestationStore) Create(a *VaultLockAttestation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Attestations[a.ID] = a
}

func (s *VaultLockAttestationStore) Snapshot() map[string]*VaultLockAttestation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]*VaultLockAttestation, len(s.Attestations))
	for k, v := range s.Attestations {
		out[k] = v
	}
	return out
}

func (s *VaultLockAttestationStore) Restore(all map[string]*VaultLockAttestation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range all {
		s.Attestations[k] = v
	}
}
