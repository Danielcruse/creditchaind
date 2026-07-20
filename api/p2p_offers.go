package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var p2pOfferStore = core.NewP2POfferStore()

// resolveTokenStore is lookupCustomToken's counterpart for contexts
// that don't need a pool - P2P offers trade directly between two
// wallets, no AMM involved, so CCT/REPT (which lookupCustomToken
// doesn't resolve, since neither is ever pool-paired against itself)
// need to be included here too.
func resolveTokenStore(symbol string) (*core.TokenStore, error) {
	switch symbol {
	case "CCT":
		return tokenStore, nil
	case "REPT":
		return reptStore, nil
	case "ETHB":
		// Deliberately resolved directly here, NOT via
		// lookupCustomToken (which pairs a symbol with a CCT-priced
		// AMM pool) - ETHB represents real bridged Sepolia ETH 1:1
		// (see api/eth_bridge.go), so it must never float against CCT
		// at a synthetic swap-pool rate. Still fully usable for
		// ordinary transfers/P2P offers via this path.
		return bridgedEthStore, nil
	case "BTCB":
		// Same reasoning as ETHB - real bridged testnet BTC 1:1, never
		// floated against CCT via an AMM pool.
		return bridgedBtcStore, nil
	}
	store, _, err := lookupCustomToken(symbol)
	return store, err
}

type p2pOfferCreateData struct {
	OfferSymbol string `json:"offerSymbol"`
	OfferAmount uint64 `json:"offerAmount"`
	WantSymbol  string `json:"wantSymbol"`
	WantAmount  uint64 `json:"wantAmount"`
	// PaymentMode is "ONCHAIN" (default, if omitted) or "EXTERNAL" -
	// see core.PaymentMode's doc comment.
	PaymentMode string `json:"paymentMode"`
}

