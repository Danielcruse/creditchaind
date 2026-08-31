package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// This file is the "advertiser pays CCT, a moderator reviews, an
// eligible viewer sees it in the governance feed" flow discussed at
// length with the user: CCT pays (splitAdFee, api/ad_fee_split.go), RC
// (REPT) gates how much an advertiser may spend on one post
// (core.MaxAdSpendForRept), and Trust Index ranks placement among
// several eligible posts (see HandleSponsoredPostFeed). A post scoped
// to a validator (ValidatorID != "") is only ever shown to a wallet
// that has actually delegated CCT to that validator
// (delegationStore.DelegatedBy) - real, already-existing on-chain
// state, not a new follow/subscribe relationship. Deliberately does
// NOT do any geo/region targeting or transaction-counterparty-footprint
// reach extension - both were explicitly scoped OUT of v1 (see the
// implementation plan this was built from).
var sponsoredPostStore = core.NewSponsoredPostStore()

// sponsoredPostRunLengthSeconds is how long an APPROVED post stays
// ACTIVE - a fixed run length CCT buys, not unlimited display (see
// core.SponsoredPost.ExpiresAt's doc comment). A disclosed judgment
// call, not a locked spec value.
const sponsoredPostRunLengthSeconds = 7 * 24 * 3600 // 7 days

// minSponsoredPostCCT is the anti-spam floor on a single post's spend -
// separate from core.MaxAdSpendForRept's upper bound, same "a flat
// floor plus a reputation-scaled ceiling" shape as
// HubMinimumStakeCCT/HubStakeVaultMaxCCT (api/companies.go).
const minSponsoredPostCCT = 10

type sponsoredPostCreateData struct {
	ValidatorID string                     `json:"validatorId"` // "" = global reach
	Text        string                     `json:"text"`
	Category    core.SponsoredPostCategory `json:"category"`
	CCTAmount   uint64                     `json:"cctAmount"`
}

