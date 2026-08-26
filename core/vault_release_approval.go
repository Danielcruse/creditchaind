package core

import "sync"

// VaultReleaseApproval is the withdrawal-side mirror of
// VaultLockAttestation (core/vault_lock_attestation.go) - see that
// file's doc comment for why this must be real gossiped/consensus
// state, not a node-local side table. A user burns their CCC1 balance
// (TxVaultBurn) to request release; each bridge validator that agrees
// the burn is real and the destination is correct submits a
// TxVaultReleaseApprove carrying THEIR OWN real signature over the
// canonical release payload, computed independently on their own node.
// Because the real external-chain signature rides inside the gossiped
// transaction itself, threshold-counting and real-signature-collection
// happen through the same replicated stream - no separate off-chain
// coordination channel is needed, and every node ends up holding the
// full signature bundle once threshold is reached.
type VaultReleaseApproval struct {
	// ID is the burn transaction's own ID - one release approval record
	// per burn, by construction.
	ID                    string
	Chain                 string // "ETH" | "BTC" | "USDC"
	BurnCcAddress         string
	DestinationExtAddress string
	// AmountSmallestUnit mirrors VaultLockAttestation's field of the
	// same name - decimal string, native smallest unit.
	AmountSmallestUnit string
	// ReleaseID is the on-chain replay-guard nonce the ETH/USDC vault
	// contract's usedReleaseIds mapping checks (contracts/
	// CreditChainVault.sol) - not meaningful for BTC, where an
	// already-spent UTXO is itself the replay guard.
	ReleaseID uint64
	// Signatures is bridge-validator CreditChainAddress -> that
	// validator's own real external-chain signature (DER-encoded ECDSA
	// for both ETH and BTC) over the canonical release payload -
	// mirrors VaultLockAttestation.Attestors' address-keyed shape, just
	// carrying a real cryptographic artifact per entry instead of a
	// bare boolean.
	Signatures map[string][]byte
	Status     VaultReleaseApprovalStatus
	CreatedAt  int64
	ReleasedAt int64
	// ExternalReleaseTxHash is the real ETH/USDC release() tx hash or
	// BTC broadcast txid, populated once TxVaultReleaseComplete confirms
	// the actual external-chain release succeeded - see
	// api/vault_release_relay.go / api/vault_btc_release_relay.go.
	ExternalReleaseTxHash string
	// PendingFeeAmountSmallestUnit is ETH/USDC's gas-cost-based release
	// fee (see api/vault_fee.go), already deducted from AmountSmallestUnit
	// at burn time but deliberately NOT credited to anyone yet - unlike
	// BTC (which still credits a fixed configured recipient immediately,
	// since it has no relayer/gas cost at all), which real relayer ends
	// up actually paying the gas for THIS release is only known once one
	// of them succeeds. Credited to whichever CreditChainAddress the
	// winning relayer's own TxVaultReleaseComplete names (see
	// applyVaultReleaseComplete) - empty/unused for BTC.
	PendingFeeAmountSmallestUnit string
	// BurnBlockHeight is the CreditChain block index the burn (TxVaultBurn)
	// that opened this approval was applied in - the baseline
	// VaultReleaseConfirmationDepthBlocks is measured from. Recorded at
	// burn time (applyVaultBurn, api/vault_lock.go) so every node agrees
	// on the identical baseline regardless of when it happens to observe
	// the burn.
	BurnBlockHeight uint64
}

// VaultReleaseConfirmationDepthBlocks is how many additional CreditChain
// blocks must be built on top of the block a burn was applied in before
// any bridge validator may co-sign its release - added 2026-08-20, per
// explicit direction to bury a burn under real CreditChain finality
// before anything acts on it externally, replacing the previous
// "act on it as soon as seen" behavior. 8 is the midpoint of the
// requested 6-10 block range. This guards against a short-lived
// CreditChain-side fork/reorg unwinding a burn before an irreversible
// external release (real ETH/BTC/USDC leaving the vault) has happened -
// the same reasoning real chains use N-confirmation waits for, applied
// here to CreditChain's own blocks as the source of truth for "did this
// burn really happen." Enforced both where a validator would submit a
// signature (HandleVaultReleaseApprove, and the auto-sign watch loop in
// vault_release_approve_watch.go - the primary, UX-facing gate) and
// again at apply time (applyVaultReleaseApprove, api/vault_lock.go -
// the safety-critical, consensus-agreed re-check every node performs
// identically, never trusting submission-time state).
const VaultReleaseConfirmationDepthBlocks uint64 = 8

type VaultReleaseApprovalStatus string

const (
	VaultReleasePending  VaultReleaseApprovalStatus = "PENDING"
	VaultReleaseReleased VaultReleaseApprovalStatus = "RELEASED"
	// VaultReleaseRefunded is a terminal status for a PENDING approval
	// that never actually executed on-chain and was instead
	// administratively refunded back to the burner (see
	// api/admin_refund_stuck_vault_burn.go) - e.g. an approval stuck
	// forever behind a stale bridge-validator signature, or one whose
	// computed release amount was zeroed out entirely by the flat
	// release fee (see api/vault_fee.go's splitVaultReleaseFee). Once
	// set, this approval can never be signed, relayed, or refunded
	// again - the real backing value was already restored to the
	// burner's CCC1 balance instead.
	VaultReleaseRefunded VaultReleaseApprovalStatus = "REFUNDED"
)

// VaultReleaseApprovalStore is kept forever, same permanent-audit-trail
// convention as VaultLockAttestationStore/AdminMultisigStore.
type VaultReleaseApprovalStore struct {
	mu        sync.Mutex
	Approvals map[string]*VaultReleaseApproval
}

func NewVaultReleaseApprovalStore() *VaultReleaseApprovalStore {
	return &VaultReleaseApprovalStore{Approvals: make(map[string]*VaultReleaseApproval)}
}

func (s *VaultReleaseApprovalStore) Get(id string) (*VaultReleaseApproval, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.Approvals[id]
	return a, ok
}

func (s *VaultReleaseApprovalStore) Create(a *VaultReleaseApproval) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Approvals[a.ID] = a
}

func (s *VaultReleaseApprovalStore) Snapshot() map[string]*VaultReleaseApproval {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]*VaultReleaseApproval, len(s.Approvals))
	for k, v := range s.Approvals {
		out[k] = v
	}
	return out
}

func (s *VaultReleaseApprovalStore) Restore(all map[string]*VaultReleaseApproval) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range all {
		s.Approvals[k] = v
	}
}
