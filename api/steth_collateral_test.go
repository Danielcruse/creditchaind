package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// registerStethCollateralTestAddress registers a self-custodial ETH
// address for ccAddress (a prerequisite for ever holding a real
// STETH_CCC1 balance, see api/subaddress.go's resolveBalanceAddress)
// and mints amountWei of STETH_CCC1 directly into it - the same setup
// api/lido_stake_test.go uses, just factored out for reuse here.
func registerStethCollateralTestAddress(ccAddress, ethAddress string, amountWei string) {
	ethAddressStore.Set(&core.EthWatchedAddress{CreditChainAddress: ccAddress, EthAddress: ethAddress})
	stethCcc1Store.Mint(normalizeEthAddress(ethAddress), amountWei)
}

// TestApplyLockCollateral_StethCcc1MovesRealBalanceIntoEscrow confirms
// locking STETH_CCC1 as collateral is a REAL balance movement (out of
// the depositor's own vault balance, into collateralEscrowAddress),
// unlike CCT/OETH's pure in-memory counters - both the source balance
// draining and collateralStore's bookkeeping must move together.
func TestApplyLockCollateral_StethCcc1MovesRealBalanceIntoEscrow(t *testing.T) {
	resetApplyTestState()

	const borrower = "steth-lock-borrower"
	const ethAddr = "0xaaaa000000000000000000000000000000aaaa"
	registerStethCollateralTestAddress(borrower, ethAddr, "5000000000000000000") // 5 stETH

	tx := core.Transaction{
		From: borrower,
		Data: `{"amount":2000000000000000000,"symbol":"STETH_CCC1"}`, // lock 2 stETH
	}
	applyLockCollateral(tx)

	if got := collateralStore.LockedOf(borrower, "STETH_CCC1"); got != 2000000000000000000 {
		t.Fatalf("expected 2e18 STETH_CCC1 locked in collateralStore, got %d", got)
	}
	depositAddr := normalizeEthAddress(ethAddr)
	if got := stethCcc1Store.BalanceOf(depositAddr); got != "3000000000000000000" {
		t.Fatalf("expected depositor's real STETH_CCC1 balance drained to 3e18, got %s", got)
	}
	escrowBal := stethCcc1TransferAdapter.BalanceOf(collateralEscrowAddress("STETH_CCC1"))
	if escrowBal != 2000000000000000000 {
		t.Fatalf("expected 2e18 STETH_CCC1 sitting in the collateral escrow, got %d", escrowBal)
	}
}

// TestApplyLockCollateral_StethCcc1FailsIfInsufficientRealBalance
// mirrors the CCT/OETH insufficient-balance guards - re-checked against
// the depositor's real balance at confirmation time, not trusted from
// submission.
func TestApplyLockCollateral_StethCcc1FailsIfInsufficientRealBalance(t *testing.T) {
	resetApplyTestState()

	const borrower = "steth-lock-borrower-2"
	const ethAddr = "0xbbbb000000000000000000000000000000bbbb"
	registerStethCollateralTestAddress(borrower, ethAddr, "1000000000000000000") // 1 stETH

	tx := core.Transaction{
		From: borrower,
		Data: `{"amount":2000000000000000000,"symbol":"STETH_CCC1"}`, // try to lock 2 stETH
	}
	applyLockCollateral(tx)

	if got := collateralStore.LockedOf(borrower, "STETH_CCC1"); got != 0 {
		t.Fatalf("expected nothing locked when balance is insufficient, got %d", got)
	}
	if got := stethCcc1Store.BalanceOf(normalizeEthAddress(ethAddr)); got != "1000000000000000000" {
		t.Fatalf("expected the original balance untouched, got %s", got)
	}
}

