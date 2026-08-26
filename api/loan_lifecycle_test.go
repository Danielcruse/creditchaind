package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// newLifecycleTestLoan builds a minimal FUNDED, CCT-collateralized
// loan for advanceLoanLifecycle tests - CCT collateral deliberately
// has no live price (collateralValueInYen always returns ok=false for
// it), which makes liquidateLoan's outcome trivially predictable (full
// shortfall) for tests that don't care about the shortfall math itself
// (that's covered separately by TestLiquidateLoan_* in apply_test.go).
func newLifecycleTestLoan(id, borrower, lender string) *core.Loan {
	return &core.Loan{
		ID:               id,
		Borrower:         borrower,
		Lender:           lender,
		Symbol:           "USDCTEST",
		Amount:           50,
		Status:           core.LoanFunded,
		CollateralSymbol: "CCT",
		CollateralAmount: 20,
	}
}

func TestAdvanceLoanLifecycle_HealthyToWarningOnFirstAtRisk(t *testing.T) {
	resetApplyTestState()
	loan := newLifecycleTestLoan("lc-1", "lc-1-borrower", "lc-1-lender")

	advanceLoanLifecycle(loan, true, 1000, "test")

	if loan.RiskStage != core.LoanRiskWarning {
		t.Fatalf("expected RiskStage WARNING, got %q", loan.RiskStage)
	}
	if loan.RiskStageEnteredAt != 1000 {
		t.Fatalf("expected RiskStageEnteredAt 1000, got %d", loan.RiskStageEnteredAt)
	}
	if loan.Status != core.LoanFunded {
		t.Fatalf("expected Status to stay FUNDED, got %s", loan.Status)
	}
}

func TestAdvanceLoanLifecycle_HealthyStaysHealthyWhenNotAtRisk(t *testing.T) {
	resetApplyTestState()
	loan := newLifecycleTestLoan("lc-2", "lc-2-borrower", "lc-2-lender")

	advanceLoanLifecycle(loan, false, 1000, "test")

	if loan.RiskStage != core.LoanRiskHealthy {
		t.Fatalf("expected RiskStage to stay HEALTHY, got %q", loan.RiskStage)
	}
}

func TestAdvanceLoanLifecycle_RecoversToHealthyWhenNoLongerAtRisk(t *testing.T) {
	resetApplyTestState()
	loan := newLifecycleTestLoan("lc-3", "lc-3-borrower", "lc-3-lender")

	advanceLoanLifecycle(loan, true, 1000, "test") // -> WARNING
	if loan.RiskStage != core.LoanRiskWarning {
		t.Fatalf("setup: expected WARNING, got %q", loan.RiskStage)
	}

	advanceLoanLifecycle(loan, false, 1500, "test") // recovers

	if loan.RiskStage != core.LoanRiskHealthy {
		t.Fatalf("expected RiskStage to fall back to HEALTHY once no longer at risk, got %q", loan.RiskStage)
	}
	if loan.RiskStageEnteredAt != 0 {
		t.Fatalf("expected RiskStageEnteredAt reset to 0, got %d", loan.RiskStageEnteredAt)
	}
}

func TestAdvanceLoanLifecycle_EscalatesThroughFullLadderOnSustainedRisk(t *testing.T) {
	resetApplyTestState()
	loan := newLifecycleTestLoan("lc-4", "lc-4-borrower", "lc-4-lender")

	asOf := int64(1000)
	advanceLoanLifecycle(loan, true, asOf, "test")
	if loan.RiskStage != core.LoanRiskWarning {
		t.Fatalf("step 1: expected WARNING, got %q", loan.RiskStage)
	}

	asOf += core.LoanRiskWarningDwellSeconds + 1
	advanceLoanLifecycle(loan, true, asOf, "test")
	if loan.RiskStage != core.LoanRiskMarginCall {
		t.Fatalf("step 2: expected MARGIN_CALL, got %q", loan.RiskStage)
	}

	asOf += core.LoanRiskMarginCallDwellSeconds + 1
	advanceLoanLifecycle(loan, true, asOf, "test")
	if loan.RiskStage != core.LoanRiskGracePeriod {
		t.Fatalf("step 3: expected GRACE_PERIOD, got %q", loan.RiskStage)
	}
	wantDeadline := asOf + core.LoanRiskGracePeriodSeconds
	if loan.RiskStageDeadline != wantDeadline {
		t.Fatalf("expected RiskStageDeadline %d, got %d", wantDeadline, loan.RiskStageDeadline)
	}
}

