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
	PurchaseAwaitingPayment PurchaseStatus = "AWAITING_PAYMENT"
	PurchaseConfirmed       PurchaseStatus = "CONFIRMED"
)

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

	Status PurchaseStatus

	CreatedAt   int64
	ConfirmedAt int64
}

type TreasuryStore struct {
	mu sync.Mutex

	Treasury  Treasury
	Purchases map[string]*PurchaseRequest
}

func NewTreasuryStore() *TreasuryStore {
	return &TreasuryStore{Purchases: make(map[string]*PurchaseRequest)}
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

// ConfirmPurchase marks an AWAITING_PAYMENT request CONFIRMED,
// atomically with the check that it was still awaiting - the actual
// CCT transfer is the caller's job (see api/apply.go's
// applyTreasuryPurchaseConfirm).
func (s *TreasuryStore) ConfirmPurchase(id string) (*PurchaseRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.Purchases[id]
	if !ok || p.Status != PurchaseAwaitingPayment {
		return nil, false
	}

	p.Status = PurchaseConfirmed
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