// TestCreditCollateralTo_StethCcc1ReleasesRealBalanceToBorrower
// confirms releaseLoanCollateral's real-money release path - the
// borrower's registered self-custodial ETH address is what makes this
// safe (see creditCollateralTo's doc comment on the empty-pubkey
// fallback risk, which can't trigger for a borrower reclaiming their
// own previously-locked STETH_CCC1).
func TestCreditCollateralTo_StethCcc1ReleasesRealBalanceToBorrower(t *testing.T) {
	resetApplyTestState()

	const borrower = "steth-release-borrower"
	const ethAddr = "0xcccc000000000000000000000000000000cccc"
	registerStethCollateralTestAddress(borrower, ethAddr, "3000000000000000000")

	applyLockCollateral(core.Transaction{
		From: borrower,
		Data: `{"amount":3000000000000000000,"symbol":"STETH_CCC1"}`,
	})
	if got := stethCcc1Store.BalanceOf(normalizeEthAddress(ethAddr)); got != "0" {
		t.Fatalf("expected balance fully drained into escrow after lock, got %s", got)
	}

	creditCollateralTo(borrower, "STETH_CCC1", 1500000000000000000) // release half

	if got := stethCcc1Store.BalanceOf(normalizeEthAddress(ethAddr)); got != "1500000000000000000" {
		t.Fatalf("expected 1.5e18 STETH_CCC1 released back to the borrower's real balance, got %s", got)
	}
	escrowBal := stethCcc1TransferAdapter.BalanceOf(collateralEscrowAddress("STETH_CCC1"))
	if escrowBal != 1500000000000000000 {
		t.Fatalf("expected 1.5e18 STETH_CCC1 remaining in escrow, got %d", escrowBal)
	}
}

// TestCollateralValueInYen_StethCcc1PricesViaEthUsdCentsPerEth confirms
// the STETH_CCC1 pricing path added to collateralValueInYen: real
// USD-cents via EthUsdCentsPerEth, treated 1:1 as YEN-e units per the
// approved design decision (see api/apply.go's doc comment).
func TestCollateralValueInYen_StethCcc1PricesViaEthUsdCentsPerEth(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 200_000) // $2,000.00/ETH

	// 2.5 stETH * $2000/ETH = $5000 = 500000 cents = 500000 YEN-e units.
	valueInYen, ok := collateralValueInYen("STETH_CCC1", 2_500_000_000_000_000_000, time.Now().Unix())
	if !ok {
		t.Fatalf("expected a live price with EthUsdCentsPerEth set")
	}
	if valueInYen != 500_000 {
		t.Fatalf("expected 500000 YEN-e (=$5000.00), got %d", valueInYen)
	}
}

// TestCollateralValueInYen_StethCcc1FailsClosedWithNoLivePrice ensures
// an unset EthUsdCentsPerEth returns ok=false (never a fabricated
// zero/infinite value) - callers must fall back to due-date-only
// handling, exactly like CCT already does.
func TestCollateralValueInYen_StethCcc1FailsClosedWithNoLivePrice(t *testing.T) {
	resetApplyTestState()

	_, ok := collateralValueInYen("STETH_CCC1", 1_000_000_000_000_000_000, time.Now().Unix())
	if ok {
		t.Fatalf("expected ok=false when EthUsdCentsPerEth has never been set")
	}
}

// TestCollateralValueInYen_StethCcc1FailsClosedWhenPriceIsStale is a
// regression test for the staleness gate: a real EthUsdCentsPerEth
// value that was set long ago (more than EthUsdCentsPerEthMaxAgeSeconds
// before asOf) must be refused, not silently trusted forever - see
// core.GovernanceParamsStore.IsStale's doc comment.
func TestCollateralValueInYen_StethCcc1FailsClosedWhenPriceIsStale(t *testing.T) {
	resetApplyTestState()

	setAt := int64(1_000_000)
	governanceParamsStore.SetAt("EthUsdCentsPerEth", 200_000, setAt)

	// Just inside the window: still fresh.
	if _, ok := collateralValueInYen("STETH_CCC1", 1_000_000_000_000_000_000, setAt+EthUsdCentsPerEthMaxAgeSeconds); !ok {
		t.Fatalf("expected a price right at the staleness boundary to still be considered fresh")
	}
	// Just past the window: stale.
	if _, ok := collateralValueInYen("STETH_CCC1", 1_000_000_000_000_000_000, setAt+EthUsdCentsPerEthMaxAgeSeconds+1); ok {
		t.Fatalf("expected a price past EthUsdCentsPerEthMaxAgeSeconds to be rejected as stale")
	}
}

