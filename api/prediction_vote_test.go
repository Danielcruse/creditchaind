package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// resetPredictionTestState extends resetRCGovernanceTestState with the
// prediction-market-specific package vars this file's tests mutate.
func resetPredictionTestState() {
	resetRCGovernanceTestState()
	predictionPoolStore = core.NewPredictionPoolStore()
	cctBtcTestPool = core.NewLiquidityPool("BTCTEST")
	cctEthTestPool = core.NewLiquidityPool("ETHTEST")
}

// newPredictionProposal creates an Open PREDICTION proposal directly
// (bypassing applyGovernance's "create" action, which isn't the
// subject of these tests) with a low quorum so the ordinary headcount
// vote isn't what's under test here.
func newPredictionProposal(id, targetSymbol string, targetPrice uint64, deadline, duration int64) *core.Proposal {
	p := &core.Proposal{
		ID:                        id,
		CreatedBy:                 "pred-creator",
		Open:                      true,
		Quorum:                    1,
		Deadline:                  deadline,
		Kind:                      core.ProposalKindPrediction,
		PredictionTargetSymbol:    targetSymbol,
		PredictionTargetPrice:     targetPrice,
		PredictionDurationSeconds: duration,
	}
	proposalStore.Create(p)
	return p
}

func TestApplyPredictionVote_RegistersHeadcountAndLazilyCreatesPoolOnRCBet(t *testing.T) {
	resetPredictionTestState()
	newActivatedVoter(t, "pred-voter-a", 100, core.StakeCap)
	reptStore.Mint("pred-voter-a", 50)
	cctAdapter.Mint("pred-voter-a", 10)

	p := newPredictionProposal("prop-pred-1", "BTCTEST", 1000000, 999999, PredictionDefaultDurationSeconds)

	// A plain vote (no RC) must register the headcount vote and NOT
	// create a pool.
	tx := core.Transaction{
		ID: "pred-vote-1", From: "pred-voter-a", Type: core.TxPredictionVote, Timestamp: 1000,
		Data: `{"proposalId":"prop-pred-1","side":"YES","rcAmount":0}`,
	}
	applyPredictionVote(tx)

	if p.YesVotes == 0 {
		t.Fatalf("expected the ordinary headcount Yes vote to register")
	}
	if predictionPoolStore.Get("prop-pred-1") != nil {
		t.Fatalf("expected no pool to exist yet - no RC was attached")
	}
	if got := cctAdapter.BalanceOf("pred-voter-a"); got != 10 {
		t.Fatalf("expected no CCT fee charged on a plain (RC-less) vote, got balance %d", got)
	}

	// A second wallet's RC-backed vote must create the pool AND charge
	// the CCT fee.
	newActivatedVoter(t, "pred-voter-b", 100, core.StakeCap)
	reptStore.Mint("pred-voter-b", 50)
	cctAdapter.Mint("pred-voter-b", 10)

	tx2 := core.Transaction{
		ID: "pred-vote-2", From: "pred-voter-b", Type: core.TxPredictionVote, Timestamp: 1001,
		Data: `{"proposalId":"prop-pred-1","side":"YES","rcAmount":20}`,
	}
	applyPredictionVote(tx2)

	pool := predictionPoolStore.Get("prop-pred-1")
	if pool == nil {
		t.Fatalf("expected the pool to spring into existence on the first RC-backed vote")
	}
	if pool.YesPoolRC != 20 {
		t.Fatalf("expected YesPoolRC=20, got %d", pool.YesPoolRC)
	}
	if got := reptStore.BalanceOf("pred-voter-b"); got != 30 {
		t.Fatalf("expected 20 RC burned from voter-b (50->30), got %d", got)
	}
	if got := cctAdapter.BalanceOf("pred-voter-b"); got != 10-PredictionVoteFeeCCT {
		t.Fatalf("expected the CCT prediction-vote fee charged, got balance %d", got)
	}
	if got := cctAdapter.BalanceOf(cctTransferFeePoolAddress()); got != PredictionVoteFeeCCT {
		t.Fatalf("expected the fee to land in the shared CCT fee pool, got %d", got)
	}
}

