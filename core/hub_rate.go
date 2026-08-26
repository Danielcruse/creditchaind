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
// HubRateScale is the fixed-point scale SellRate/BuyRate are stored
// at - both are the FiatSymbol amount per 1 whole unit of Symbol,
// times HubRateScale, so a rate like "1 CCT = 0.000975 ETH" round-trips
// as an exact integer (975) instead of truncating to 0 the way a bare
// uint64 would. Mirrors core/order_book.go's identical priceScale
// convention for the same reason - keeping the two on the same scale
// isn't required for correctness (they're unrelated markets) but
// avoids a second, differently-scaled "price" concept in this
// codebase. Every SellRate/BuyRate value, and every computation that
// multiplies an amount by one, must divide by this constant to recover
// the real-world quantity.
const HubRateScale = 1_000_000

type HubRate struct {
	Hub string
	// HubPublicKey is the public key of whichever address actually
	// backs this listing's inventory - the hub's own public key for
	// its flagship listing, or the operator's own public key when
	// OperatorAddress is set. Always pairs with InventoryAddress().
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
	// inventory), scaled by HubRateScale (e.g. a real rate of 0.000975
	// is stored as 975). Zero means this hub doesn't sell this symbol.
	SellRate uint64
	// BuyRate is the FiatSymbol amount per 1 unit of Symbol when a
	// user SELLS Symbol to this hub (the hub buys, paying fiat off-
	// app), scaled by HubRateScale exactly like SellRate. Zero means
	// this hub doesn't buy this symbol.
	BuyRate uint64

	// Active gates whether new trades can be requested against this
	// rate at all - a hub can go temporarily off-market (e.g. no
	// inventory, stepping away) without losing its configured rates.
	Active bool

	UpdatedAt int64

	// PostedBy is whichever wallet actually signed this rate update -
	// the hub itself, or one of its delegated workers/operators (see
	// core.HubWorker's doc comment). Equal to Hub when the hub posted
	// it directly, equal to OperatorAddress when an operator posted
	// their own listing.
	PostedBy string

	// OperatorAddress is "" for the hub's own flagship listing (the
	// original, single-marketplace-per-hub behavior: inventory and
	// settlement both happen against Hub's own balance). When
	// non-empty, this listing belongs to one specific active worker of
	// Hub - a distinct sub-marketplace operating under the hub's
	// verified badge, with its OWN inventory/balance, separate from the
	// hub's own and from every other operator's. This is what makes
	// "add a worker" actually mean "stand up another independent
	// marketplace inside one verified hub" rather than just "another
	// person who can edit the hub's shared price" - see
	// core.HubWorkerStore.IsActiveWorker, the authorization check that
	// still gates who may ever post a rate with this set.
	OperatorAddress string
}

// InventoryAddress is whose actual balance this rate's trades settle
// against - the hub itself for its own flagship listing, or the
// specific operator for their own sub-marketplace. Every balance
// check/transfer/escrow tied to this rate must resolve against this
// address (paired with HubPublicKey, which is always the public key of
// whichever address this returns), never against Hub directly, so an
// operator's trades only ever touch the operator's own funds.
func (r *HubRate) InventoryAddress() string {
	if r.OperatorAddress != "" {
		return r.OperatorAddress
	}
	return r.Hub
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
	Rates map[string]*HubRate // keyed by hubRateKey(Hub, Symbol, OperatorAddress, FiatSymbol)
}

func NewHubRateStore() *HubRateStore {
	return &HubRateStore{Rates: make(map[string]*HubRate)}
}

// hubRateKey includes FiatSymbol (the counter-currency) alongside
// Hub/Symbol/OperatorAddress - a hub or operator can post several
// concurrent rates for the same Symbol against different counter
// currencies at once (e.g. CCT priced in TESTFIAT AND in ETHTEST
// simultaneously), so the counter currency has to be part of what
// makes a listing unique, not just an attribute of one. Without this,
// posting a second currency for the same symbol would silently
// overwrite the first instead of coexisting alongside it.
func hubRateKey(hub, symbol, operator, fiatSymbol string) string {
	return hub + ":" + symbol + ":" + operator + ":" + fiatSymbol
}

// Set posts or replaces a rate for a (symbol, counter-currency) pair
// under a hub - keyed by (Hub, Symbol, OperatorAddress, FiatSymbol) so
// the hub's own flagship listing (OperatorAddress "") and each
// operator's own listing coexist independently, and so do multiple
// currency options for the same symbol under the same actor. Always
// the whole record, no partial-field update, so a listing can never
// end up with a stale SellRate paired with a freshly-changed BuyRate
// by accident.
func (s *HubRateStore) Set(r *HubRate) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Rates[hubRateKey(r.Hub, r.Symbol, r.OperatorAddress, r.FiatSymbol)] = r
}

// Get looks up one specific listing - operator "" is the hub's own
// flagship listing, a non-empty operator is that operator's own;
// fiatSymbol picks which of that actor's currency options for symbol.
func (s *HubRateStore) Get(hub, symbol, operator, fiatSymbol string) (*HubRate, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Rates[hubRateKey(hub, symbol, operator, fiatSymbol)]
	return r, ok
}

// Renormalize rebuilds every map key from each entry's own fields -
// needed once, right after restoring Rates from a state.json snapshot
// written before FiatSymbol became part of the key, so those entries
// stay reachable via Get under the new key scheme instead of silently
// becoming orphaned (still visible via List, but never matched by Get,
// which is what applyHubTradeRequest/HandleRequestHubTrade actually
// use to find the rate a trade targets). Idempotent and harmless to
// call even when every key already matches the current scheme.
func (s *HubRateStore) Renormalize() {
	s.mu.Lock()
	defer s.mu.Unlock()

	rebuilt := make(map[string]*HubRate, len(s.Rates))
	for _, r := range s.Rates {
		rebuilt[hubRateKey(r.Hub, r.Symbol, r.OperatorAddress, r.FiatSymbol)] = r
	}
	s.Rates = rebuilt
}

// List returns every posted rate, active or not, across every hub and
// operator, so a hub's own dashboard can still show (and let them
// re-activate) a rate they previously took offline.
func (s *HubRateStore) List() []*HubRate {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]*HubRate, 0, len(s.Rates))
	for _, r := range s.Rates {
		list = append(list, r)
	}
	return list
}