// TestCollateralValueInYen_StethCcc1AppliesDepegHaircut confirms
// StethEthRatioBasisPoints (a below-10000 admin override, simulating an
// observed stETH/ETH depeg) reduces the priced value proportionally -
// without this, a depeg would silently let stETH-backed loans stay
// under-collateralized past what the liquidation engine believes.
func TestCollateralValueInYen_StethCcc1AppliesDepegHaircut(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 200_000)          // $2,000.00/ETH
	setGovernanceParamForTest(t, "StethEthRatioBasisPoints", 9_400)     // stETH depegged to 0.94 ETH

	// 1 stETH "face value" = $2000 = 200000 YEN-e at 1:1, but at a 0.94
	// ratio it's worth only 94% of that = 188000 YEN-e.
	valueInYen, ok := collateralValueInYen("STETH_CCC1", 1_000_000_000_000_000_000, time.Now().Unix())
	if !ok {
		t.Fatalf("expected a live price")
	}
	if valueInYen != 188_000 {
		t.Fatalf("expected 188000 YEN-e after the 0.94 depeg haircut, got %d", valueInYen)
	}
}

// TestMeetsMinimumCollateralRatio_StethCcc1UsesRealValueNotRawWei is a
// regression test for the wei-vs-whole-unit magnitude mismatch: a raw
// "CollateralAmount >= Amount" comparison is meaningless for STETH_CCC1
// (any nonzero wei amount is astronomically larger than a normal YEN-e
// loan amount), so it must be priced through collateralValueInYen
// instead - both the too-small-in-real-terms case (must be rejected
// despite a huge wei number) and the genuinely-sufficient case.
func TestMeetsMinimumCollateralRatio_StethCcc1UsesRealValueNotRawWei(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 200_000) // $2,000.00/ETH

	// 0.001 stETH ~= $2.00 = 200 YEN-e units - nowhere near enough to
	// back a 1,000,000 YEN-e loan, even though 1e15 wei dwarfs 1000000
	// as a raw integer.
	dustWei := uint64(1_000_000_000_000_000)
	if meetsMinimumCollateralRatio("STETH_CCC1", dustWei, 1_000_000, time.Now().Unix()) {
		t.Fatalf("expected dust-value STETH_CCC1 collateral to fail the minimum ratio check despite a large raw wei magnitude")
	}

	// 1 stETH = $2000 = 200000 YEN-e units - comfortably covers a 1000
	// YEN-e loan.
	oneStethWei := uint64(1_000_000_000_000_000_000)
	if !meetsMinimumCollateralRatio("STETH_CCC1", oneStethWei, 1000, time.Now().Unix()) {
		t.Fatalf("expected a genuinely-sufficient STETH_CCC1 amount to pass the minimum ratio check")
	}
}

// TestMeetsMinimumCollateralRatio_StethCcc1FailsClosedWithNoLivePrice
// confirms a loan request can't sneak past the minimum-ratio gate just
// because no live ETH/USD rate has been set yet.
func TestMeetsMinimumCollateralRatio_StethCcc1FailsClosedWithNoLivePrice(t *testing.T) {
	resetApplyTestState()

	if meetsMinimumCollateralRatio("STETH_CCC1", 5_000_000_000_000_000_000, 1, time.Now().Unix()) {
		t.Fatalf("expected the minimum ratio check to fail closed with no live EthUsdCentsPerEth rate")
	}
}

// TestMeetsMinimumCollateralRatio_CctOethUnchanged confirms the
// existing raw-magnitude behavior for CCT/OETH is completely
// unaffected by the STETH_CCC1 special case.
func TestMeetsMinimumCollateralRatio_CctOethUnchanged(t *testing.T) {
	if !meetsMinimumCollateralRatio("CCT", 100, 100, time.Now().Unix()) {
		t.Fatalf("expected CCT collateral == loan amount to pass")
	}
	if meetsMinimumCollateralRatio("CCT", 99, 100, time.Now().Unix()) {
		t.Fatalf("expected CCT collateral < loan amount to fail")
	}
	if !meetsMinimumCollateralRatio("OETH", 500, 100, time.Now().Unix()) {
		t.Fatalf("expected OETH collateral > loan amount to pass")
	}
}