func TestApplyPredictionVote_RejectsDoubleVoteAndInsufficientRC(t *testing.T) {
	resetPredictionTestState()
	newActivatedVoter(t, "pred-dup-voter", 100, core.StakeCap)
	reptStore.Mint("pred-dup-voter", 5)
	cctAdapter.Mint("pred-dup-voter", 10)

	newPredictionProposal("prop-pred-dup", "BTCTEST", 1000000, 999999, PredictionDefaultDurationSeconds)

	// Insufficient RC: the apply-side check must reject it, no burn, no
	// headcount vote registered either (since the whole tx is invalid).
	tx := core.Transaction{
		ID: "pred-dup-1", From: "pred-dup-voter", Type: core.TxPredictionVote, Timestamp: 1000,
		Data: `{"proposalId":"prop-pred-dup","side":"YES","rcAmount":100}`,
	}
	applyPredictionVote(tx)
	if got := reptStore.BalanceOf("pred-dup-voter"); got != 5 {
		t.Fatalf("expected no RC burned on an insufficient-balance RC bet, got %d", got)
	}

	// A valid plain vote registers once...
	tx2 := core.Transaction{
		ID: "pred-dup-2", From: "pred-dup-voter", Type: core.TxPredictionVote, Timestamp: 1001,
		Data: `{"proposalId":"prop-pred-dup","side":"YES","rcAmount":0}`,
	}
	applyPredictionVote(tx2)
	p, _ := proposalStore.Get("prop-pred-dup")
	firstYes := p.YesVotes
	if firstYes == 0 {
		t.Fatalf("expected the first vote to register")
	}

	// ...and a second vote from the same address must be rejected
	// (reusing proposalStore's own HasVoted dedup).
	tx3 := core.Transaction{
		ID: "pred-dup-3", From: "pred-dup-voter", Type: core.TxPredictionVote, Timestamp: 1002,
		Data: `{"proposalId":"prop-pred-dup","side":"NO","rcAmount":0}`,
	}
	applyPredictionVote(tx3)
	if p.YesVotes != firstYes || p.NoVotes != 0 {
		t.Fatalf("expected the second vote from the same address to be rejected, got yes=%d no=%d", p.YesVotes, p.NoVotes)
	}
}

// seedBtcTestPoolPrice sets cctBtcTestPool's reserves to produce a
// known, exact live price via core.CurrentPoolPriceScaled - CCTReserve
// (smallest-unit decimal string) * priceScale / TokenReserve = 6 *
// 1,000,000 / 2 = 3,000,000.
func seedBtcTestPoolPrice() {
	cctBtcTestPool.CCTReserve = "6"
	cctBtcTestPool.TokenReserve = 2
}

func TestProcessPredictionResolution_ProRataPayoutOnWinningSide(t *testing.T) {
	resetPredictionTestState()
	seedBtcTestPoolPrice() // live price = 3,000,000

	p := newPredictionProposal("prop-pred-resolve", "BTCTEST", 3000000, 1000, PredictionDefaultDurationSeconds) // target MET (price >= target)

	// 3 infusers on YES (winning side): 10/20/30 RC. 1 infuser on NO
	// (losing side): 60 RC - entirely redistributed pro-rata to the
	// three YES infusers by their own share of the 60 YES total.
	predictionPoolStore.Infuse(&core.PredictionInfusion{ProposalID: p.ID, Address: "winner-a", Side: core.PredictionSideYes, RCAmount: 10, CreatedAt: 1})
	predictionPoolStore.Infuse(&core.PredictionInfusion{ProposalID: p.ID, Address: "winner-b", Side: core.PredictionSideYes, RCAmount: 20, CreatedAt: 1})
	predictionPoolStore.Infuse(&core.PredictionInfusion{ProposalID: p.ID, Address: "winner-c", Side: core.PredictionSideYes, RCAmount: 30, CreatedAt: 1})
	predictionPoolStore.Infuse(&core.PredictionInfusion{ProposalID: p.ID, Address: "loser-a", Side: core.PredictionSideNo, RCAmount: 60, CreatedAt: 1})

	processPredictionResolution(p, 1000)

	pool := predictionPoolStore.Get(p.ID)
	if !pool.Resolved || pool.WinningSide != core.PredictionSideYes {
		t.Fatalf("expected resolved with WinningSide=YES, got resolved=%v side=%q", pool.Resolved, pool.WinningSide)
	}

	// payout(infusion) = infusion * losingPoolRC / winningPoolRC
	// winner-a: 10 * 60/60 = 10 -> total 10+10=20
	// winner-b: 20 * 60/60 = 20 -> total 20+20=40
	// winner-c: 30 * 60/60 = 30 -> total 30+30=60
	if got := reptStore.BalanceOf("winner-a"); got != 20 {
		t.Fatalf("expected winner-a minted 20 (10 stake + 10 payout), got %d", got)
	}
	if got := reptStore.BalanceOf("winner-b"); got != 40 {
		t.Fatalf("expected winner-b minted 40, got %d", got)
	}
	if got := reptStore.BalanceOf("winner-c"); got != 60 {
		t.Fatalf("expected winner-c minted 60, got %d", got)
	}
	if got := reptStore.BalanceOf("loser-a"); got != 0 {
		t.Fatalf("expected the loser to receive nothing (their RC was already burned at vote time), got %d", got)
	}
}