// HandleCreateP2POffer locks OfferAmount of OfferSymbol out of the
// creator's own balance into a per-offer escrow, listing it for
// anyone to accept at exactly the terms specified - no slippage, no
// pool-ratio math, just the two numbers the creator chose.
func HandleCreateP2POffer(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req p2pOfferCreateData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.PaymentMode == "" {
		req.PaymentMode = string(core.PaymentModeOnChain)
	}
	if req.PaymentMode != string(core.PaymentModeOnChain) && req.PaymentMode != string(core.PaymentModeExternal) {
		http.Error(w, "paymentMode must be 'ONCHAIN' or 'EXTERNAL'", http.StatusBadRequest)
		return
	}
	// Only a verified Trading Hub (see core/company.go) may create a
	// P2P offer - buy or sell, ONCHAIN or EXTERNAL, any token. Regular
	// users still trade freely by ACCEPTING an offer a hub posted (see
	// HandleAcceptP2POffer) - only listing/creating one is hub-gated,
	// same as buy requests (HandleCreateBuyRequest) already are, so a
	// retail user always trades against a vetted counterparty rather
	// than an anonymous one.
	if !companyStore.IsVerified(address) {
		http.Error(w, "only a verified Trading Hub can create a P2P offer - apply at /companies.html", http.StatusForbidden)
		return
	}

	if req.OfferAmount == 0 || req.WantAmount == 0 {
		http.Error(w, "offerAmount and wantAmount must both be positive", http.StatusBadRequest)
		return
	}
	if req.OfferSymbol == req.WantSymbol {
		http.Error(w, "offerSymbol and wantSymbol must differ", http.StatusBadRequest)
		return
	}

	offerStore, err := resolveTokenStore(req.OfferSymbol)
	if err != nil {
		http.Error(w, "unknown offerSymbol", http.StatusNotFound)
		return
	}
	// EXTERNAL mode's "want" side is a real-world payment this chain
	// can't observe (e.g. "USD", 50) - never a real token, so it's
	// never resolved against a TokenStore, only length-bounded like
	// any other free-text field (see corsMiddleware's body-size cap
	// for the general version of this guard).
	if req.PaymentMode == string(core.PaymentModeOnChain) {
		if _, err := resolveTokenStore(req.WantSymbol); err != nil {
			http.Error(w, "unknown wantSymbol", http.StatusNotFound)
			return
		}
	} else if len(req.WantSymbol) == 0 || len(req.WantSymbol) > 20 {
		http.Error(w, "wantSymbol must be 1-20 characters", http.StatusBadRequest)
		return
	}
	offerSide := resolveBalanceAddress(address, auth.PublicKey, req.OfferSymbol)
	if offerStore.BalanceOf(offerSide) < req.OfferAmount {
		http.Error(w, "insufficient balance to offer that amount", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxP2POfferCreate, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// p2pOfferView adds the "verified seller" badge on top of the raw
// core.P2POffer - computed at read time from companyStore rather than
// stored on the offer itself, so a company's verification status
// (which can change independently, e.g. a later rejection wouldn't
// apply here anyway - only a fresh application can be rejected) is
// always current as of when the listing is fetched, not stale as of
// when the offer was created.
type p2pOfferView struct {
	*core.P2POffer
	VerifiedSeller bool `json:"VerifiedSeller"`
}

// HandleListP2POffers lists every offer ever created, any status -
// open ones are what's actionable, but accepted/cancelled ones stay
// visible so a trade's outcome doesn't just vanish once it's settled.
func HandleListP2POffers(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	offers := p2pOfferStore.List()
	views := make([]p2pOfferView, 0, len(offers))
	for _, o := range offers {
		// HubAddress is set when Creator is actually a hub OPERATOR
		// (see core.HubRate.OperatorAddress) rather than the verified
		// hub itself - checking the hub, not the operator, is what
		// still shows "verified" for that operator's own trades.
		verifiedVia := o.Creator
		if o.HubAddress != "" {
			verifiedVia = o.HubAddress
		}
		views = append(views, p2pOfferView{
			P2POffer:       o,
			VerifiedSeller: companyStore.IsVerified(verifiedVia),
		})
	}

	json.NewEncoder(w).Encode(views)
}

type p2pOfferActionData struct {
	OfferID string `json:"offerId"`
}

// HandleAcceptP2POffer is the other trader taking the deal - they
// must currently hold WantAmount of WantSymbol; on confirmation both
// sides of the trade move atomically (see api/apply.go's
// applyP2POfferAccept), and both parties get a reputation bump for a
// trade actually completing.
func HandleAcceptP2POffer(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req p2pOfferActionData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	offer, ok := p2pOfferStore.Get(req.OfferID)
	if !ok {
		http.Error(w, "offer not found", http.StatusNotFound)
		return
	}
	if offer.PaymentMode == core.PaymentModeExternal {
		http.Error(w, "this is an EXTERNAL-payment offer - use /p2p/offers/accept-external instead", http.StatusBadRequest)
		return
	}
	if offer.Status != core.OfferOpen {
		http.Error(w, "offer is no longer open", http.StatusBadRequest)
		return
	}
	if offer.Creator == address {
		http.Error(w, "cannot accept your own offer", http.StatusBadRequest)
		return
	}

	wantStore, err := resolveTokenStore(offer.WantSymbol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	wantSide := resolveBalanceAddress(address, auth.PublicKey, offer.WantSymbol)
	if wantStore.BalanceOf(wantSide) < offer.WantAmount {
		http.Error(w, fmt.Sprintf("insufficient %s balance to accept this offer", offer.WantSymbol), http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxP2POfferAccept, string(data))

	notifyUser(offer.Creator, "TRADE_STARTED", fmt.Sprintf("%s accepted your offer to trade %d %s for %d %s", address, offer.OfferAmount, offer.OfferSymbol, offer.WantAmount, offer.WantSymbol), tx.ID)
	notifyUser(address, "TRADE_STARTED", fmt.Sprintf("You accepted a trade for %d %s", offer.WantAmount, offer.WantSymbol), tx.ID)

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleCancelP2POffer lets only the original creator withdraw an
// offer that hasn't been accepted yet, refunding the escrow.
func HandleCancelP2POffer(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req p2pOfferActionData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	offer, ok := p2pOfferStore.Get(req.OfferID)
	if !ok {
		http.Error(w, "offer not found", http.StatusNotFound)
		return
	}
	if offer.Creator != address {
		http.Error(w, "only the creator can cancel this offer", http.StatusForbidden)
		return
	}
	if offer.Status != core.OfferOpen {
		http.Error(w, "offer is no longer open", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxP2POfferCancel, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// p2pOfferEscrowAddress is the same synthetic-holding-address trick
// pool escrows use (see api/apply.go's poolAddress) - a per-offer
// address, keyed by ID, that holds exactly what its creator locked up
// until the offer is accepted or cancelled.
func p2pOfferEscrowAddress(offerID string) string {
	return "P2POFFER:" + offerID
}