// TestHasEnoughCollateral_StethCcc1RejectsDustWeiAgainstRealLoanAmount
// is a regression test for the wei-vs-YEN-e magnitude mismatch: a raw
// "locked wei >= loan amount" comparison would treat almost any nonzero
// STETH_CCC1 lock as "enough" to back nearly any loan, since a wei
// integer dwarfs a whole-unit YEN-e amount. hasEnoughCollateral must
// price it through stethCcc1WeiNeededForYenValue instead.
func TestHasEnoughCollateral_StethCcc1RejectsDustWeiAgainstRealLoanAmount(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 200_000) // $2,000.00/ETH

	const borrower = "steth-hasenough-borrower"
	// Lock a dust amount: 1e12 wei (~$0.000002) - a raw magnitude
	// comparison against a 1000 YEN-e loan would wrongly pass (1e12 >
	// 1000), but this is nowhere near enough real value.
	collateralStore.Add(borrower, "STETH_CCC1", 1_000_000)

	if hasEnoughCollateral(borrower, 1000, time.Now().Unix()) {
		t.Fatalf("expected dust-wei STETH_CCC1 collateral to be rejected for a 1000 YEN-e loan")
	}

	// 1 stETH ($2000 = 200000 YEN-e) comfortably covers a 1000 YEN-e loan.
	collateralStore.Add(borrower, "STETH_CCC1", 1_000_000_000_000_000_000)
	if !hasEnoughCollateral(borrower, 1000, time.Now().Unix()) {
		t.Fatalf("expected a genuinely-sufficient STETH_CCC1 lock to pass")
	}
}

// TestReserveCollateralForLoan_StethCcc1ReservesRealValueNotRawAmount
// confirms the amount actually reserved (and later released 1:1 on
// repayment) is priced correctly - not just "amount" wei (which would
// reserve a dust-sized sliver of the real lock for any loan size).
func TestReserveCollateralForLoan_StethCcc1ReservesRealValueNotRawAmount(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 200_000) // $2,000.00/ETH

	const borrower = "steth-reserve-borrower"
	collateralStore.Add(borrower, "STETH_CCC1", 1_000_000_000_000_000_000) // 1 stETH

	// 1000 YEN-e units = 1000 USD cents = $10.00; at $2000/ETH that's
	// 0.005 stETH = 5e15 wei.
	symbol, reserved, ok := reserveCollateralForLoan(borrower, 1000, time.Now().Unix())
	if !ok || symbol != "STETH_CCC1" {
		t.Fatalf("expected reservation to succeed against STETH_CCC1, got symbol=%q ok=%v", symbol, ok)
	}
	if reserved != 5_000_000_000_000_000 {
		t.Fatalf("expected reservedAmount to be exactly 5e15 wei, got %d", reserved)
	}

	remaining := collateralStore.LockedOf(borrower, "STETH_CCC1")
	if remaining != 995_000_000_000_000_000 {
		t.Fatalf("expected exactly 5e15 wei (0.005 stETH = $10) reserved out of the 1 stETH lock, got %d remaining locked", remaining)
	}
}

// TestLiquidateLoan_StethCcc1SeizesRealBalanceToLender confirms the
// real-money seizure path: an under-collateralized STETH_CCC1 loan
// forfeits collateral into the LENDER's real STETH_CCC1 balance (not
// just an internal counter, unlike CCT/OETH), and the lender must
// already have a registered self-custodial ETH address for this to
// land somewhere reachable (see creditCollateralTo's doc comment).
func TestLiquidateLoan_StethCcc1SeizesRealBalanceToLender(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 200_000) // $2,000.00/ETH

	const borrower = "steth-liq-borrower"
	const lender = "steth-liq-lender"
	const lenderEth = "0xdddd000000000000000000000000000000dddd"
	ethAddressStore.Set(&core.EthWatchedAddress{CreditChainAddress: lender, EthAddress: lenderEth})

	// Seed the collateral escrow directly (as if this amount had
	// already been locked via applyLockCollateral) - 1 stETH = $2000 =
	// 200000 YEN-e, covering a 1000 YEN-e loan several times over so
	// the shortfall path below is unambiguous (owed fully covered).
	stethCcc1TransferAdapter.Mint(collateralEscrowAddress("STETH_CCC1"), 1_000_000_000_000_000_000)

	loan := &core.Loan{
		ID:               "steth-liq-loan",
		Borrower:         borrower,
		Lender:           lender,
		Symbol:           "YENE",
		Amount:           1000,
		Status:           core.LoanFunded,
		CollateralSymbol: "STETH_CCC1",
		CollateralAmount: 1_000_000_000_000_000_000,
		DueAt:            1000,
	}

	shortfall := liquidateLoan(loan, 5000, "test")
	if shortfall != 0 {
		t.Fatalf("expected no shortfall - collateral value far exceeds what's owed, got %d", shortfall)
	}

	lenderBal := stethCcc1Store.BalanceOf(normalizeEthAddress(lenderEth))
	if lenderBal == "0" {
		t.Fatalf("expected the lender to receive real seized STETH_CCC1 balance, got 0")
	}
}

