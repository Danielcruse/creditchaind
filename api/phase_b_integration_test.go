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

// buildSignedEnvelopeJSON signs payload exactly the way
// website/js/api.js's apiPostSigned does, so this drives the REAL HTTP
// handler end-to-end (route -> requireSignedRequest -> recordTx),
// rather than calling an apply* function directly like the other
// Phase B tests do.
func buildSignedEnvelopeJSON(t *testing.T, pub, priv []byte, payload any) []byte {
	t.Helper()

	from := core.GenerateAddress(pub)
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	env := core.SignedEnvelope{
		Address:   from,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: time.Now().Unix(),
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(priv, env.CanonicalMessage())
	env.Signature = hex.EncodeToString(sig)

	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return body
}

// TestPhaseB_EndToEnd_HTTPAndConsensusIntegration drives the real
// Phase B HTTP handlers (HandleLoanAddCollateral, HandleLoanHealth)
// and the real consensus-loop-invoked sweep (ApplyBlock ->
// processCollateralLiquidations) against a peer-funded, OETH-
// collateralized loan - the same round trip a browser click-through
// would exercise, minus the browser itself.
//
// A literal browser pass would additionally need to first clear the
// peer-funded marketplace's pre-existing, unrelated lender gate
// (core.MinReputationForLending=40 plus KYC submission, see
// api/kyc.go) - that gate has no fast legitimate bootstrap path from a
// fresh wallet (it's normally earned over many real trades) and isn't
// part of what Phase B added, so this test constructs the FUNDED loan
// directly - the same state that gate would eventually allow through -
// and focuses verification on the new liquidation/collateral-topup
// machinery itself, exercised via its real HTTP + consensus path.
func TestPhaseB_EndToEnd_HTTPAndConsensusIntegration(t *testing.T) {
	resetApplyTestState()
	// resetApplyTestState reassigns the package-level walletStore var
	// to a fresh store without updating creditEngine.Wallets, leaving
	// it stale (same gotcha TestProcessLoanDefaults_... documents) -
	// processCollateralLiquidations calls creditEngine.DefaultLoan,
	// which reads through ce.Wallets, so this must be re-synced or the
	// reputation deltas below would silently apply to a wallet store
	// this test can never observe.
	creditEngine.Wallets = walletStore
	mempool.Clear()
	appliedBlock.mu.Lock()
	appliedBlock.seen = make(map[string]bool)
	appliedBlock.mu.Unlock()

	borrowerPub, borrowerPriv, _ := core.GenerateKeyPair()
	borrower := core.GenerateAddress(borrowerPub)
	const lender = "phaseb-e2e-lender"

	walletStore.Get(borrower).Reputation = 50
	walletStore.Get(lender).Reputation = 50

	// Healthy starting reserves.
	cctEthTestPool.CCTReserve = 2_000_000
	cctEthTestPool.TokenReserve = 1_000_000
	cctUsdcTestPool.CCTReserve = 1_000_000
	cctUsdcTestPool.TokenReserve = 1_000_000

	loan := &core.Loan{
		ID:               "phaseb-e2e-loan",
		Borrower:         borrower,
		Lender:           lender,
		Symbol:           "YENE",
		Amount:           1000,
		Status:           core.LoanFunded,
		CollateralSymbol: "OETH",
		CollateralAmount: 800,
	}
	loanStore.Loans[loan.ID] = loan

	// --- 1. GET /loans/health while healthy ---
	healthReq := httptest.NewRequest(http.MethodGet, "/loans/health?loanId="+loan.ID, nil)
	healthRec := httptest.NewRecorder()
	HandleLoanHealth(healthRec, healthReq)

	var health loanHealthResponse
	if err := json.Unmarshal(healthRec.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health response: %v (body=%s)", err, healthRec.Body.String())
	}
	if !health.PriceAvailable {
		t.Fatalf("expected priceAvailable true with seeded reserves, got %+v", health)
	}
	if health.AtRisk {
		t.Fatalf("expected atRisk false while healthy, got %+v", health)
	}

	// --- 2. POST /loans/add-collateral via a real signed HTTP request ---
	collateralStore.Add(borrower, "OETH", 200) // already-locked, not yet earmarked to any loan

	addBody := buildSignedEnvelopeJSON(t, borrowerPub, borrowerPriv, loanAddCollateralData{
		LoanID: loan.ID,
		Amount: 200,
	})
	addReq := httptest.NewRequest(http.MethodPost, "/loans/add-collateral", bytes.NewReader(addBody))
	addRec := httptest.NewRecorder()
	HandleLoanAddCollateral(addRec, addReq)

	if addRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from HandleLoanAddCollateral, got %d: %s", addRec.Code, addRec.Body.String())
	}
	var addResp struct {
		Status string `json:"status"`
		TxID   string `json:"txId"`
	}
	if err := json.Unmarshal(addRec.Body.Bytes(), &addResp); err != nil {
		t.Fatalf("decode add-collateral response: %v (body=%s)", err, addRec.Body.String())
	}
	if addResp.TxID == "" {
		t.Fatalf("expected a txId in the add-collateral response, got %+v", addResp)
	}

	// Sweep the just-recorded transaction into a real block via
	// ApplyBlock - the actual mechanism a validator uses, not a direct
	// call to applyLoanAddCollateral.
	if len(mempool.Transactions) != 1 {
		t.Fatalf("expected exactly 1 transaction in the mempool, got %d", len(mempool.Transactions))
	}
	block1 := core.Block{
		Index:        1,
		Hash:         "phaseb-e2e-block-1",
		Timestamp:    time.Now().Unix(),
		Transactions: mempool.Transactions,
	}
	ApplyBlock(block1)
	mempool.Clear()

	if got := loan.CollateralAmount; got != 1000 {
		t.Fatalf("expected loan.CollateralAmount 1000 (800+200) after add-collateral confirms, got %d", got)
	}
	if got := collateralStore.LockedOf(borrower, "OETH"); got != 0 {
		t.Fatalf("expected the borrower's general locked-collateral pool fully drawn down (0 left), got %d", got)
	}

	// --- 3. Crash the OETH price and confirm /loans/health flags it ---
	cctEthTestPool.CCTReserve = 600_000 // was 2,000,000 - OETH now worth ~3.3x less in CCT

	collateralValue, ok := collateralValueInYen(loan.CollateralSymbol, loan.CollateralAmount)
	if !ok {
		t.Fatalf("expected a live (crashed) price to still be available")
	}
	owed := repayAmountFor(loan)
	if collateralValue*100 >= core.LiquidationThresholdPercent*owed {
		t.Fatalf("test setup invalid: expected the crashed price to drop collateral below the liquidation threshold (collateralValue=%d, owed=%d)", collateralValue, owed)
	}

	health2Req := httptest.NewRequest(http.MethodGet, "/loans/health?loanId="+loan.ID, nil)
	health2Rec := httptest.NewRecorder()
	HandleLoanHealth(health2Rec, health2Req)

	var health2 loanHealthResponse
	if err := json.Unmarshal(health2Rec.Body.Bytes(), &health2); err != nil {
		t.Fatalf("decode health2 response: %v", err)
	}
	if !health2.AtRisk {
		t.Fatalf("expected atRisk true after crashing the OETH price, got %+v", health2)
	}

	// --- 4. Confirm the consensus sweep walks the loan through the full
	// Default Protection ladder (Warning -> MarginCall -> GracePeriod ->
	// liquidate -> AwaitingFinalRedemption -> processFinalRedemptions) -
	// a single block no longer instantly liquidates/defaults, unlike
	// before this feature; each stage needs its own dwell/deadline to
	// actually pass (see core/loan_risk.go, advanceLoanLifecycle in
	// api/apply.go). Blocks from here on use explicit, escalating
	// Timestamps (not time.Now()) so the test can simulate days of
	// elapsed time without actually waiting.
	wantShortfall := uint64(0)
	if collateralValue < owed {
		wantShortfall = owed - collateralValue
	}
	wantDelta := core.ReputationDeltaLoanDefault * wantShortfall / owed
	if wantDelta > core.ReputationDeltaLoanDefault {
		wantDelta = core.ReputationDeltaLoanDefault
	}
	if wantShortfall > 0 && wantDelta < core.ReputationDeltaLoanDefaultMinimum {
		wantDelta = core.ReputationDeltaLoanDefaultMinimum
	}

	asOf := time.Now().Unix()
	ApplyBlock(core.Block{Index: 2, Hash: "phaseb-e2e-block-2", Timestamp: asOf})
	if loan.RiskStage != core.LoanRiskWarning {
		t.Fatalf("expected RiskStage WARNING on the first at-risk sweep, got %q", loan.RiskStage)
	}

	asOf += core.LoanRiskWarningDwellSeconds + 1
	ApplyBlock(core.Block{Index: 3, Hash: "phaseb-e2e-block-3", Timestamp: asOf})
	if loan.RiskStage != core.LoanRiskMarginCall {
		t.Fatalf("expected RiskStage MARGIN_CALL after the warning dwell, got %q", loan.RiskStage)
	}

	asOf += core.LoanRiskMarginCallDwellSeconds + 1
	ApplyBlock(core.Block{Index: 4, Hash: "phaseb-e2e-block-4", Timestamp: asOf})
	if loan.RiskStage != core.LoanRiskGracePeriod {
		t.Fatalf("expected RiskStage GRACE_PERIOD after the margin call dwell, got %q", loan.RiskStage)
	}

	asOf = loan.RiskStageDeadline + 1
	ApplyBlock(core.Block{Index: 5, Hash: "phaseb-e2e-block-5", Timestamp: asOf})

	if wantShortfall == 0 {
		if loan.Status != core.LoanRepaid {
			t.Fatalf("expected loan status REPAID (collateral still covered debt), got %s - check the test's price-crash math", loan.Status)
		}
		if loan.CollateralAmount != 0 {
			t.Fatalf("expected all collateral disposed of by the liquidation sweep, got %d left", loan.CollateralAmount)
		}
		return
	}

	if loan.RiskStage != core.LoanRiskAwaitingFinalRedemption {
		t.Fatalf("expected RiskStage AWAITING_FINAL_REDEMPTION after grace period expiry with a shortfall, got %q", loan.RiskStage)
	}
	if loan.CollateralAmount != 0 {
		t.Fatalf("expected all collateral disposed of by the liquidation sweep, got %d left", loan.CollateralAmount)
	}
	// Reputation/Mining Power hasn't moved yet - the Default Protection
	// philosophy is to never punish immediately, only after the Final
	// Redemption window (below) also expires.
	if got := walletStore.Get(borrower).Reputation; got != 50 {
		t.Fatalf("expected borrower reputation still 50 before the final redemption window expires, got %d", got)
	}

	asOf = loan.RiskStageDeadline + 1
	ApplyBlock(core.Block{Index: 6, Hash: "phaseb-e2e-block-6", Timestamp: asOf})

	if loan.Status != core.LoanDefault {
		t.Fatalf("expected loan status DEFAULT after the final redemption window expired, got %s", loan.Status)
	}
	if loan.RiskStage != core.LoanRiskDefaulted {
		t.Fatalf("expected RiskStage DEFAULTED, got %q", loan.RiskStage)
	}
	if got := walletStore.Get(borrower).Reputation; got != 50-wantDelta {
		t.Fatalf("expected borrower reputation decreased by %d (from 50), got %d", wantDelta, got)
	}
	if got := walletStore.Get(lender).Reputation; got != 50+wantDelta {
		t.Fatalf("expected lender reputation increased by %d (from 50), got %d", wantDelta, got)
	}
}
