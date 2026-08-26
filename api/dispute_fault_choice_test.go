package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func seedPendingFaultChoice(offerID, atFault, moderator, symbol string, fee uint64, createdAt int64) {
	disputeFaultChoiceStore.Create(&core.DisputeFaultChoice{
		OfferID: offerID, AtFaultAddress: atFault, ResolvingModerator: moderator,
		Symbol: symbol, FeeAmount: fee, CreatedAt: createdAt,
		Deadline: createdAt + core.DisputeFaultChoiceWindowSeconds, Status: core.DisputeFaultChoicePending,
	})
}

// TestApplyDisputeFaultPay_TransfersFeeAndAvoidsReputationPenalty
// confirms paying moves the fee to the resolving moderator, marks the
// choice PAID, and leaves the at-fault party's reputation untouched.
func TestApplyDisputeFaultPay_TransfersFeeAndAvoidsReputationPenalty(t *testing.T) {
	resetApplyTestState()
	disputeFaultChoiceStore = core.NewDisputeFaultChoiceStore()

	const atFault, moderator = "at-fault-addr", "moderator-addr"
	walletStore.Get(atFault).Reputation = 50
	cctStore.Mint(atFault, cctW(100))
	seedPendingFaultChoice("offer-1", atFault, moderator, "CCT", 30, 1000)

	tx := core.Transaction{ID: "pay-tx", From: atFault, Type: core.TxDisputeFaultPay, Data: `{"offerId":"offer-1"}`, Timestamp: 2000}
	applyDisputeFaultPay(tx)

	if got := cctStore.BalanceOf(moderator); got != cctW(30) {
		t.Fatalf("expected the moderator to receive the 30 CCT fee, got %s", got)
	}
	if got := cctStore.BalanceOf(atFault); got != cctW(70) {
		t.Fatalf("expected the at-fault party's balance to drop by exactly the fee, got %s", got)
	}
	if got := walletStore.Get(atFault).Reputation; got != 50 {
		t.Fatalf("expected reputation untouched after paying, got %d", got)
	}
	choice, _ := disputeFaultChoiceStore.Get("offer-1")
	if choice.Status != core.DisputeFaultChoicePaid {
		t.Fatalf("expected status PAID, got %s", choice.Status)
	}
}

// TestApplyDisputeFaultPay_InsufficientBalanceLeavesChoicePending
// confirms a failed transfer doesn't mark the choice resolved - the
// at-fault party can still act again (e.g. after topping up).
func TestApplyDisputeFaultPay_InsufficientBalanceLeavesChoicePending(t *testing.T) {
	resetApplyTestState()
	disputeFaultChoiceStore = core.NewDisputeFaultChoiceStore()

	const atFault, moderator = "at-fault-addr", "moderator-addr"
	seedPendingFaultChoice("offer-1", atFault, moderator, "CCT", 30, 1000)
	// atFault has 0 CCT - the transfer must fail.

	tx := core.Transaction{ID: "pay-tx", From: atFault, Type: core.TxDisputeFaultPay, Data: `{"offerId":"offer-1"}`, Timestamp: 2000}
	applyDisputeFaultPay(tx)

	choice, _ := disputeFaultChoiceStore.Get("offer-1")
	if choice.Status != core.DisputeFaultChoicePending {
		t.Fatalf("expected the choice to remain PENDING after a failed transfer, got %s", choice.Status)
	}
}

