package core

import "sync"

// HubRate is a verified Trading Hub's standing, continuously-available
// quote for trading a specific in-app token against an off-chain
// (EXTERNAL, real-world) currency - the OTC-desk counterpart to a
// one-shot, fixed-amount P2POffer or BuyRequest. A hub posts this once
// (or updates it whenever its price changes) instead of re-listing a
// fresh fixed-size offer every time. No amount is fixed or escrowed at
// rate-set time - the tradeable range is simply "whatever the hub
// actually holds right now" (see api/apply.go's applyHubTradeRequest,
// which checks the hub's live balance at the moment someone actually
// requests a trade, not a pre-locked pot). This is the deliberate
// mechanism behind "the hub's wallet balance is the limit a buyer can
// demand" - there is no other cap.
type HubRate struct {
	Hub          string
	HubPublicKey string

	// Symbol is the in-app token being quoted (e.g. "CCT").
	Symbol string
	// FiatSymbol is either a free-text, non-real-token label for the
	// off-app currency this hub settles in (e.g. "TESTFIAT"), or - when
	// Mode is ONCHAIN - a real, resolvable in-app token symbol (e.g.
	// "ETHTEST") this hub quotes Symbol against. The field is reused
	// rather than renamed so an already-persisted state.json (written
	// before ONCHAIN mode existed) still decodes unchanged - every rate
	// written back then was implicitly off-app, since that was the only
	// mode. Never validated as a real token for OFFAPP; always resolved
	// and moved on chain for ONCHAIN, same as P2POffer's WantSymbol
	// distinguishes ONCHAIN from EXTERNAL.
	FiatSymbol string

	// Mode picks which of two settlement paths a trade against this
	// rate follows - "" or "OFFAPP" (the original, default behavior)
	// settles through the same EXTERNAL P2POffer chat/dispute flow as
	// before; "ONCHAIN" settles atomically, immediately, with both
	// Symbol and FiatSymbol (here a real token) moving on chain in one
	// step, exactly like a direct P2POffer ONCHAIN accept - there is
	// nothing to negotiate or dispute since both sides are verifiable
	// balances the server itself moves.
	Mode string

	// SellRate is the FiatSymbol amount per 1 unit of Symbol when a
	// user BUYS Symbol from this hub (the hub sells its own
	// inventory). Zero means this hub doesn't sell this symbol.
	SellRate uint64
	// BuyRate is the FiatSymbol amount per 1 unit of Symbol when a
	// user SELLS Symbol to this hub (the hub buys, paying fiat off-
	// app). Zero means this hub doesn't buy this symbol.
	BuyRate uint64

	// Active gates whether new trades can be requested against this
	// rate at all - a hub can go temporarily off-market (e.g. no
	// inventory, stepping away) without losing its configured rates.
	Active bool

	UpdatedAt int64

	// PostedBy is whichever wallet actually signed this rate update -
	// the hub itself, or one of its delegated workers (see
	// core.HubWorker's doc comment). Equal to Hub when the hub posted
	// it directly. Purely informational/audit - it grants no
	// additional authority itself, and doesn't change who the rate
	// belongs to (still Hub) or who a trader is actually trading
	// against (still Hub).
	PostedBy string
}

// IsOnChain reports whether this rate settles atomically on-chain
// (FiatSymbol is a real token) rather than through the off-app
// EXTERNAL chat/dispute flow - "" and "OFFAPP" are both treated as the
// original off-app behavior, so a rate persisted before Mode existed
// keeps working unchanged.
func (r *HubRate) IsOnChain() bool {
	return r.Mode == "ONCHAIN"
}

type HubRateStore struct {
	mu    sync.Mutex
	Rates map[string]*HubRate // keyed by hubRateKey(Hub, Symbol)
}

func NewHubRateStore() *HubRateStore {
	return &HubRateStore{Rates: make(map[string]*HubRate)}
}

func hubRateKey(hub, symbol string) string {
	return hub + ":" + symbol
}

// Set posts or replaces a hub's rate for a symbol - always the whole
// record, no partial-field update, so a hub can never end up with a
// stale SellRate paired with a freshly-changed BuyRate by accident.
func (s *HubRateStore) Set(r *HubRate) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Rates[hubRateKey(r.Hub, r.Symbol)] = r
}

func (s *HubRateStore) Get(hub, symbol string) (*HubRate, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Rates[hubRateKey(hub, symbol)]
	return r, ok
}

// List returns every posted rate, active or not, so a hub's own
// dashboard can still show (and let them re-activate) a rate they
// previously took offline.
func (s *HubRateStore) List() []*HubRate {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]*HubRate, 0, len(s.Rates))
	for _, r := range s.Rates {
		list = append(list, r)
	}
	return list
}
