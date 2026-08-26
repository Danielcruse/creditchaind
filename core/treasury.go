package core

import "sync"

// Treasury holds the Foundation's public CCT sale configuration. The
// actual CCT allocation is NOT tracked here as a separate balance -
// it lives as a real tokenStore balance at FoundationAddress (see
// api/token.go's tokenStore), the same way pool/stake escrow already
// represents custody as an ordinary balance at a known address rather
// than duplicate bookkeeping. FoundationAddress is a real,
// operator-supplied address (not a synthetic constant like pool
// addresses use) since, unlike a pool, a treasury needs a real
// controlling party who can eventually move funds outside the sale
// flow.
type Treasury struct {
	FoundationAddress string

	// SalePriceCentsPerCCT is a fiat reference price (US cents per
	// CCT), purely descriptive/display - it is never itself moved
	// on-chain. Fiat-denominated rather than YEN-e-denominated because
	// this phase doesn't wire YEN-e into any acquisition path yet, so
	// no buyer could hold YEN-e to pay with.
	SalePriceCentsPerCCT uint64

	Paused bool

	// GenesisPoolsAllocated guards PoSR's one-time genesis pool mint
	// (see api/apply.go's applyGenesisPoolAllocation) - this chain has
	// been running for a long time already, so there's no true
	// "genesis" moment to hook into programmatically; this flag is
	// what makes the deliberate, explicit admin action idempotent
	// instead.
	GenesisPoolsAllocated bool

	// TokenomicsDecimalMigrationApplied guards the one-time conversion
	// of every CCT balance from the old whole-unit uint64 ledger
	// (TokenStore) into the new 18-decimal big-int ledger (CctStore) -
	// see api/tokenomics_migration.go's applyTokenomicsDecimalMigration.
	// Same idempotent-admin-action pattern as GenesisPoolsAllocated.
	TokenomicsDecimalMigrationApplied bool

	// LastModeratorPoolSweepAt is the block timestamp the moderator fee
	// pool (see api/apply.go's platformFeeModeratorPoolAddress) was last
	// distributed to active moderators - api/apply.go's
	// sweepModeratorFeePoolIfDue gates on this, block-timestamp-driven
	// like every other periodic sweep in that file. Parked here (rather
	// than a new dedicated store) purely to reuse Treasury's existing
	// persistence wiring for a single int64 - zero value (never swept)
	// is a safe starting point, same as every other zero-valued flag on
	// this struct.
	LastModeratorPoolSweepAt int64

	// CCTBurnedPendingReward accumulates the CCT-equivalent value of
	// every marketplace platform-fee burn (see api/apply.go's
	// splitPlatformFee) since the last block reward payout - real CCT
	// burns add directly; ETH_CCC1/BTC_CCC1/USDC_CCC1 burns add their
	// CCT-equivalent value instead, via api/burn_value_oracle.go's
	// cctEquivalentWholeOfBurn (REPT has no defined USD/CCT exchange
	// rate, so its burns don't contribute). api/apply.go's
	// applyBlockReward consumes and resets this every block, minting
	// 90% of it fresh to the producing validator (and their
	// delegators) as that block's reward - ties real validator income
	// to genuine fee-burning economic activity across every symbol the
	// platform fee is charged in, instead of an unconditional flat/
	// decaying emission or a CCT-only accounting. Non-CCT burns are
	// never actually converted/minted/moved into CCT anywhere - this
	// field only tracks a VALUATION of what was already burned in its
	// own currency, so no other token ledger's balance is touched by
	// this accounting. The other 10% of whatever was burned (in CCT
	// terms) stays permanently destroyed (it was already burned in
	// full, in its own currency, at the moment the fee was collected;
	// this is a separate, additional CCT mint of 90% of the tracked
	// total, not an un-burning). A block with no burn activity since
	// the last one pays no reward - intended behavior, not a bug.
	// Parked here for the same "reuse Treasury's existing persistence
	// wiring" reason as LastModeratorPoolSweepAt above.
	CCTBurnedPendingReward uint64
}

