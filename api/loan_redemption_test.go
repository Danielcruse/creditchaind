package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// newRedemptionTestLoan builds a loan already sitting in
// AWAITING_FINAL_REDEMPTION with a real borrower/lender keypair,
// matching the state advanceLoanLifecycle leaves behind once a grace
// period expires with a shortfall.
func newRedemptionTestLoan(t *testing.T) (loan *core.Loan, borrower, borrowerPubHex, lender string) {
	t.Helper()

	borrowerPub, _, _ := core.GenerateKeyPair()
	borrowerPubHex = hex.EncodeToString(borrowerPub)
	borrower = core.GenerateAddress(borrowerPub)
	lender = "redemption-test-lender"

	loan = &core.Loan{
		ID:                  "redeem-1",
		Borrower:            borrower,
		Lender:              lender,
		Symbol:              "YENE",
		Amount:              100,
		Status:              core.LoanDefault, // liquidateLoan already ran, collateral gone
		RiskStage:           core.LoanRiskAwaitingFinalRedemption,
		RiskStageEnteredAt:  1000,
		RiskStageDeadline:   1000 + core.LoanRiskFinalRedemptionWindowSeconds,
		LiquidatedShortfall: 40,
		LiquidatedOwed:      100,
	}
	loanStore.Loans[loan.ID] = loan
	return loan, borrower, borrowerPubHex, lender
}

func buildRedeemShortfallTx(loanID, borrower, borrowerPubHex string) core.Transaction {
	data, _ := json.Marshal(loanRedeemShortfallData{LoanID: loanID})
	return core.Transaction{
		ID:            "tx-redeem-" + loanID,
		From:          borrower,
		AuthAddress:   borrower,
		AuthPublicKey: borrowerPubHex,
		Type:          core.TxLoanRedeemShortfall,
		Data:          string(data),
		Timestamp:     2000,
	}
}

func TestApplyLoanRedeemShortfall_ClearsRiskStageWithoutReputationChange(t *testing.T) {
	resetApplyTestState()
	creditEngine.Wallets = walletStore

	loan, borrower, borrowerPubHex, lender := newRedemptionTestLoan(t)
	walletStore.Get(borrower).Reputation = 60

	borrowerSide := resolveBalanceAddress(borrower, borrowerPubHex, "YENE")
	yenStore.Mint(borrowerSide, 40)

	tx := buildRedeemShortfallTx(loan.ID, borrower, borrowerPubHex)
	applyLoanRedeemShortfall(tx)

	if loan.RiskStage != core.LoanRiskHealthy {
		t.Fatalf("expected RiskStage cleared to HEALTHY, got %q", loan.RiskStage)
	}
	if loan.Status != core.LoanRepaid {
		t.Fatalf("expected Status REPAID, got %s", loan.Status)
	}
	if got := walletStore.Get(borrower).Reputation; got != 60 {
		t.Fatalf("expected Mining Power (reputation) unchanged at 60, got %d", got)
	}
	lenderSide := resolveBalanceAddress(lender, "", "YENE")
	if got := yenStore.BalanceOf(lenderSide); got != 40 {
		t.Fatalf("expected lender to receive the 40 YENE shortfall, got %d", got)
	}
	if got := yenStore.BalanceOf(borrowerSide); got != 0 {
		t.Fatalf("expected borrower's YENE balance drained to 0, got %d", got)
	}
}

func TestApplyLoanRedeemShortfall_FailsIfInsufficientBalance(t *testing.T) {
	resetApplyTestState()
	creditEngine.Wallets = walletStore

	loan, borrower, borrowerPubHex, _ := newRedemptionTestLoan(t)
	// Deliberately fund less than the 40 owed.
	borrowerSide := resolveBalanceAddress(borrower, borrowerPubHex, "YENE")
	yenStore.Mint(borrowerSide, 10)

	tx := buildRedeemShortfallTx(loan.ID, borrower, borrowerPubHex)
	applyLoanRedeemShortfall(tx)

	if loan.RiskStage != core.LoanRiskAwaitingFinalRedemption {
		t.Fatalf("expected RiskStage to stay AWAITING_FINAL_REDEMPTION on failed transfer, got %q", loan.RiskStage)
	}
	if got := yenStore.BalanceOf(borrowerSide); got != 10 {
		t.Fatalf("expected borrower's balance untouched on failed transfer, got %d", got)
	}
}

func TestApplyLoanRedeemShortfall_FailsIfNotAwaitingFinalRedemption(t *testing.T) {
	resetApplyTestState()
	creditEngine.Wallets = walletStore

	loan, borrower, borrowerPubHex, _ := newRedemptionTestLoan(t)
	loan.RiskStage = core.LoanRiskGracePeriod // not yet liquidated

	borrowerSide := resolveBalanceAddress(borrower, borrowerPubHex, "YENE")
	yenStore.Mint(borrowerSide, 40)

	tx := buildRedeemShortfallTx(loan.ID, borrower, borrowerPubHex)
	applyLoanRedeemShortfall(tx)

	if loan.RiskStage != core.LoanRiskGracePeriod {
		t.Fatalf("expected RiskStage untouched outside AWAITING_FINAL_REDEMPTION, got %q", loan.RiskStage)
	}
	if got := yenStore.BalanceOf(borrowerSide); got != 40 {
		t.Fatalf("expected no transfer to occur, borrower balance should stay 40, got %d", got)
	}
}