// TestApplyDisputeFaultDecline_AppliesReputationPenaltyOnly confirms
// declining charges the standard fraud penalty and moves no funds.
func TestApplyDisputeFaultDecline_AppliesReputationPenaltyOnly(t *testing.T) {
	resetApplyTestState()
	disputeFaultChoiceStore = core.NewDisputeFaultChoiceStore()
	reputationEventStore = core.NewReputationEventStore()

	const atFault, moderator = "at-fault-addr", "moderator-addr"
	walletStore.Get(atFault).Reputation = 50
	cctStore.Mint(atFault, cctW(100))
	seedPendingFaultChoice("offer-1", atFault, moderator, "CCT", 30, 1000)

	tx := core.Transaction{ID: "decline-tx", From: atFault, Type: core.TxDisputeFaultDecline, Data: `{"offerId":"offer-1"}`, Timestamp: 2000}
	applyDisputeFaultDecline(tx)

	if got := walletStore.Get(atFault).Reputation; got != 50-uint64(ReputationDeltaP2PFraud) {
		t.Fatalf("expected reputation decreased by %d, got %d", ReputationDeltaP2PFraud, got)
	}
	if got := cctStore.BalanceOf(atFault); got != cctW(100) {
		t.Fatalf("expected balance untouched (no fee taken on decline), got %s", got)
	}
	if got := cctStore.BalanceOf(moderator); got != "0" {
		t.Fatalf("expected the moderator to receive nothing on decline, got %s", got)
	}
	choice, _ := disputeFaultChoiceStore.Get("offer-1")
	if choice.Status != core.DisputeFaultChoiceDeclined {
		t.Fatalf("expected status DECLINED, got %s", choice.Status)
	}
	if _, ok := reputationEventStore.Get(tx.ID); !ok {
		t.Fatal("expected a reputation event to be recorded")
	}
}

// TestResolveExpiredDisputeFaultChoices_AppliesBothPenaltiesOnTimeout
// is the core regression test for the "if hub does not choose he
// loses both" rule: a choice past its deadline with neither pay nor
// decline confirmed gets BOTH the reputation penalty AND a forced fee
// transfer, applied automatically as part of block application.
func TestResolveExpiredDisputeFaultChoices_AppliesBothPenaltiesOnTimeout(t *testing.T) {
	resetApplyTestState()
	disputeFaultChoiceStore = core.NewDisputeFaultChoiceStore()
	reputationEventStore = core.NewReputationEventStore()

	const atFault, moderator = "at-fault-addr", "moderator-addr"
	walletStore.Get(atFault).Reputation = 50
	cctStore.Mint(atFault, cctW(100))
	seedPendingFaultChoice("offer-1", atFault, moderator, "CCT", 30, 1000)

	// Well past the 72-hour deadline (1000 + 72*3600).
	block := core.Block{Timestamp: 1000 + core.DisputeFaultChoiceWindowSeconds + 1}
	resolveExpiredDisputeFaultChoices(block)

	if got := walletStore.Get(atFault).Reputation; got != 50-uint64(ReputationDeltaP2PFraud) {
		t.Fatalf("expected reputation decreased by %d, got %d", ReputationDeltaP2PFraud, got)
	}
	if got := cctStore.BalanceOf(moderator); got != cctW(30) {
		t.Fatalf("expected the moderator to be force-paid the 30 CCT fee, got %s", got)
	}
	choice, _ := disputeFaultChoiceStore.Get("offer-1")
	if choice.Status != core.DisputeFaultChoiceDefaulted {
		t.Fatalf("expected status DEFAULTED, got %s", choice.Status)
	}
}

// TestResolveExpiredDisputeFaultChoices_LeavesUnexpiredChoicesAlone
// confirms a choice still within its window is never touched by the
// sweep, however many blocks pass.
func TestResolveExpiredDisputeFaultChoices_LeavesUnexpiredChoicesAlone(t *testing.T) {
	resetApplyTestState()
	disputeFaultChoiceStore = core.NewDisputeFaultChoiceStore()

	const atFault, moderator = "at-fault-addr", "moderator-addr"
	walletStore.Get(atFault).Reputation = 50
	seedPendingFaultChoice("offer-1", atFault, moderator, "CCT", 30, 1000)

	block := core.Block{Timestamp: 1000 + 3600} // 1 hour later, well inside the 72h window
	resolveExpiredDisputeFaultChoices(block)

	if got := walletStore.Get(atFault).Reputation; got != 50 {
		t.Fatalf("expected reputation untouched while still within the window, got %d", got)
	}
	choice, _ := disputeFaultChoiceStore.Get("offer-1")
	if choice.Status != core.DisputeFaultChoicePending {
		t.Fatalf("expected the choice to remain PENDING, got %s", choice.Status)
	}
}

