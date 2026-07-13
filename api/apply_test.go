package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// resetApplyTestState gives each test a clean slate for every
// package-level store applyTxOnce/ApplyTransaction touch for a plain
// CCT transfer - these are real singletons shared across the package,
// so tests that mutate them must not leak state into each other.
func resetApplyTestState() {
	walletStore = core.NewWalletStore()
	tokenStore = core.NewTokenStore()
	appliedTx.mu.Lock()
	appliedTx.seen = make(map[string]bool)
	appliedTx.mu.Unlock()
}

// buildSignedTransferTx builds a genuinely-signed TxExchange
// transaction (a plain CCT transfer) exactly the way a real client
// would - same envelope/signature shape as core.SignedEnvelope, packed
// into the transaction's Auth* fields the way recordTx does.
func buildSignedTransferTx(t *testing.T, to string, amount uint64, ts int64) (core.Transaction, string) {
	t.Helper()

	pub, priv, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(pub)

	payload := tokenTransferData{To: to, Amount: amount}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	env := core.SignedEnvelope{
		Address:   from,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: ts,
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(priv, env.CanonicalMessage())

	tx := core.Transaction{
		ID:            "test-tx-" + from + "-" + to,
		From:          from,
		To:            to,
		Amount:        amount,
		Type:          core.TxExchange,
		Timestamp:     ts,
		AuthAddress:   from,
		AuthPublicKey: hex.EncodeToString(pub),
		AuthTimestamp: ts,
		AuthPayload:   payloadJSON,
		AuthSignature: hex.EncodeToString(sig),
	}
	return tx, from
}

func TestApplyTxOnce_ValidTransferMovesBalance(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	tx, from := buildSignedTransferTx(t, to, 100, 1000)
	// CCT sends also carry CCTTransferFee on top of the amount (see
	// api/apply.go's applyTransfer) - minted here alongside the
	// transfer amount so the sender can cover both.
	tokenStore.Mint(from, 100+CCTTransferFee)

	applyTxOnce(tx)

	if got := tokenStore.BalanceOf(from); got != 0 {
		t.Fatalf("expected sender balance 0 after transfer (amount + fee both spent), got %d", got)
	}
	if got := tokenStore.BalanceOf(to); got != 100 {
		t.Fatalf("expected recipient balance 100, got %d", got)
	}
}

func TestApplyTxOnce_DuplicateIDAppliedOnce(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	tx, from := buildSignedTransferTx(t, to, 100, 1000)
	tokenStore.Mint(from, 200)

	applyTxOnce(tx)
	applyTxOnce(tx) // same tx.ID, replayed - must be a no-op the second time

	if got := tokenStore.BalanceOf(to); got != 100 {
		t.Fatalf("expected recipient balance 100 (transfer applied exactly once), got %d", got)
	}
	// 200 - 100 (amount) - CCTTransferFee (charged once, not twice by
	// the replay).
	if got := tokenStore.BalanceOf(from); got != 200-100-CCTTransferFee {
		t.Fatalf("expected sender to have %d left (amount + fee debited exactly once), got %d", 200-100-CCTTransferFee, got)
	}
}

func TestApplyTxOnce_TamperedSignatureRejected(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	tx, from := buildSignedTransferTx(t, to, 100, 1000)
	tokenStore.Mint(from, 100)

	// Flip the signature to something that won't verify - simulates a
	// corrupted or forged transaction reaching a node.
	tx.AuthSignature = "00" + tx.AuthSignature[2:]

	applyTxOnce(tx)

	if got := tokenStore.BalanceOf(from); got != 100 {
		t.Fatalf("expected balance untouched after a bad signature, got %d (sender should still have 100)", got)
	}
	if got := tokenStore.BalanceOf(to); got != 0 {
		t.Fatalf("expected recipient to receive nothing from a rejected transaction, got %d", got)
	}
}

func TestApplyTxOnce_AmountMismatchBetweenSignedAndClaimedRejected(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	// Signed payload says 100, but the transaction itself (what a
	// malicious block producer could rewrite after the fact) claims
	// 999 - verifyTxAuth's TxExchange case exists specifically to catch
	// this: a validly-signed envelope whose Transaction.Amount was
	// altered after signing.
	tx, from := buildSignedTransferTx(t, to, 100, 1000)
	tx.Amount = 999
	tokenStore.Mint(from, 999)

	applyTxOnce(tx)

	if got := tokenStore.BalanceOf(to); got != 0 {
		t.Fatalf("expected the amount-mismatched transaction to be rejected entirely, recipient got %d", got)
	}
	if got := tokenStore.BalanceOf(from); got != 999 {
		t.Fatalf("expected sender's balance untouched, got %d", got)
	}
}

func TestApplyTxOnce_RecipientMismatchBetweenSignedAndClaimedRejected(t *testing.T) {
	resetApplyTestState()

	// Signed payload authorizes paying "recipient-address", but the
	// transaction's own To field was redirected to "attacker-address" -
	// must be rejected, not silently redirected.
	tx, from := buildSignedTransferTx(t, "recipient-address", 100, 1000)
	tx.To = "attacker-address"
	tokenStore.Mint(from, 100)

	applyTxOnce(tx)

	if got := tokenStore.BalanceOf("attacker-address"); got != 0 {
		t.Fatalf("expected a redirected recipient to receive nothing, got %d", got)
	}
	if got := tokenStore.BalanceOf(from); got != 100 {
		t.Fatalf("expected sender's balance untouched, got %d", got)
	}
}

func TestApplyBlock_DuplicateBlockAppliedOnce(t *testing.T) {
	resetApplyTestState()
	appliedBlock.mu.Lock()
	appliedBlock.seen = make(map[string]bool)
	appliedBlock.mu.Unlock()

	const to = "recipient-address"
	tx, from := buildSignedTransferTx(t, to, 50, 2000)
	tokenStore.Mint(from, 50+CCTTransferFee)

	block := core.Block{
		Index:        1,
		Hash:         "test-block-hash",
		Timestamp:    2000,
		Transactions: []core.Transaction{tx},
	}

	ApplyBlock(block)
	ApplyBlock(block) // same Hash, replayed - must be a no-op the second time

	if got := tokenStore.BalanceOf(to); got != 50 {
		t.Fatalf("expected recipient balance 50 (block applied exactly once), got %d", got)
	}
}

// TestProcessLoanDefaults_OverdueLoanForfeitsCollateralAndReputationToLender
// verifies the peer-funded loan marketplace's core economics: a
// borrower who misses their due date loses BOTH their collateral and
// reputation to the lender, who is left strictly better off - never
// just made whole, per core.ReputationDeltaLoanDefaultLenderGain's
// doc comment.
func TestProcessLoanDefaults_OverdueLoanForfeitsCollateralAndReputationToLender(t *testing.T) {
	const borrower = "loan-default-test-borrower"
	const lender = "loan-default-test-lender"

	// creditEngine was wired to whatever walletStore was at package-
	// init time; resetApplyTestState (used by earlier tests in this
	// file) reassigns the package-level walletStore var to a fresh
	// store without updating creditEngine.Wallets, leaving it stale.
	// processLoanDefaults calls creditEngine.DefaultLoan, which reads
	// through ce.Wallets - re-sync here so this test observes the same
	// wallets it just set up, regardless of what ran before it.
	creditEngine.Wallets = walletStore

	walletStore.Get(borrower).Reputation = 50
	walletStore.Get(lender).Reputation = 50

	loan := &core.Loan{
		ID:               "test-default-loan",
		Borrower:         borrower,
		Lender:           lender,
		Symbol:           "USDCTEST",
		Amount:           50,
		Status:           core.LoanFunded,
		CollateralSymbol: "CCT",
		CollateralAmount: 20,
		DueAt:            1000,
	}
	loanStore.Loans[loan.ID] = loan

	processLoanDefaults(2000) // well past DueAt

	if loan.Status != core.LoanDefault {
		t.Fatalf("expected loan status DEFAULT, got %s", loan.Status)
	}
	if got := stakingStore.Stakes[lender]["CCT"]; got != 20 {
		t.Fatalf("expected lender to receive the borrower's 20 staked CCT collateral, got %d", got)
	}
	if got := walletStore.Get(borrower).Reputation; got != 50-uint64(core.ReputationDeltaLoanDefault) {
		t.Fatalf("expected borrower reputation decreased by %d (from 50), got %d", core.ReputationDeltaLoanDefault, got)
	}
	if got := walletStore.Get(lender).Reputation; got != 50+uint64(core.ReputationDeltaLoanDefaultLenderGain) {
		t.Fatalf("expected lender reputation increased by %d (from 50), got %d", core.ReputationDeltaLoanDefaultLenderGain, got)
	}
}

// TestProcessLoanDefaults_NotYetDueLoanUntouched confirms the sweep
// only ever touches loans whose due date has actually passed.
func TestProcessLoanDefaults_NotYetDueLoanUntouched(t *testing.T) {
	loan := &core.Loan{
		ID:       "test-not-due-loan",
		Borrower: "loan-not-due-test-borrower",
		Lender:   "loan-not-due-test-lender",
		Status:   core.LoanFunded,
		DueAt:    5000,
	}
	loanStore.Loans[loan.ID] = loan

	processLoanDefaults(2000) // before DueAt

	if loan.Status != core.LoanFunded {
		t.Fatalf("expected loan to remain FUNDED (not yet due), got %s", loan.Status)
	}
}

// TestProcessLoanDefaults_SelfFundedLoanNeverDefaults confirms a
// self-funded loan (Lender == "", no real lender to protect) is
// exempt from the sweep even if its DueAt has technically passed -
// see processLoanDefaults's doc comment.
func TestProcessLoanDefaults_SelfFundedLoanNeverDefaults(t *testing.T) {
	loan := &core.Loan{
		ID:       "test-self-funded-loan",
		Borrower: "loan-self-funded-test-borrower",
		Lender:   "",
		Status:   core.LoanFunded,
		DueAt:    1000,
	}
	loanStore.Loans[loan.ID] = loan

	processLoanDefaults(2000)

	if loan.Status != core.LoanFunded {
		t.Fatalf("expected self-funded loan to never default, got status %s", loan.Status)
	}
}

// buildDisputedP2POffer constructs an EXTERNAL-mode offer already
// sitting in SettlementDisputed (same technique
// TestP2POffer_OnlyAssignedModeratorCanResolve in
// core/p2p_offer_test.go uses - set the struct fields directly rather
// than replaying the whole mark-paid/dispute sequence, since only the
// moderator-resolve step itself is under test here), with escrow
// funded so releaseEscrowToBuyer/refundEscrowToSeller have something
// real to move.
func buildDisputedP2POffer(t *testing.T, id, seller, buyer, moderator string, amount uint64) *core.P2POffer {
	t.Helper()

	offer := &core.P2POffer{
		ID:               id,
		Creator:          seller,
		OfferSymbol:      "CCT",
		OfferAmount:      amount,
		WantSymbol:       "TESTFIAT",
		WantAmount:       amount,
		PaymentMode:      core.PaymentModeExternal,
		Status:           core.OfferAccepted,
		AcceptedBy:       buyer,
		SettlementStatus: core.SettlementDisputed,
		ModeratorAddress: moderator,
	}
	p2pOfferStore.Offers[id] = offer
	tokenStore.Mint(p2pOfferEscrowAddress(id), amount)
	return offer
}

// TestApplyP2PModeratorResolve_ReleaseToBuyerPenalizesSeller confirms
// a moderator siding with the buyer (release=true - the seller wrongly
// disputed a real payment) both releases escrow to the buyer AND
// penalizes the seller's reputation, with a matching ReputationEvent
// recorded - see the new ReputationDeltaP2PFraud penalty added to
// applyP2PModeratorResolve.
func TestApplyP2PModeratorResolve_ReleaseToBuyerPenalizesSeller(t *testing.T) {
	resetApplyTestState()
	p2pOfferStore = core.NewP2POfferStore()
	reputationEventStore = core.NewReputationEventStore()

	const seller = "modresolve-test-seller"
	const buyer = "modresolve-test-buyer"
	const moderator = "modresolve-test-moderator"
	const amount = 25

	walletStore.Get(seller).Reputation = 50
	walletStore.Get(buyer).Reputation = 50

	buildDisputedP2POffer(t, "test-offer-release", seller, buyer, moderator, amount)

	tx := core.Transaction{
		ID:        "test-modresolve-release",
		From:      moderator,
		Type:      core.TxP2PModeratorResolve,
		Timestamp: 1000,
		Data:      `{"offerId":"test-offer-release","release":true}`,
	}
	applyP2PModeratorResolve(tx)

	if got := tokenStore.BalanceOf(buyer); got != amount {
		t.Fatalf("expected buyer to receive %d CCT escrow, got %d", amount, got)
	}
	if got := walletStore.Get(seller).Reputation; got != 50-uint64(ReputationDeltaP2PFraud) {
		t.Fatalf("expected seller reputation decreased by %d (from 50), got %d", ReputationDeltaP2PFraud, got)
	}
	if got := walletStore.Get(buyer).Reputation; got != 50 {
		t.Fatalf("expected buyer reputation untouched at 50, got %d", got)
	}

	event, ok := reputationEventStore.Get(tx.ID)
	if !ok || event.Target != seller || event.Delta != -int64(ReputationDeltaP2PFraud) {
		t.Fatalf("expected a recorded reputation event targeting seller with delta -%d, got %+v (ok=%v)", ReputationDeltaP2PFraud, event, ok)
	}
}

// TestApplyP2PModeratorResolve_RefundSellerPenalizesBuyer is the
// mirror case - release=false (the buyer's payment claim was false)
// refunds the seller and penalizes the buyer instead.
func TestApplyP2PModeratorResolve_RefundSellerPenalizesBuyer(t *testing.T) {
	resetApplyTestState()
	p2pOfferStore = core.NewP2POfferStore()
	reputationEventStore = core.NewReputationEventStore()

	const seller = "modresolve-test-seller-2"
	const buyer = "modresolve-test-buyer-2"
	const moderator = "modresolve-test-moderator-2"
	const amount = 25

	walletStore.Get(seller).Reputation = 50
	walletStore.Get(buyer).Reputation = 50

	buildDisputedP2POffer(t, "test-offer-refund", seller, buyer, moderator, amount)

	tx := core.Transaction{
		ID:        "test-modresolve-refund",
		From:      moderator,
		Type:      core.TxP2PModeratorResolve,
		Timestamp: 1000,
		Data:      `{"offerId":"test-offer-refund","release":false}`,
	}
	applyP2PModeratorResolve(tx)

	if got := tokenStore.BalanceOf(seller); got != amount {
		t.Fatalf("expected seller to be refunded %d CCT escrow, got %d", amount, got)
	}
	if got := walletStore.Get(buyer).Reputation; got != 50-uint64(ReputationDeltaP2PFraud) {
		t.Fatalf("expected buyer reputation decreased by %d (from 50), got %d", ReputationDeltaP2PFraud, got)
	}
	if got := walletStore.Get(seller).Reputation; got != 50 {
		t.Fatalf("expected seller reputation untouched at 50, got %d", got)
	}

	event, ok := reputationEventStore.Get(tx.ID)
	if !ok || event.Target != buyer || event.Delta != -int64(ReputationDeltaP2PFraud) {
		t.Fatalf("expected a recorded reputation event targeting buyer with delta -%d, got %+v (ok=%v)", ReputationDeltaP2PFraud, event, ok)
	}
}