// TestApplyLoanOfferDraw_RefusesStethCcc1FundingForUnregisteredLender
// is a regression test for the unregistered-lender seizure gap (see
// audit/KNOWN_GAPS.md) - a STETH_CCC1-collateralized draw against an
// offer whose lender has never registered a self-custodial ETH address
// must be refused (and the reserved collateral refunded), not create a
// loan whose eventual forfeiture path is a known dead end.
func TestApplyLoanOfferDraw_RefusesStethCcc1FundingForUnregisteredLender(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 200_000) // $2,000.00/ETH

	const borrower = "steth-draw-borrower"
	const lender = "steth-draw-unregistered-lender" // deliberately never registered
	collateralStore.Add(borrower, "STETH_CCC1", 1_000_000_000_000_000_000) // 1 stETH
	cctStore.Mint(lender, core.FormatCctSmallest(core.CctWholeToSmallest(10_000)))

	loanOfferStore.Create(&core.LoanOffer{
		ID: "offer-unregistered-lender", Lender: lender, Symbol: "CCT",
		RatePercent: 5, TotalAmount: 1000, RemainingAmount: 1000, Status: core.LoanOfferOpen,
	})

	data, _ := json.Marshal(loanOfferDrawData{OfferID: "offer-unregistered-lender", Amount: 1000})
	applyLoanOfferDraw(core.Transaction{From: borrower, Data: string(data), Timestamp: time.Now().Unix()})

	if got := len(loanStore.Loans); got != 0 {
		t.Fatalf("expected no loan to be created against an unregistered lender, got %d loans", got)
	}
	if got := collateralStore.LockedOf(borrower, "STETH_CCC1"); got != 1_000_000_000_000_000_000 {
		t.Fatalf("expected the reserved collateral to be fully refunded back to the pool, got %d locked", got)
	}
	offer, _ := loanOfferStore.Get("offer-unregistered-lender")
	if offer.RemainingAmount != 1000 {
		t.Fatalf("expected the offer's capital to be untouched, got %d remaining", offer.RemainingAmount)
	}
}

// TestApplyLoanOfferDraw_AllowsStethCcc1FundingForRegisteredLender is
// the positive-case twin - once the lender registers, the exact same
// draw succeeds and creates a real STETH_CCC1-collateralized loan.
func TestApplyLoanOfferDraw_AllowsStethCcc1FundingForRegisteredLender(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 200_000) // $2,000.00/ETH

	const borrower = "steth-draw-borrower-2"
	const lender = "steth-draw-registered-lender"
	const lenderEth = "0xeeee000000000000000000000000000000eeee"
	ethAddressStore.Set(&core.EthWatchedAddress{CreditChainAddress: lender, EthAddress: lenderEth})
	collateralStore.Add(borrower, "STETH_CCC1", 1_000_000_000_000_000_000) // 1 stETH
	cctStore.Mint(lender, core.FormatCctSmallest(core.CctWholeToSmallest(10_000)))

	loanOfferStore.Create(&core.LoanOffer{
		ID: "offer-registered-lender", Lender: lender, Symbol: "CCT",
		RatePercent: 5, TotalAmount: 1000, RemainingAmount: 1000, Status: core.LoanOfferOpen,
	})

	data, _ := json.Marshal(loanOfferDrawData{OfferID: "offer-registered-lender", Amount: 1000})
	applyLoanOfferDraw(core.Transaction{ID: "draw-tx-1", From: borrower, Data: string(data), Timestamp: time.Now().Unix()})

	if len(loanStore.Loans) != 1 {
		t.Fatalf("expected exactly one loan created, got %d", len(loanStore.Loans))
	}
	var loan *core.Loan
	for _, l := range loanStore.Loans {
		loan = l
	}
	if loan.CollateralSymbol != "STETH_CCC1" || loan.Lender != lender {
		t.Fatalf("expected a STETH_CCC1-collateralized loan to %s, got symbol=%q lender=%q", lender, loan.CollateralSymbol, loan.Lender)
	}
}

