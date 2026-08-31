package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/creditchain/creditchaind/core"
)

func resetSponsoredPostTestState() {
	resetApplyTestState()
	sponsoredPostStore = core.NewSponsoredPostStore()
	reptStore = core.NewTokenStore()
	moderatorStore = core.NewModeratorStore()
	validatorStore = core.NewValidatorStore()
	delegationStore = core.NewDelegationStore()
	treasuryStore.Treasury.FoundationAddress = "sp-test-foundation"
}

func TestHandleCreateSponsoredPost_RejectsInvalidCategory(t *testing.T) {
	resetSponsoredPostTestState()

	pub, priv, _ := core.GenerateKeyPair()
	addr := core.GenerateAddress(pub)
	reptStore.Mint(addr, 1000)
	cctStore.Mint(addr, cctW(1000))

	body := buildSignedEnvelopeJSON(t, pub, priv, sponsoredPostCreateData{
		Text: "Buy my stuff", Category: core.SponsoredPostCategory("NOT_REAL"), CCTAmount: 50,
	})
	req := httptest.NewRequest(http.MethodPost, "/sponsored-posts/create", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleCreateSponsoredPost(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateSponsoredPost_RejectsUnknownValidator(t *testing.T) {
	resetSponsoredPostTestState()

	pub, priv, _ := core.GenerateKeyPair()
	addr := core.GenerateAddress(pub)
	reptStore.Mint(addr, 1000)
	cctStore.Mint(addr, cctW(1000))

	body := buildSignedEnvelopeJSON(t, pub, priv, sponsoredPostCreateData{
		ValidatorID: "no-such-validator", Text: "Stake with us", Category: core.CategoryValidatorStaking, CCTAmount: 50,
	})
	req := httptest.NewRequest(http.MethodPost, "/sponsored-posts/create", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleCreateSponsoredPost(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateSponsoredPost_RejectsSpendOverRCTier(t *testing.T) {
	resetSponsoredPostTestState()

	pub, priv, _ := core.GenerateKeyPair()
	addr := core.GenerateAddress(pub)
	// 0 REPT -> MaxAdSpendForRept caps at 200.
	cctStore.Mint(addr, cctW(1000))

	body := buildSignedEnvelopeJSON(t, pub, priv, sponsoredPostCreateData{
		Text: "General store", Category: core.CategoryGeneralBusiness, CCTAmount: 201,
	})
	req := httptest.NewRequest(http.MethodPost, "/sponsored-posts/create", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleCreateSponsoredPost(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreateSponsoredPost_RejectsInsufficientCctBalance(t *testing.T) {
	resetSponsoredPostTestState()

	pub, priv, _ := core.GenerateKeyPair()
	addr := core.GenerateAddress(pub)
	reptStore.Mint(addr, 1000) // unlocks up to 50000 spend
	// No CCT minted at all.

	body := buildSignedEnvelopeJSON(t, pub, priv, sponsoredPostCreateData{
		Text: "General store", Category: core.CategoryGeneralBusiness, CCTAmount: 100,
	})
	req := httptest.NewRequest(http.MethodPost, "/sponsored-posts/create", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleCreateSponsoredPost(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestSponsoredPost_EndToEnd_CreateReviewFeed drives the real flow:
// submit -> confirm (applySponsoredPostCreate splits the fee and
// assigns a moderator) -> moderator approves (applySponsoredPostReview)
// -> a delegator of the targeted validator sees it in their feed with
// a real Reason, while an unrelated wallet does not.
func TestSponsoredPost_EndToEnd_CreateReviewFeed(t *testing.T) {
	resetSponsoredPostTestState()

	modPub, modPriv, _ := core.GenerateKeyPair()
	modAddr := core.GenerateAddress(modPub)
	moderatorStore.Register(modAddr, hex.EncodeToString(modPub), 1000)

	valPub, _, _ := core.GenerateKeyPair()
	valAddr := core.GenerateAddress(valPub)
	validatorStore.Register(valAddr, "val-node", hex.EncodeToString(valPub))

	advPub, advPriv, _ := core.GenerateKeyPair()
	advAddr := core.GenerateAddress(advPub)
	reptStore.Mint(advAddr, 1000)
	cctStore.Mint(advAddr, cctW(1000))

	delegatorAddr := "sp-test-delegator"
	delegationStore.Delegate(valAddr, delegatorAddr, 500)
	unrelatedAddr := "sp-test-unrelated"

	// Realistic (real wall-clock-based) timestamps, not small fixed
	// numbers - HandleSponsoredPostFeed filters ListActive against a
	// genuine time.Now().Unix(), same convention as
	// yeneClaimStore.CanClaim's HTTP-side liveness checks, so ExpiresAt
	// (computed from these tx timestamps) needs to land in the real
	// future to be considered still active.
	now := time.Now().Unix()
	createTx := signedHubTxAs(t, advPub, advPriv, core.TxSponsoredPostCreate, sponsoredPostCreateData{
		ValidatorID: valAddr, Text: "Stake with us and earn rewards", Category: core.CategoryValidatorStaking, CCTAmount: 100,
	}, now)
	applyTxOnce(createTx)

	post, ok := sponsoredPostStore.Get(createTx.ID)
	if !ok {
		t.Fatal("expected the post to have been created")
	}
	if post.Status != core.SponsoredPostPending {
		t.Fatalf("expected PENDING_REVIEW, got %s", post.Status)
	}
	if post.ModeratorAddress != modAddr {
		t.Fatalf("expected assigned moderator %s, got %s", modAddr, post.ModeratorAddress)
	}
	if got := cctStore.BalanceOf(advAddr); got != cctW(900) {
		t.Fatalf("expected advertiser debited by the full 100 CCT fee, got %s", got)
	}

	reviewTx := signedHubTxAs(t, modPub, modPriv, core.TxSponsoredPostReview, sponsoredPostReviewData{
		ID: createTx.ID, Approve: true,
	}, now+1)
	applyTxOnce(reviewTx)

	post, _ = sponsoredPostStore.Get(createTx.ID)
	if post.Status != core.SponsoredPostActive {
		t.Fatalf("expected ACTIVE after approval, got %s", post.Status)
	}

	// The delegator sees it, with a real reason naming the validator.
	req := httptest.NewRequest(http.MethodGet, "/sponsored-posts/feed?viewer="+delegatorAddr, nil)
	rec := httptest.NewRecorder()
	HandleSponsoredPostFeed(rec, req)

	var entries []sponsoredPostFeedEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != createTx.ID {
		t.Fatalf("expected the delegator to see exactly the one post, got %+v", entries)
	}
	if entries[0].Reason == "" {
		t.Fatal("expected a non-empty Reason - no silent targeting")
	}

	// An unrelated wallet (no delegation to valAddr) does not see it.
	req2 := httptest.NewRequest(http.MethodGet, "/sponsored-posts/feed?viewer="+unrelatedAddr, nil)
	rec2 := httptest.NewRecorder()
	HandleSponsoredPostFeed(rec2, req2)

	var entries2 []sponsoredPostFeedEntry
	if err := json.Unmarshal(rec2.Body.Bytes(), &entries2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries2) != 0 {
		t.Fatalf("expected an unrelated viewer to see no validator-scoped posts, got %+v", entries2)
	}
}

func TestHandleReviewSponsoredPost_RefusesWrongModerator(t *testing.T) {
	resetSponsoredPostTestState()

	modPub, _, _ := core.GenerateKeyPair()
	modAddr := core.GenerateAddress(modPub)
	moderatorStore.Register(modAddr, hex.EncodeToString(modPub), 1000)

	impostorPub, impostorPriv, _ := core.GenerateKeyPair()

	sponsoredPostStore.Create(&core.SponsoredPost{
		ID: "post-1", ModeratorAddress: modAddr, Status: core.SponsoredPostPending,
	})

	body := buildSignedEnvelopeJSON(t, impostorPub, impostorPriv, sponsoredPostReviewData{ID: "post-1", Approve: true})
	req := httptest.NewRequest(http.MethodPost, "/sponsored-posts/review", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleReviewSponsoredPost(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