func TestAdvanceLoanLifecycle_LiquidatesOnGracePeriodExpiry(t *testing.T) {
	resetApplyTestState()
	loan := newLifecycleTestLoan("lc-5", "lc-5-borrower", "lc-5-lender")
	loan.RiskStage = core.LoanRiskGracePeriod
	loan.RiskStageEnteredAt = 1000
	loan.RiskStageDeadline = 1000 + core.LoanRiskGracePeriodSeconds

	// Before the deadline: nothing happens yet.
	advanceLoanLifecycle(loan, true, loan.RiskStageDeadline-1, "test")
	if loan.RiskStage != core.LoanRiskGracePeriod {
		t.Fatalf("expected loan to stay in GRACE_PERIOD before the deadline, got %q", loan.RiskStage)
	}
	if loan.CollateralAmount != 20 {
		t.Fatalf("expected collateral untouched before the deadline, got %d", loan.CollateralAmount)
	}

	// At/after the deadline: liquidateLoan actually fires.
	asOf := loan.RiskStageDeadline + 1
	advanceLoanLifecycle(loan, true, asOf, "test")

	if loan.RiskStage != core.LoanRiskAwaitingFinalRedemption {
		t.Fatalf("expected RiskStage AWAITING_FINAL_REDEMPTION after grace period expiry, got %q", loan.RiskStage)
	}
	if loan.CollateralAmount != 0 {
		t.Fatalf("expected collateral seized (CollateralAmount 0), got %d", loan.CollateralAmount)
	}
	if got := stakingStore.Stakes["lc-5-lender"]["CCT"]; got != 20 {
		t.Fatalf("expected lender to receive the seized 20 CCT, got %d", got)
	}
	if loan.LiquidatedShortfall != 50 {
		t.Fatalf("expected full 50 shortfall (CCT has no live price), got %d", loan.LiquidatedShortfall)
	}
	wantRedemptionDeadline := asOf + core.LoanRiskFinalRedemptionWindowSeconds
	if loan.RiskStageDeadline != wantRedemptionDeadline {
		t.Fatalf("expected final redemption deadline %d, got %d", wantRedemptionDeadline, loan.RiskStageDeadline)
	}
}

// TestAdvanceLoanLifecycle_IdempotentWhenCalledTwiceSameBlock covers
// the dual-sweep race: a loan that's simultaneously overdue AND
// under-collateralized gets visited by both processLoanDefaults and
// processCollateralLiquidations in the same block - advanceLoanLifecycle
// must not double-advance it.
func TestAdvanceLoanLifecycle_IdempotentWhenCalledTwiceSameBlock(t *testing.T) {
	resetApplyTestState()
	loan := newLifecycleTestLoan("lc-6", "lc-6-borrower", "lc-6-lender")

	advanceLoanLifecycle(loan, true, 1000, "overdue")
	advanceLoanLifecycle(loan, true, 1000, "price-triggered") // same block, second sweep

	if loan.RiskStage != core.LoanRiskWarning {
		t.Fatalf("expected a single WARNING transition, got %q", loan.RiskStage)
	}
	if loan.RiskStageEnteredAt != 1000 {
		t.Fatalf("expected RiskStageEnteredAt to stay 1000 (not re-stamped), got %d", loan.RiskStageEnteredAt)
	}

	// Now push it to GracePeriod and confirm calling it twice at the
	// exact liquidation instant doesn't liquidate twice (second call
	// finds RiskStage already AWAITING_FINAL_REDEMPTION, a terminal
	// no-op case).
	loan.RiskStage = core.LoanRiskGracePeriod
	loan.RiskStageEnteredAt = 1000
	loan.RiskStageDeadline = 1000 + core.LoanRiskGracePeriodSeconds
	asOf := loan.RiskStageDeadline + 1

	advanceLoanLifecycle(loan, true, asOf, "overdue")
	advanceLoanLifecycle(loan, true, asOf, "price-triggered")

	if got := stakingStore.Stakes["lc-6-lender"]["CCT"]; got != 20 {
		t.Fatalf("expected collateral seized exactly once (20 CCT), got %d", got)
	}
}
