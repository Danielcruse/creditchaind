package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var tradeReviewStore = core.NewTradeReviewStore()

// TradeReviewMinStarsForBonus mirrors Rating's own precedent
// (core/rating.go's doc comment: "a rating of 4 or 5 is what
// applyRatingSubmit treats as good enough to pay out") - only a
// genuinely positive review grows the reviewee's reputation. A 1-3
// star review is still recorded (visible on the reviewee's own trade-
// review history) but grants no reputation, for the same reason
// applyModeratorConfirm only rewards an undisputed confirmation: a
// mediocre-or-worse review is legitimate feedback, not something to
// incentivize away from by paying it out.
const TradeReviewMinStarsForBonus = 4

// TradeReviewReputationBonus matches ModeratorConfirmReputationBonus's
// own conservative +1 precedent - kept deliberately small, on top of
// TradeReviewStore's own per-pair cap (see that type's doc comment),
// since reputation feeds both PoSR validator weight and governance
// voting power.
const TradeReviewReputationBonus = 1

type tradeReviewData struct {
	TradeID string `json:"tradeId"`
	Stars   uint8  `json:"stars"`
	Comment string `json:"comment"`
}

// tradeReviewEligibleStatuses is exactly the "trade succeeded" set the
// Swipe-page chat modal itself already treats as concluded (see
// website/js/p2p.js's TERMINAL_SETTLEMENT_STATUSES) minus the two
// non-success terminal outcomes (DISPUTED with no resolution yet,
// EXPIRED, RESOLVED_REFUND) - a review is about whether the OTHER
// PARTY behaved well in a trade that actually completed, not a verdict
// on a trade that fell through.
var tradeReviewEligibleStatuses = map[core.SettlementStatus]bool{
	core.SettlementConfirmed:       true,
	core.SettlementAutoReleased:    true,
	core.SettlementResolvedRelease: true,
}

// counterpartyFor returns the other side of offer relative to address,
// and whether address is actually a participant at all.
func counterpartyFor(o *core.P2POffer, address string) (string, bool) {
	switch address {
	case o.Creator:
		return o.AcceptedBy, true
	case o.AcceptedBy:
		return o.Creator, true
	default:
		return "", false
	}
}

// HandleSubmitTradeReview lets either side of a successfully-settled
// P2P offer review the other - see core/trade_review.go's doc comment
// for what "successfully settled" means and why the resulting
// reputation bonus is capped per reviewer->reviewee pair.
func HandleSubmitTradeReview(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req tradeReviewData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Stars < 1 || req.Stars > 5 {
		http.Error(w, "stars must be between 1 and 5", http.StatusBadRequest)
		return
	}

	o, ok := p2pOfferStore.Get(req.TradeID)
	if !ok {
		http.Error(w, "trade not found", http.StatusNotFound)
		return
	}
	if !tradeReviewEligibleStatuses[o.SettlementStatus] {
		http.Error(w, "this trade hasn't settled successfully yet", http.StatusBadRequest)
		return
	}
	if _, isParticipant := counterpartyFor(o, address); !isParticipant {
		http.Error(w, "you weren't a party to this trade", http.StatusForbidden)
		return
	}
	if tradeReviewStore.HasReviewed(req.TradeID, address) {
		http.Error(w, "you've already reviewed this trade", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxTradeReview, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type tradeReviewInfo struct {
	TradeID   string `json:"tradeId"`
	Reviewer  string `json:"reviewer"`
	Reviewee  string `json:"reviewee"`
	Stars     uint8  `json:"stars"`
	Comment   string `json:"comment"`
	CreatedAt int64  `json:"createdAt"`
}

func toTradeReviewInfo(r *core.TradeReview) tradeReviewInfo {
	return tradeReviewInfo{
		TradeID: r.TradeID, Reviewer: r.Reviewer, Reviewee: r.Reviewee,
		Stars: r.Stars, Comment: r.Comment, CreatedAt: r.CreatedAt,
	}
}

// HandleGetTradeReviewsForAddress is a public read of every review a
// wallet has ever received - what a profile page shows.
func HandleGetTradeReviewsForAddress(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "address required", http.StatusBadRequest)
		return
	}

	reviews := tradeReviewStore.ForReviewee(address)
	out := make([]tradeReviewInfo, 0, len(reviews))
	for _, r := range reviews {
		out = append(out, toTradeReviewInfo(r))
	}
	json.NewEncoder(w).Encode(out)
}

// HandleGetTradeReviewStatus tells the Swipe-page chat modal exactly
// what it needs to decide whether to show the review prompt: whether
// THIS trade has settled into a reviewable state, and which of the two
// participants (by address) have already reviewed.
func HandleGetTradeReviewStatus(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	tradeID := r.URL.Query().Get("tradeId")
	if tradeID == "" {
		http.Error(w, "tradeId required", http.StatusBadRequest)
		return
	}

	o, ok := p2pOfferStore.Get(tradeID)
	if !ok {
		http.Error(w, "trade not found", http.StatusNotFound)
		return
	}

	reviewed := make(map[string]bool)
	for _, rev := range tradeReviewStore.ForTrade(tradeID) {
		reviewed[rev.Reviewer] = true
	}

	json.NewEncoder(w).Encode(map[string]any{
		"eligible":        tradeReviewEligibleStatuses[o.SettlementStatus],
		"creatorReviewed": reviewed[o.Creator],
		"buyerReviewed":   reviewed[o.AcceptedBy],
	})
}