// PurchaseStatus is a one-sided purchase-request state machine,
// deliberately NOT modeled on core.P2POffer's EXTERNAL settlement
// machinery - that solves a harder problem (two mutually-untrusted
// strangers, hence independent buyer-mark-paid/seller-confirm
// signals, a dispute path, and an auto-release timeout). None of that
// applies here: the treasury operator is a trusted authority
// confirming their own receipt of payment, there's no second party to
// protect from the operator, no moderator, no auto-release need.
type PurchaseStatus string

const (
	// PurchaseAwaitingPayment is the initial state: the buyer has
	// requested a purchase and picked a Method, but the admin hasn't
	// yet sent payment instructions for it.
	PurchaseAwaitingPayment PurchaseStatus = "AWAITING_PAYMENT"
	// PurchaseInstructionsSent means the admin has provided
	// AdminInstructions (bank details / crypto address / wallet
	// handle) and, if BuyerEmail was given, emailed them to the buyer
	// along with a confirmation link.
	PurchaseInstructionsSent PurchaseStatus = "INSTRUCTIONS_SENT"
	// PurchaseBuyerMarkedPaid means the buyer clicked the "I've paid"
	// link - purely informational, not proof of payment; the admin
	// still independently verifies before confirming.
	PurchaseBuyerMarkedPaid PurchaseStatus = "BUYER_MARKED_PAID"
	PurchaseConfirmed       PurchaseStatus = "CONFIRMED"
)

// PurchaseMethod is how the buyer intends to pay off-app - shown to
// the admin so they know which kind of instructions (bank details vs.
// a crypto receiving address vs. a wallet handle) to send back. Raw
// card details are deliberately NEVER collected through any form on
// this site - PurchaseMethodCard/ApplePay/GooglePay/OnlineWallet all
// resolve to the exact same "buyer leaves contact info, admin follows
// up by email with a real, PCI-compliant payment link (e.g. a Stripe/
// PayPal checkout page) they complete on that processor's OWN site"
// flow - see HandleCreatePurchaseRequest's doc comment. Collecting a
// card number here instead would be a PCI-DSS violation.
type PurchaseMethod string

const (
	PurchaseMethodBankTransfer   PurchaseMethod = "BANK_TRANSFER"
	PurchaseMethodCryptoTransfer PurchaseMethod = "CRYPTO_TRANSFER"
	PurchaseMethodOnlineWallet   PurchaseMethod = "ONLINE_WALLET"
	PurchaseMethodCard           PurchaseMethod = "CARD"
	PurchaseMethodApplePay       PurchaseMethod = "APPLE_PAY"
	PurchaseMethodGooglePay      PurchaseMethod = "GOOGLE_PAY"
)

// ValidPurchaseMethod reports whether m is one of the supported
// off-app payment methods - the single place both the HTTP handler
// and the apply-time re-validation check against, so they can never
// drift apart on what's accepted.
func ValidPurchaseMethod(m PurchaseMethod) bool {
	switch m {
	case PurchaseMethodBankTransfer, PurchaseMethodCryptoTransfer, PurchaseMethodOnlineWallet,
		PurchaseMethodCard, PurchaseMethodApplePay, PurchaseMethodGooglePay:
		return true
	default:
		return false
	}
}

// MethodNeedsPresetBankAccount reports whether m is the one method
// that looks up a currency-keyed BankAccountDetails record (see below)
// rather than just collecting contact info for the admin to follow up
// on by email.
func MethodNeedsPresetBankAccount(m PurchaseMethod) bool {
	return m == PurchaseMethodBankTransfer
}