func TestProcessPredictionResolution_NoWinningInfusersDoesNotPanicOrMisdirectFunds(t *testing.T) {
	resetPredictionTestState()
	seedBtcTestPoolPrice() // live price = 3,000,000

	// Target NOT met (target price above the live price) -> NO wins,
	// but only YES has any infusers. Nobody should be paid.
	p := newPredictionProposal("prop-pred-nowinners", "BTCTEST", 9999999, 1000, PredictionDefaultDurationSeconds)
	predictionPoolStore.Infuse(&core.PredictionInfusion{ProposalID: p.ID, Address: "only-yes", Side: core.PredictionSideYes, RCAmount: 15, CreatedAt: 1})

	processPredictionResolution(p, 1000)

	pool := predictionPoolStore.Get(p.ID)
	if !pool.Resolved || pool.WinningSide != core.PredictionSideNo {
		t.Fatalf("expected resolved with WinningSide=NO (target not met), got resolved=%v side=%q", pool.Resolved, pool.WinningSide)
	}
	if got := reptStore.BalanceOf("only-yes"); got != 0 {
		t.Fatalf("expected the sole (losing-side) infuser to receive nothing, got %d", got)
	}
}

func TestProcessPredictionResolution_LongPoolBonusTriggersAtThresholdAndCaps(t *testing.T) {
	resetPredictionTestState()
	seedBtcTestPoolPrice()

	duration := int64(PredictionLongPoolBonusThresholdSeconds) // exactly the boundary
	p := newPredictionProposal("prop-pred-bonus", "BTCTEST", 3000000, 1000+duration, duration)
	predictionPoolStore.Infuse(&core.PredictionInfusion{ProposalID: p.ID, Address: "bonus-yes", Side: core.PredictionSideYes, RCAmount: 100, CreatedAt: 1000})
	predictionPoolStore.Infuse(&core.PredictionInfusion{ProposalID: p.ID, Address: "bonus-no", Side: core.PredictionSideNo, RCAmount: 100, CreatedAt: 1000})

	asOf := p.Deadline // createdAt = Deadline - duration = 1000, so asOf-createdAt == duration exactly
	processPredictionResolution(p, asOf)

	pool := predictionPoolStore.Get(p.ID)
	wantBonus := (uint64(100) + 100) * PredictionLongPoolBonusMaxBps / 10000 // 10% of 200 = 20
	if pool.BonusMinted != wantBonus {
		t.Fatalf("expected BonusMinted=%d at exactly the 14-day boundary, got %d", wantBonus, pool.BonusMinted)
	}

	// Bonus is split pro-rata across BOTH sides (not just the winner) -
	// bonus-yes and bonus-no each staked half the total, so each gets
	// half the bonus, on top of whatever their win/loss payout was.
	// bonus-yes wins (target met): stake 100 back + payout (100*100/100=100) + bonus share (20*100/200=10) = 210
	if got := reptStore.BalanceOf("bonus-yes"); got != 210 {
		t.Fatalf("expected bonus-yes minted 210 (100 stake + 100 payout + 10 bonus), got %d", got)
	}
	// bonus-no loses: 0 payout, but still gets its bonus share (10)
	if got := reptStore.BalanceOf("bonus-no"); got != 10 {
		t.Fatalf("expected bonus-no minted 10 (bonus share only, no payout on a loss), got %d", got)
	}
}

func TestProcessPredictionResolution_NoBonusBeforeThreshold(t *testing.T) {
	resetPredictionTestState()
	seedBtcTestPoolPrice()

	duration := int64(PredictionLongPoolBonusThresholdSeconds - 1) // just under the boundary
	p := newPredictionProposal("prop-pred-nobonus", "BTCTEST", 3000000, 1000+duration, duration)
	predictionPoolStore.Infuse(&core.PredictionInfusion{ProposalID: p.ID, Address: "nobonus-yes", Side: core.PredictionSideYes, RCAmount: 50, CreatedAt: 1000})

	processPredictionResolution(p, p.Deadline)

	pool := predictionPoolStore.Get(p.ID)
	if pool.BonusMinted != 0 {
		t.Fatalf("expected no bonus minted just under the 14-day threshold, got %d", pool.BonusMinted)
	}
}

func TestFinalizeProposal_PredictionWithNoRCVotesFinalizesLikeAPlainProposal(t *testing.T) {
	resetPredictionTestState()
	seedBtcTestPoolPrice()

	newActivatedVoter(t, "pred-plain-voter", 100, core.StakeCap)
	p := newPredictionProposal("prop-pred-plain", "BTCTEST", 3000000, 1000, PredictionDefaultDurationSeconds)
	if _, err := proposalStore.Vote(p.ID, "pred-plain-voter", true, votingWeight("pred-plain-voter"), 999); err != nil {
		t.Fatalf("vote: %v", err)
	}

	// Must not panic touching a nonexistent PredictionPool, and must
	// still finalize the ordinary headcount PASSED/FAILED outcome.
	finalizeProposal(p, 1000)

	if predictionPoolStore.Get(p.ID) != nil {
		t.Fatalf("expected no PredictionPool to have been created for a proposal with zero RC votes")
	}
	if p.Open {
		t.Fatalf("expected the proposal to be closed after finalization")
	}
}
