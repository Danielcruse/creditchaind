package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var ratingStore = core.NewRatingStore()

const maxRatingCommentLen = 280

// goodRatingStarsThreshold is a judgment call ("anyone that rates us
// good"), not derived from any specified model - 4 stars, not just 5,
// counts as "good" for the purposes of the one-time CCT reward.
const goodRatingStarsThreshold = 4

type ratingSubmitData struct {
	Stars   uint8  `json:"stars"`
	Comment string `json:"comment"`
}

// HandleSubmitRating lets any wallet leave one site rating, ever - a
// second submission overwrites the first (ratingStore.Set) but never
// pays out twice, see applyRatingSubmit.
func HandleSubmitRating(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req ratingSubmitData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Stars < 1 || req.Stars > 5 {
		http.Error(w, "stars must be 1-5", http.StatusBadRequest)
		return
	}
	if len(req.Comment) > maxRatingCommentLen {
		http.Error(w, "comment must be under 280 characters", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(ratingSubmitData{Stars: req.Stars, Comment: req.Comment})
	tx := recordTx(auth, address, "", 0, core.TxRatingSubmit, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleMyRating is a public read (no auth needed - a rating carries
// no value/authority of its own to protect).
func HandleMyRating(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "address required", http.StatusBadRequest)
		return
	}

	rating, ok := ratingStore.Get(address)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{"rated": false})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"rated": true, "rating": rating})
}

// HandleRecentRatings is the public feed shown on validator.html's
// gated apply-only view (see §7 of the plan) and anywhere else a
// social-proof list of ratings is useful.
func HandleRecentRatings(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	json.NewEncoder(w).Encode(ratingStore.Recent(20))
}

// applyRatingSubmit records tx.From's rating and, only the first time
// that address has ever rated (ratingStore.Set returns true) and only
// if the rating is "good" (>= goodRatingStarsThreshold), pays a flat
// 1 CCT reward straight to tx.From - literally "1 CCT from admin
// Treasury" per the user's own wording, via tokenStore.Transfer out of
// treasuryStore.Treasury.FoundationAddress (the same real Treasury
// balance HandleAdminConfirmPurchase already pays buyers from - see
// api/treasury.go), not a fresh mint. Same silent-no-op-on-
// insufficient-balance convention applyTreasuryPurchaseConfirm already
// uses: if the Treasury is too low, the rating still records, it just
// doesn't pay - a known, disclosed rough edge, not a crash.
func applyRatingSubmit(tx core.Transaction) {
	var data ratingSubmitData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	isFirst := ratingStore.Set(&core.Rating{
		Address:   tx.From,
		Stars:     data.Stars,
		Comment:   data.Comment,
		CreatedAt: tx.Timestamp,
	})

	if isFirst && data.Stars >= goodRatingStarsThreshold {
		tokenStore.Transfer(treasuryStore.Treasury.FoundationAddress, tx.From, 1)
	}
}