// PurchaseRequest is a buyer's intent to buy CCTAmount CCT from the
// Foundation Treasury. PriceAtRequest snapshots the sale price at
// request time so a later admin price change can't retroactively
// change what this buyer owes.
type PurchaseRequest struct {
	ID string

	BuyerAddress   string
	BuyerPublicKey string

	CCTAmount      uint64
	PriceAtRequest uint64

	// Method is which of the supported off-app payment channels the
	// buyer picked - tells the admin what kind of instructions
	// (AdminInstructions) to send back. Never trusted as proof of
	// payment; the operator's own confirm action is what actually
	// authorizes the CCT transfer.
	Method PurchaseMethod
	// BuyerEmail is where instructions and the confirmation link get
	// sent, if live outbound email is configured on this node (see
	// api/email.go's smtpConfigured) - "" if the buyer never gave one,
	// in which case the admin has to reach them some other way.
	BuyerEmail string
	// BuyerName/BuyerContact are extra contact info collected on the
	// buy-cct.html form - BuyerName also doubles as the "pay from an
	// account in this name" instruction for bank transfers (see
	// HandleCreatePurchaseRequest's doc comment on why that matters).
	// Both optional in the underlying data model even though the
	// frontend requires them - keeps this struct usable for any
	// caller/test that doesn't care about contact info specifically.
	BuyerName    string
	BuyerContact string
	// Currency is only meaningful for PurchaseMethodBankTransfer - which
	// of the admin's preset BankAccountDetails (below) to show the
	// buyer. Empty for every other method.
	Currency string
	// AdminInstructions is what the admin tells the buyer to actually
	// do (bank account details, a crypto receiving address, a wallet
	// handle) - "" until the admin sends it (see SendInstructions).
	AdminInstructions string
	// ConfirmToken gates the buyer's "I've paid" email link - proves
	// the click is tied to this exact instructions round, not just
	// guessable from the purchase ID. Not a security boundary on
	// funds; see TxTreasuryPurchaseBuyerMarkedPaid's doc comment.
	ConfirmToken string

	Status PurchaseStatus

	CreatedAt          int64
	InstructionsSentAt int64
	MarkedPaidAt       int64
	ConfirmedAt        int64
}

// BankAccountDetails is one currency's preset receiving-account info,
// configured once by the admin (see api/treasury.go's
// HandleAdminSetBankAccount) and shown to EVERY bank-transfer buyer who
// picks that currency, immediately, with no per-request admin action
// needed - unlike AdminInstructions (still available as a free-text
// fallback/extra-notes channel per request). Deliberately generic
// field names (AccountNumber covers an IBAN, a local account number,
// etc.) rather than a rigid per-country schema, since real bank
// details vary by country far more than a fixed set of fields could
// cleanly cover.
type BankAccountDetails struct {
	Currency      string `json:"currency"`
	AccountName   string `json:"accountName"`
	AccountNumber string `json:"accountNumber"`
	BankName      string `json:"bankName"`
	RoutingCode   string `json:"routingCode"` // SWIFT/BIC, routing number, sort code, IFSC, etc. - whatever this currency's banking system calls it
	Notes         string `json:"notes"`       // any extra free-text the admin wants shown alongside (e.g. "reference: your CreditChain address")
}

type TreasuryStore struct {
	mu sync.Mutex

	Treasury     Treasury
	Purchases    map[string]*PurchaseRequest
	BankAccounts map[string]BankAccountDetails // keyed by Currency
}

func NewTreasuryStore() *TreasuryStore {
	return &TreasuryStore{
		Purchases:    make(map[string]*PurchaseRequest),
		BankAccounts: make(map[string]BankAccountDetails),
	}
}

// SetBankAccount adds or replaces the preset receiving-account details
// for one currency - a full replacement of that currency's record, not
// a partial merge, so a stale field can never survive an update by
// omission.
func (s *TreasuryStore) SetBankAccount(details BankAccountDetails) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.BankAccounts[details.Currency] = details
}

func (s *TreasuryStore) GetBankAccount(currency string) (BankAccountDetails, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.BankAccounts[currency]
	return d, ok
}

// ListBankAccounts is what buy-cct.html's currency picker populates
// itself from - only currencies the admin has actually configured show
// up as choosable at all.
func (s *TreasuryStore) ListBankAccounts() []BankAccountDetails {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]BankAccountDetails, 0, len(s.BankAccounts))
	for _, d := range s.BankAccounts {
		list = append(list, d)
	}
	return list
}

func (s *TreasuryStore) CreatePurchase(p *PurchaseRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Purchases[p.ID] = p
}

func (s *TreasuryStore) GetPurchase(id string) (*PurchaseRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.Purchases[id]
	return p, ok
}