// HandleCreateSponsoredPost lets any wallet submit a Sponsored Post for
// moderator review - fast-fail checks mirror HandleApplyCompany's shape
// (balance/eligibility checked here, re-checked and only actually
// charged at confirmation time in applySponsoredPostCreate).
func HandleCreateSponsoredPost(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req sponsoredPostCreateData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if len(req.Text) == 0 || len(req.Text) > 500 {
		http.Error(w, "text must be 1-500 characters", http.StatusBadRequest)
		return
	}
	if !core.IsAllowedSponsoredPostCategory(req.Category) {
		http.Error(w, "category must be a valid selection from the list", http.StatusBadRequest)
		return
	}
	if req.ValidatorID != "" {
		if _, ok := validatorStore.Get(req.ValidatorID); !ok {
			http.Error(w, "validatorId is not a currently-registered validator", http.StatusNotFound)
			return
		}
	}
	if req.CCTAmount < minSponsoredPostCCT {
		http.Error(w, "cctAmount is below the minimum spend for a Sponsored Post", http.StatusBadRequest)
		return
	}
	maxSpend := core.MaxAdSpendForRept(reptStore.BalanceOf(address))
	if req.CCTAmount > maxSpend {
		http.Error(w, "cctAmount exceeds what your current RC (REPT) balance allows for a single post - build more reputation to unlock a larger campaign", http.StatusForbidden)
		return
	}
	if !hasCctBalance(address, req.CCTAmount) {
		http.Error(w, "insufficient CCT balance to cover cctAmount", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxSponsoredPostCreate, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type sponsoredPostReviewData struct {
	ID      string `json:"id"`
	Approve bool   `json:"approve"`
}

// HandleReviewSponsoredPost is the assigned moderator's approve/reject
// decision - only the assigned moderator may answer, and only once
// (core.SponsoredPostStore.Review enforces this at confirmation time,
// same as CompanyStore.Review's identical rule).
func HandleReviewSponsoredPost(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req sponsoredPostReviewData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	post, ok := sponsoredPostStore.Get(req.ID)
	if !ok {
		http.Error(w, "post not found", http.StatusNotFound)
		return
	}
	if post.ModeratorAddress != address {
		http.Error(w, "this post wasn't assigned to you", http.StatusForbidden)
		return
	}
	if post.Status != core.SponsoredPostPending {
		http.Error(w, "this post has already been reviewed", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxSponsoredPostReview, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleListMySponsoredPosts is an advertiser's own submission history
// - every post they've ever submitted, regardless of status, so a
// rejected or expired one stays visible rather than vanishing (same
// reasoning as every other list endpoint in this codebase).
func HandleListMySponsoredPosts(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "missing address", http.StatusBadRequest)
		return
	}

	json.NewEncoder(w).Encode(sponsoredPostStore.ForAdvertiser(address))
}

// HandleListAssignedSponsoredPosts is a moderator's own review queue -
// same purpose as api/moderation.js's refreshAssignedCompanies, just
// for Sponsored Posts instead of Trading Hub applications. Includes
// whether the category needs the numeric-claims review lane
// (core.MakesNumericClaims) so the moderator UI can show/hide that
// panel without a second request.
type sponsoredPostReviewView struct {
	*core.SponsoredPost
	MakesNumericClaims bool `json:"makesNumericClaims"`
}

func HandleListAssignedSponsoredPosts(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	moderator := r.URL.Query().Get("moderator")
	if moderator == "" {
		http.Error(w, "missing moderator", http.StatusBadRequest)
		return
	}

	posts := sponsoredPostStore.ForModerator(moderator)
	views := make([]sponsoredPostReviewView, 0, len(posts))
	for _, p := range posts {
		views = append(views, sponsoredPostReviewView{SponsoredPost: p, MakesNumericClaims: core.MakesNumericClaims(p.Category)})
	}

	json.NewEncoder(w).Encode(views)
}

// sponsoredPostFeedEntry is one post as served to a specific viewer -
// Reason is always populated (never a silent/unexplained placement),
// per the "no silent targeting" requirement this feature was designed
// around.
type sponsoredPostFeedEntry struct {
	*core.SponsoredPost
	Sponsored bool   `json:"sponsored"`
	Reason    string `json:"reason"`
}

// HandleSponsoredPostFeed returns every ACTIVE post a viewer is
// eligible to see right now: every global-reach post, plus every post
// scoped to a validator the viewer has actually delegated CCT to (see
// delegationStore.DelegatedBy) - never a post scoped to a validator
// they have no real relationship with. Ranked by the viewer's own Trust
// Index descending purely as a display-order signal (a higher-trust
// viewer sees higher-priority placement first) - this does NOT gate
// eligibility, only ordering, so it can never be the reason a post is
// hidden. viewer="" (no wallet) still sees every global-reach post
// (Reason: "Shown to everyone"), just no validator-scoped ones (no
// delegation to check against).
func HandleSponsoredPostFeed(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	viewer := r.URL.Query().Get("viewer")
	now := time.Now().Unix()

	var entries []sponsoredPostFeedEntry
	for _, p := range sponsoredPostStore.ListActive("", now) {
		entries = append(entries, sponsoredPostFeedEntry{SponsoredPost: p, Sponsored: true, Reason: "Shown to everyone"})
	}

	if viewer != "" {
		for _, v := range validatorStore.List() {
			if delegationStore.DelegatedBy(v.Address, viewer) == 0 {
				continue
			}
			for _, p := range sponsoredPostStore.ListActive(v.Address, now) {
				entries = append(entries, sponsoredPostFeedEntry{
					SponsoredPost: p, Sponsored: true,
					Reason: "Shown because you delegate to " + v.Address,
				})
			}
		}
	}

	sortSponsoredFeedByTrust(entries)

	json.NewEncoder(w).Encode(entries)
}

// sortSponsoredFeedByTrust is a placement-order-only signal (never an
// eligibility gate, see HandleSponsoredPostFeed's doc comment) - a
// simple stable sort by each advertiser's own Trust Index, higher
// first, so a more-trusted advertiser's post tends to surface earlier
// in the interleaved feed without ever excluding a lower-trust one.
func sortSponsoredFeedByTrust(entries []sponsoredPostFeedEntry) {
	for i := 1; i < len(entries); i++ {
		j := i
		for j > 0 {
			a := core.TrustDimensionValue(walletStore.Get(entries[j].Advertiser), core.DimensionContribution)
			b := core.TrustDimensionValue(walletStore.Get(entries[j-1].Advertiser), core.DimensionContribution)
			if a <= b {
				break
			}
			entries[j], entries[j-1] = entries[j-1], entries[j]
			j--
		}
	}
}
