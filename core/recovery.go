package core

import "sync"

// WalletRecovery is a testnet-only account-recovery placeholder, and
// it's important to be precise about what it actually does: it does
// NOT recover a lost private key - that's mathematically impossible
// without the key (or a seed phrase derived from it) having been
// saved somewhere in the first place. What this does instead is a
// MIGRATION - a verified email plus a low-entropy security question
// (the wallet's creation year, see core.Wallet.CreatedAt) authorizes
// moving the old address's core balances and reputation to a brand
// new keypair the recovering user controls, after which the old
// address is permanently frozen (see ModeratorReputationBonus-style
// disclosed-limitation comments elsewhere in this package for the
// established pattern this follows).
//
// The security here is deliberately weaker than a signature - it has
// to be, since the whole scenario is "the real credential is gone."
// A creation-year question has very few possible answers for a young
// network and should be treated as a testnet-only convenience, not a
// substitute for real account-recovery infrastructure (e.g. a second
// hardware-backed factor, a time-locked social-recovery quorum) before
// any of this holds real value.
type WalletRecovery struct {
	Address       string
	Email         string
	EmailVerified bool

	// MigratedTo is "" until this address has been recovered away
	// from - once set, the address is frozen (see api/apply.go's
	// applyTxOnce guard) and can never be used again, even if the
	// original private key later turns up.
	MigratedTo string

	// PendingEmail/PendingEmailToken/PendingEmailTokenExpiresAt stage
	// an unconfirmed contact-email verification (see
	// SetPendingEmail/ConfirmPendingEmail below) - kept on this same
	// record deliberately, rather than a separate store, since it's
	// still fundamentally "this address's email situation," just not
	// confirmed yet. Cleared once ConfirmPendingEmail succeeds. Zero
	// value (PendingEmailToken == "") means no request currently
	// outstanding.
	PendingEmail               string
	PendingEmailToken          string
	PendingEmailTokenExpiresAt int64
}

type RecoveryStore struct {
	mu      sync.Mutex
	Records map[string]*WalletRecovery
}

func NewRecoveryStore() *RecoveryStore {
	return &RecoveryStore{Records: make(map[string]*WalletRecovery)}
}

// SetEmail links (or relinks) a verified email to an address - only
// ever called after the address's own owner has proven control of
// both the address (their signature) and the email (a confirmed
// code), while they still hold the real private key.
func (s *RecoveryStore) SetEmail(address, email string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Records[address]
	if !ok {
		r = &WalletRecovery{Address: address}
		s.Records[address] = r
	}
	r.Email = email
	r.EmailVerified = true
}

// SetPendingEmail stages an unverified contact email + one-time
// confirm code for address, auto-creating a record if this is its
// first ever email-related transaction - mirrors SetEmail's own
// auto-create shape. Called only from applyRequestContactEmailCode
// (api/apply.go). Re-callable at any time (a fresh "resend" or
// "update email" request simply overwrites whatever was pending
// before, matching the real user-facing gap this closes: there was no
// way to request a code again or change the email being verified).
func (s *RecoveryStore) SetPendingEmail(address, email, code string, expiresAt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Records[address]
	if !ok {
		r = &WalletRecovery{Address: address}
		s.Records[address] = r
	}
	r.PendingEmail = email
	r.PendingEmailToken = code
	r.PendingEmailTokenExpiresAt = expiresAt
}

// ConfirmPendingEmail promotes the staged PendingEmail to the real,
// verified Email/EmailVerified fields once the submitted code matches
// - called only from applyConfirmContactEmail. No constant-time
// comparison needed here (unlike a long-lived shared secret): this is
// a short-lived, single-use code delivered out of band via email, not
// a value an attacker gets repeated local guesses against. Returns
// false (and touches nothing) if there's no outstanding request, the
// code doesn't match, or it's expired - the caller is expected to
// request a fresh code in any of those cases.
func (s *RecoveryStore) ConfirmPendingEmail(address, code string, asOf int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Records[address]
	if !ok || r.PendingEmailToken == "" || r.PendingEmailToken != code || asOf > r.PendingEmailTokenExpiresAt {
		return false
	}

	r.Email = r.PendingEmail
	r.EmailVerified = true
	r.PendingEmail = ""
	r.PendingEmailToken = ""
	r.PendingEmailTokenExpiresAt = 0
	return true
}

func (s *RecoveryStore) Get(address string) (*WalletRecovery, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Records[address]
	return r, ok
}

// IsRecoverable is the actual security check the migration flow
// depends on - a match requires knowing BOTH the address and its
// exact linked, verified email, and the address must not already have
// been recovered away from once. This is deterministic and fully
// replicated (built entirely from TxSetRecoveryEmail transactions
// every node applies identically), so every node reaches the same
// answer independently - no ephemeral, node-local state (like a
// one-time code shown at submission time) is part of the actual
// security boundary here.
func (s *RecoveryStore) IsRecoverable(address, email string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Records[address]
	return ok && r.EmailVerified && r.Email == email && r.MigratedTo == ""
}

func (s *RecoveryStore) IsMigrated(address string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Records[address]
	return ok && r.MigratedTo != ""
}

// MarkMigrated freezes the old address, atomically with the check
// that it hasn't already been recovered away from - guards against
// two competing recovery transactions (e.g. two different "new
// addresses" both racing to claim the same lost wallet) both
// succeeding. Auto-creates the record if this address has never set a
// recovery email (e.g. TxHDWalletMigrate, which is signed by the old
// key directly and has no email/security-question prerequisite the
// way TxRecoverWallet does) - the address being frozen is what
// matters, not whether it happened to have a prior record.
func (s *RecoveryStore) MarkMigrated(address, newAddress string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Records[address]
	if !ok {
		r = &WalletRecovery{Address: address}
		s.Records[address] = r
	}
	if r.MigratedTo != "" {
		return false
	}
	r.MigratedTo = newAddress
	return true
}