// ListPurchases returns every request regardless of status, same
// reasoning as every other list store in this package.
func (s *TreasuryStore) ListPurchases() []*PurchaseRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]*PurchaseRequest, 0, len(s.Purchases))
	for _, p := range s.Purchases {
		list = append(list, p)
	}
	return list
}

// ConfirmPurchase marks a not-yet-confirmed request CONFIRMED,
// atomically with the check that it was still in one of the three
// pre-confirm states - the admin may confirm at any of them (e.g.
// skipping straight from AWAITING_PAYMENT if the buyer paid and told
// them directly, without going through the instructions/mark-paid
// dance at all). The actual CCT transfer is the caller's job (see
// api/apply.go's applyTreasuryPurchaseConfirm).
func (s *TreasuryStore) ConfirmPurchase(id string) (*PurchaseRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.Purchases[id]
	if !ok {
		return nil, false
	}
	switch p.Status {
	case PurchaseAwaitingPayment, PurchaseInstructionsSent, PurchaseBuyerMarkedPaid:
	default:
		return nil, false
	}

	p.Status = PurchaseConfirmed
	return p, true
}

// SendInstructions records the admin's payment instructions and a
// fresh confirmation token for one pending purchase, moving it from
// AWAITING_PAYMENT to INSTRUCTIONS_SENT. A no-op (returns false) if
// the purchase isn't in that state - e.g. instructions were already
// sent, or it's already confirmed.
func (s *TreasuryStore) SendInstructions(id, instructions, token string, at int64) (*PurchaseRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.Purchases[id]
	if !ok || p.Status != PurchaseAwaitingPayment {
		return nil, false
	}

	p.AdminInstructions = instructions
	p.ConfirmToken = token
	p.Status = PurchaseInstructionsSent
	p.InstructionsSentAt = at
	return p, true
}

// MarkBuyerPaid transitions INSTRUCTIONS_SENT to BUYER_MARKED_PAID,
// only if token matches the one this purchase's instructions round
// actually issued - the real gate on this action, since there's no
// wallet signature to check (see TxTreasuryPurchaseBuyerMarkedPaid's
// doc comment).
func (s *TreasuryStore) MarkBuyerPaid(id, token string, at int64) (*PurchaseRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.Purchases[id]
	if !ok || p.Status != PurchaseInstructionsSent || token == "" || p.ConfirmToken != token {
		return nil, false
	}

	p.Status = PurchaseBuyerMarkedPaid
	p.MarkedPaidAt = at
	return p, true
}

// MarkBuyerPaidBySignedRequest is the in-app "I Have Paid" button's
// counterpart to MarkBuyerPaid's email-token version - the buyer is
// right there in the app with a wallet already, so a genuine signature
// (buyerAddress already verified as tx.AuthAddress by the caller) is
// the gate instead of a token, a strictly stronger proof of intent
// (still not proof of actual payment either way - the admin's own
// confirm step is still what actually authorizes the CCT transfer,
// same as always). Allowed from AWAITING_PAYMENT too, not just
// INSTRUCTIONS_SENT - the new preset-bank-account flow may never move
// a purchase into INSTRUCTIONS_SENT at all (see
// MethodNeedsPresetBankAccount's doc comment), so gating this on that
// specific status the way the token version does would wrongly block
// the common case.
func (s *TreasuryStore) MarkBuyerPaidBySignedRequest(id, buyerAddress string, at int64) (*PurchaseRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.Purchases[id]
	if !ok || p.BuyerAddress != buyerAddress {
		return nil, false
	}
	switch p.Status {
	case PurchaseAwaitingPayment, PurchaseInstructionsSent:
	default:
		return nil, false
	}

	p.Status = PurchaseBuyerMarkedPaid
	p.MarkedPaidAt = at
	return p, true
}

func (s *TreasuryStore) SetSalePrice(centsPerCCT uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Treasury.SalePriceCentsPerCCT = centsPerCCT
}

func (s *TreasuryStore) SetPaused(paused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Treasury.Paused = paused
}

func (s *TreasuryStore) SetFoundationAddress(address string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Treasury.FoundationAddress = address
}