// TestApplyLoanRequestFulfill_RefusesStethCcc1FundingForUnregisteredLender
// is the loan-request-side twin of the offer-draw regression above.
func TestApplyLoanRequestFulfill_RefusesStethCcc1FundingForUnregisteredLender(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 200_000)

	const borrower = "steth-reqfulfill-borrower"
	const lender = "steth-reqfulfill-unregistered-lender"
	cctStore.Mint(lender, core.FormatCctSmallest(core.CctWholeToSmallest(10_000)))

	loanRequestStore.Create(&core.LoanRequest{
		ID: "req-unregistered", Borrower: borrower, RequestedSymbol: "CCT", Amount: 1000,
		CollateralSymbol: "STETH_CCC1", CollateralAmount: 1_000_000_000_000_000_000,
		MaxInterestPercent: 10, Status: core.LoanRequestOpen,
	})
	negotiationStore.Create(&core.Negotiation{
		ID: "neg-unregistered", RequestID: "req-unregistered", Lender: lender,
		Borrower: borrower, Amount: 1000, Symbol: "CCT", RatePercent: 5,
	})

	data, _ := json.Marshal(negotiationActionData{NegotiationID: "neg-unregistered"})
	applyLoanRequestFulfill(core.Transaction{From: lender, Data: string(data), Timestamp: time.Now().Unix()})

	if got := len(loanStore.Loans); got != 0 {
		t.Fatalf("expected no loan to be created for an unregistered lender, got %d loans", got)
	}
	request, _ := loanRequestStore.Get("req-unregistered")
	if request.Status != core.LoanRequestOpen {
		t.Fatalf("expected the request to remain OPEN (not fulfilled), got %s", request.Status)
	}
}

