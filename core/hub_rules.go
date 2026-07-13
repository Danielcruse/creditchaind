package core

import "sync"

// HubRules is a verified Trading Hub's own preset terms/conditions for
// trading with it (e.g. payment windows, contact instructions, refund
// policy) - plain free text, shown to a trader before they can submit
// a trade request against this hub's rate (see website/js/p2p.js's
// requestHubTrade). This is a disclosure/UX gate only, the same kind
// of thing every confirm() warning already in this app is - it is not
// a security boundary, since nothing stops a caller who skips the
// frontend from hitting the API directly without ever having read
// this text, exactly like nothing stops them from bypassing any other
// client-side confirm() dialog in this codebase.
// DefaultPaymentWindowMinutes is how long a trader has to complete
// off-app payment (and the counterparty has to confirm it) when a hub
// hasn't set an explicit window for that trade direction.
const DefaultPaymentWindowMinutes = 30

type HubRules struct {
	HubAddress string
	Text       string
	// PaymentWindowMinutes is how long, in minutes, a trader has to
	// complete off-app payment before the chat's countdown expires -
	// keyed by trade direction ("BUY_FROM_HUB" / "SELL_TO_HUB") so a
	// hub can allow more time buying than selling, or vice versa.
	// A direction missing from this map falls back to
	// DefaultPaymentWindowMinutes (see PaymentWindowFor) - this keeps
	// rules saved before this field existed, or a hub that only
	// bothered to set one direction, working exactly as before.
	PaymentWindowMinutes map[string]int
	// MinReputationToApply gates HubAgentApplication submission (see
	// api/hub_agents.go's HandleApplyHubAgent) - "the hub owner sets
	// the agent request rules." Zero (the default for a hub that never
	// set this) means no reputation floor - anyone can apply.
	MinReputationToApply uint64
	UpdatedAt            int64
}

// PaymentWindowFor returns the payment window (in minutes) for a
// trade direction, falling back to DefaultPaymentWindowMinutes if the
// hub never set one explicitly (including for a nil *HubRules, i.e. a
// hub that has never posted rules at all).
func (r *HubRules) PaymentWindowFor(direction string) int {
	if r == nil || r.PaymentWindowMinutes == nil {
		return DefaultPaymentWindowMinutes
	}
	if minutes, ok := r.PaymentWindowMinutes[direction]; ok && minutes > 0 {
		return minutes
	}
	return DefaultPaymentWindowMinutes
}

type HubRulesStore struct {
	mu    sync.Mutex
	Rules map[string]*HubRules // keyed by HubAddress
}

func NewHubRulesStore() *HubRulesStore {
	return &HubRulesStore{Rules: make(map[string]*HubRules)}
}

// Set posts or replaces a hub's rules text and payment windows -
// always the whole record, same "no partial-field update" convention
// as HubRateStore.Set.
func (s *HubRulesStore) Set(hub, text string, paymentWindowMinutes map[string]int, minReputationToApply uint64, at int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Rules[hub] = &HubRules{
		HubAddress:           hub,
		Text:                 text,
		PaymentWindowMinutes: paymentWindowMinutes,
		MinReputationToApply: minReputationToApply,
		UpdatedAt:            at,
	}
}

func (s *HubRulesStore) Get(hub string) (*HubRules, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Rules[hub]
	return r, ok
}

func (s *HubRulesStore) Snapshot() map[string]*HubRules {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]*HubRules, len(s.Rules))
	for k, v := range s.Rules {
		out[k] = v
	}
	return out
}

func (s *HubRulesStore) Restore(all map[string]*HubRules) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range all {
		s.Rules[k] = v
	}
}