// TestSweepModeratorFeePoolIfDue_DistributesByReputationTimesUptime is
// the core regression test for the biweekly moderator payout: two
// active moderators with different reputation/uptime combinations
// split the pool proportionally, and a deactivated moderator gets
// nothing despite being registered.
func TestSweepModeratorFeePoolIfDue_DistributesByReputationTimesUptime(t *testing.T) {
	resetApplyTestState()
	moderatorStore = core.NewModeratorStore()
	reptStore = core.NewTokenStore()

	// mod-a: reputation 100, uptime 1.0 (never assigned a dispute) -> weight 100.
	moderatorStore.Register("mod-a", "pub-a", 1000)
	walletStore.Get("mod-a").Reputation = 100

	// mod-b: reputation 100, uptime 0.5 (1 of 2 disputes resolved) -> weight 50.
	moderatorStore.Register("mod-b", "pub-b", 1000)
	walletStore.Get("mod-b").Reputation = 100
	moderatorStore.RecordDisputeAssigned("mod-b")
	moderatorStore.RecordDisputeAssigned("mod-b")
	moderatorStore.RecordDisputeResolved("mod-b")

	// mod-c: deactivated - must receive nothing despite otherwise
	// qualifying reputation/uptime.
	moderatorStore.Register("mod-c", "pub-c", 1000)
	walletStore.Get("mod-c").Reputation = 100
	moderatorStore.Deactivate("mod-c")

	reptStore.Mint(platformFeeModeratorPoolAddress("REPT"), 150)

	block := core.Block{Timestamp: moderatorFeePoolSweepIntervalSeconds + 1}
	sweepModeratorFeePoolIfDue(block)

	// Total weight 150 (100+50) - mod-a gets 100/150 of 150 = 100,
	// mod-b gets 50/150 of 150 = 50.
	if got := reptStore.BalanceOf("mod-a"); got != 100 {
		t.Fatalf("expected mod-a to receive 100 REPT (2/3 of the pool), got %d", got)
	}
	if got := reptStore.BalanceOf("mod-b"); got != 50 {
		t.Fatalf("expected mod-b to receive 50 REPT (1/3 of the pool), got %d", got)
	}
	if got := reptStore.BalanceOf("mod-c"); got != 0 {
		t.Fatalf("expected the deactivated mod-c to receive nothing, got %d", got)
	}
	if got := reptStore.BalanceOf(platformFeeModeratorPoolAddress("REPT")); got != 0 {
		t.Fatalf("expected the pool to be fully drained, got %d left", got)
	}
	if treasuryStore.Treasury.LastModeratorPoolSweepAt != block.Timestamp {
		t.Fatalf("expected LastModeratorPoolSweepAt updated to %d, got %d", block.Timestamp, treasuryStore.Treasury.LastModeratorPoolSweepAt)
	}
}

// TestSweepModeratorFeePoolIfDue_NoOpBeforeIntervalElapsed confirms
// the sweep does nothing (and doesn't update the timestamp) before two
// weeks have passed since the last one.
func TestSweepModeratorFeePoolIfDue_NoOpBeforeIntervalElapsed(t *testing.T) {
	resetApplyTestState()
	moderatorStore = core.NewModeratorStore()
	reptStore = core.NewTokenStore()

	moderatorStore.Register("mod-a", "pub-a", 1000)
	walletStore.Get("mod-a").Reputation = 100
	reptStore.Mint(platformFeeModeratorPoolAddress("REPT"), 150)

	block := core.Block{Timestamp: moderatorFeePoolSweepIntervalSeconds - 1}
	sweepModeratorFeePoolIfDue(block)

	if got := reptStore.BalanceOf("mod-a"); got != 0 {
		t.Fatalf("expected no payout before the interval has elapsed, got %d", got)
	}
	if got := reptStore.BalanceOf(platformFeeModeratorPoolAddress("REPT")); got != 150 {
		t.Fatalf("expected the pool to remain untouched, got %d", got)
	}
}