// TestProcessCollateralLiquidations_DiscoversAndSweepsStethCcc1Loans is
// a regression test for the discovery half of the sweep, not just
// liquidateLoan's math (already covered by
// TestLiquidateLoan_StethCcc1SeizesRealBalanceToLender above) - before
// this feature, processCollateralLiquidations only ever looked at
// OETH-collateralized loans; this confirms a STETH_CCC1-collateralized
// loan is actually FOUND by the sweep's own loanStore.Loans scan and
// progressed through the same Warning -> MarginCall -> GracePeriod ->
// AwaitingFinalRedemption stages CCT/OETH loans go through (see
// TestProcessLoanDefaults_OverdueLoanForfeitsCollateralAndReputationToLender
// for the due-date-triggered twin of this same stage machine), while a
// genuinely healthy STETH_CCC1 loan is left untouched by the same sweep
// pass.
func TestProcessCollateralLiquidations_DiscoversAndSweepsStethCcc1Loans(t *testing.T) {
	resetApplyTestState()
	creditEngine.Wallets = walletStore
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 200_000) // $2,000.00/ETH

	const atRiskBorrower = "steth-sweep-atrisk-borrower"
	const atRiskLender = "steth-sweep-atrisk-lender"
	const healthyBorrower = "steth-sweep-healthy-borrower"
	const healthyLender = "steth-sweep-healthy-lender"

	// At-risk loan: owes 1000 YEN-e, backed by only 0.001 stETH ($2.00 =
	// 200 YEN-e) - well under the 130% STETH_CCC1 threshold.
	atRiskLoan := &core.Loan{
		ID: "steth-sweep-atrisk-loan", Borrower: atRiskBorrower, Lender: atRiskLender,
		Symbol: "YENE", Amount: 1000, Status: core.LoanFunded,
		CollateralSymbol: "STETH_CCC1", CollateralAmount: 1_000_000_000_000_000, // 0.001 stETH
	}
	loanStore.Loans[atRiskLoan.ID] = atRiskLoan

	// Healthy loan: owes 100 YEN-e, backed by 1 full stETH ($2000 =
	// 200000 YEN-e) - nowhere near the threshold.
	healthyLoan := &core.Loan{
		ID: "steth-sweep-healthy-loan", Borrower: healthyBorrower, Lender: healthyLender,
		Symbol: "YENE", Amount: 100, Status: core.LoanFunded,
		CollateralSymbol: "STETH_CCC1", CollateralAmount: 1_000_000_000_000_000_000, // 1 stETH
	}
	loanStore.Loans[healthyLoan.ID] = healthyLoan

	asOf := int64(1000)
	processCollateralLiquidations(asOf)
	if atRiskLoan.RiskStage != core.LoanRiskWarning {
		t.Fatalf("expected the at-risk STETH_CCC1 loan to be discovered and enter WARNING, got %q", atRiskLoan.RiskStage)
	}
	if healthyLoan.RiskStage != core.LoanRiskHealthy {
		t.Fatalf("expected the healthy STETH_CCC1 loan to be left untouched, got %q", healthyLoan.RiskStage)
	}

	asOf += core.LoanRiskWarningDwellSeconds + 1
	processCollateralLiquidations(asOf)
	if atRiskLoan.RiskStage != core.LoanRiskMarginCall {
		t.Fatalf("expected MARGIN_CALL after the warning dwell, got %q", atRiskLoan.RiskStage)
	}

	asOf += core.LoanRiskMarginCallDwellSeconds + 1
	processCollateralLiquidations(asOf)
	if atRiskLoan.RiskStage != core.LoanRiskGracePeriod {
		t.Fatalf("expected GRACE_PERIOD after the margin call dwell, got %q", atRiskLoan.RiskStage)
	}

	asOf = atRiskLoan.RiskStageDeadline + 1
	processCollateralLiquidations(asOf)
	if atRiskLoan.RiskStage != core.LoanRiskAwaitingFinalRedemption {
		t.Fatalf("expected AWAITING_FINAL_REDEMPTION after the grace period expired, got %q", atRiskLoan.RiskStage)
	}
	// The healthy loan must still be completely untouched after every
	// pass above - confirms the sweep's per-loan symbol/status filter
	// doesn't accidentally sweep every STETH_CCC1 loan indiscriminately.
	if healthyLoan.RiskStage != core.LoanRiskHealthy {
		t.Fatalf("expected the healthy STETH_CCC1 loan to remain HEALTHY throughout, got %q", healthyLoan.RiskStage)
	}
}

// TestEndToEnd_StethCcc1LoanRequestCreateFulfillRepay drives the full
// borrower-posts-request lifecycle for a STETH_CCC1-collateralized
// loan, start to finish: lock real STETH_CCC1 collateral -> post a
// loan request against it -> a registered lender fulfills it via
// negotiation -> the borrower repays -> the real STETH_CCC1 collateral
// is released back to the borrower's real balance and the loan is
// marked REPAID. Each step calls the real apply* function (not just
// the lower-level pieces already covered by the other tests in this
// file), so a regression in how they compose - not just how each
// works in isolation - would be caught here.
func TestEndToEnd_StethCcc1LoanRequestCreateFulfillRepay(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 200_000) // $2,000.00/ETH

	const borrower = "steth-e2e-borrower"
	const borrowerEth = "0xf00d000000000000000000000000000000f00d"
	const lender = "steth-e2e-lender"
	const lenderEth = "0xbeef000000000000000000000000000000beef"
	registerStethCollateralTestAddress(borrower, borrowerEth, "2000000000000000000") // 2 stETH
	ethAddressStore.Set(&core.EthWatchedAddress{CreditChainAddress: lender, EthAddress: lenderEth})
	cctStore.Mint(lender, core.FormatCctSmallest(core.CctWholeToSmallest(10_000)))

	// 1. Lock 1 of the 2 stETH as collateral.
	applyLockCollateral(core.Transaction{
		From: borrower, Data: `{"amount":1000000000000000000,"symbol":"STETH_CCC1"}`,
	})
	if got := collateralStore.LockedOf(borrower, "STETH_CCC1"); got != 1_000_000_000_000_000_000 {
		t.Fatalf("test setup invalid: expected 1e18 STETH_CCC1 locked, got %d", got)
	}

	// 2. Post a loan request for 1000 CCT against all of it - 1 stETH =
	// $2000 = 200000 YEN-e-equivalent, far more than enough to cover a
	// 1000 CCT request under meetsMinimumCollateralRatio.
	reqData, _ := json.Marshal(loanRequestCreateData{
		RequestedSymbol: "CCT", Amount: 1000, CollateralSymbol: "STETH_CCC1",
		CollateralAmount: 1_000_000_000_000_000_000, MaxInterestPercent: 10,
	})
	const e2eReqTxID = "e2e-req-tx"
	applyLoanRequestCreate(core.Transaction{
		ID: e2eReqTxID, From: borrower, Data: string(reqData), Timestamp: time.Now().Unix(),
	})
	reqID := "loanreq-" + e2eReqTxID[:min(8, len(e2eReqTxID))]
	request, ok := loanRequestStore.Get(reqID)
	if !ok {
		t.Fatalf("test setup invalid: expected the loan request to have been created")
	}
	if got := collateralStore.LockedOf(borrower, "STETH_CCC1"); got != 0 {
		t.Fatalf("expected the request to reserve all locked STETH_CCC1 out of the general pool, got %d still locked", got)
	}

	// 3. A registered lender negotiates and fulfills it.
	negotiationStore.Create(&core.Negotiation{
		ID: "e2e-neg", RequestID: request.ID, Lender: lender, Borrower: borrower,
		Amount: request.Amount, Symbol: "CCT", RatePercent: 5,
	})
	fulfillData, _ := json.Marshal(negotiationActionData{NegotiationID: "e2e-neg"})
	applyLoanRequestFulfill(core.Transaction{
		From: lender, Data: string(fulfillData), Timestamp: time.Now().Unix(),
	})

	if len(loanStore.Loans) != 1 {
		t.Fatalf("expected exactly one funded loan, got %d", len(loanStore.Loans))
	}
	var loan *core.Loan
	for _, l := range loanStore.Loans {
		loan = l
	}
	if loan.Status != core.LoanFunded || loan.CollateralSymbol != "STETH_CCC1" || loan.CollateralAmount != 1_000_000_000_000_000_000 {
		t.Fatalf("expected a FUNDED STETH_CCC1-collateralized loan for 1e18 wei, got status=%s symbol=%s amount=%d", loan.Status, loan.CollateralSymbol, loan.CollateralAmount)
	}
	borrowerCctSide := resolveBalanceAddress(borrower, "", "CCT")
	if got := cctStore.BalanceOf(borrowerCctSide); got != core.FormatCctSmallest(core.CctWholeToSmallest(1000)) {
		t.Fatalf("expected the borrower to actually receive the 1000 CCT loaned, got %s", got)
	}

	// 4. Give the borrower enough to repay (principal + 5% interest =
	// 1050 CCT) and repay.
	cctStore.Mint(borrowerCctSide, core.FormatCctSmallest(core.CctWholeToSmallest(50)))
	repayData, _ := json.Marshal(repayData{LoanID: loan.ID, Mode: "p2p"})
	applyRepay(core.Transaction{
		From: borrower, Data: string(repayData), Timestamp: time.Now().Unix(),
		AuthPublicKey: "",
	})

	if loan.Status != core.LoanRepaid {
		t.Fatalf("expected the loan to be marked REPAID, got %s", loan.Status)
	}
	// The borrower started with 2 stETH, locked 1 (leaving 1 already in
	// their real balance) - after release, the full 2 stETH should be
	// back together in their real balance.
	if got := stethCcc1Store.BalanceOf(normalizeEthAddress(borrowerEth)); got != "2000000000000000000" {
		t.Fatalf("expected the full 2e18 wei STETH_CCC1 (1e18 untouched + 1e18 released) back in the borrower's real balance, got %s", got)
	}
	lenderCctSide := resolveBalanceAddress(lender, "", "CCT")
	if got := cctStore.BalanceOf(lenderCctSide); got != core.FormatCctSmallest(core.CctWholeToSmallest(9_000+1_050)) {
		t.Fatalf("expected the lender to have their 9000 CCT remainder plus the 1050 CCT repayment, got %s", got)
	}
}
