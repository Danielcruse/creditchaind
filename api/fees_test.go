package api

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func TestApplyTransfer_CCTSendDeductsAmountPlusFeeIntoPool(t *testing.T) {
	resetApplyTestState()

	const from = "fee-test-sender"
	const to = "fee-test-recipient"
	cctStore.Mint(from, cctW(100))

	tx := core.Transaction{
		ID:     "test-cct-send",
		From:   from,
		To:     to,
		Amount: 20,
		Type:   core.TxExchange,
		Data:   `{"to":"` + to + `","amount":20,"symbol":"CCT"}`,
	}
	applyTransfer(tx)

	wantSenderLeft := new(big.Int).Sub(core.CctWholeToSmallest(100-20), big.NewInt(int64(DefaultCCTTransferFeeSmallestUnit)))
	if got := cctStore.BalanceOf(from); got != core.FormatCctSmallest(wantSenderLeft) {
		t.Fatalf("expected sender balance %s (100-20-fee), got %s", core.FormatCctSmallest(wantSenderLeft), got)
	}
	if got := cctStore.BalanceOf(to); got != cctW(20) {
		t.Fatalf("expected recipient to receive 20, got %s", got)
	}
	wantFeePool := core.FormatCctSmallest(big.NewInt(int64(DefaultCCTTransferFeeSmallestUnit)))
	if got := cctStore.BalanceOf(cctTransferFeePoolAddress()); got != wantFeePool {
		t.Fatalf("expected fee pool to hold %s, got %s", wantFeePool, got)
	}
}

func TestApplyTransfer_CCTSendInsufficientForFeeMovesNothing(t *testing.T) {
	resetApplyTestState()

	const from = "fee-test-sender-2"
	const to = "fee-test-recipient-2"
	// Exactly enough for the amount but not the fee on top of it.
	cctStore.Mint(from, cctW(20))

	tx := core.Transaction{
		ID:     "test-cct-send-2",
		From:   from,
		To:     to,
		Amount: 20,
		Type:   core.TxExchange,
		Data:   `{"to":"` + to + `","amount":20,"symbol":"CCT"}`,
	}
	applyTransfer(tx)

	if got := cctStore.BalanceOf(from); got != cctW(20) {
		t.Fatalf("expected sender balance untouched at 20, got %s", got)
	}
	if got := cctStore.BalanceOf(to); got != "0" {
		t.Fatalf("expected recipient to receive nothing, got %s", got)
	}
}

// TestDefaultCCTTransferFeeIsExactlyPointThreeCCT pins the real-world
// value: a fee of 0.3 CCT, not just "some fraction less than the old
// 1 CCT default" - the specific number this session's fee-reduction
// request actually asked for.
func TestDefaultCCTTransferFeeIsExactlyPointThreeCCT(t *testing.T) {
	want := new(big.Int).Div(core.CctWholeToSmallest(3), big.NewInt(10)) // 0.3 CCT
	got := big.NewInt(int64(DefaultCCTTransferFeeSmallestUnit))
	if got.Cmp(want) != 0 {
		t.Fatalf("DefaultCCTTransferFeeSmallestUnit = %s, want %s (0.3 CCT)", got, want)
	}
	if display := core.CctSmallestToDecimalString(got); display != "0.3" {
		t.Fatalf("CctSmallestToDecimalString(DefaultCCTTransferFeeSmallestUnit) = %q, want \"0.3\"", display)
	}
}

func TestApplyTransfer_NonCCTSendCarriesNoFee(t *testing.T) {
	resetApplyTestState()

	// Real public keys for both sides - ETHTEST's derived sub-address
	// depends only on (publicKey, tag), not the cc-address itself, so
	// two wallets with no real key (as this test used to have) would
	// collide on the identical derived address and silently transfer to
	// themselves. A real signed transaction always carries a real
	// AuthPublicKey; this test now matches that.
	fromPub, _, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(fromPub)
	toPub, _, _ := core.GenerateKeyPair()
	to := core.GenerateAddress(toPub)
	walletStore.Get(to).PublicKeyHex = hex.EncodeToString(toPub)

	// ETHTEST lives at a derived sub-address, not the raw wallet
	// address (see resolveBalanceAddress) - mint to the exact address
	// applyTransfer will resolve to, same as a real ETHTEST balance
	// would already sit there.
	fromSide := resolveBalanceAddress(from, hex.EncodeToString(fromPub), "ETHTEST")
	toSide := resolveBalanceAddress(to, hex.EncodeToString(toPub), "ETHTEST")
	ethTestStore.Mint(fromSide, 20)

	tx := core.Transaction{
		ID:            "test-ethtest-send",
		From:          from,
		To:            to,
		Amount:        20,
		Type:          core.TxExchange,
		AuthPublicKey: hex.EncodeToString(fromPub),
		Data:          `{"to":"` + to + `","amount":20,"symbol":"ETHTEST"}`,
	}
	applyTransfer(tx)

	if got := ethTestStore.BalanceOf(fromSide); got != 0 {
		t.Fatalf("expected ETHTEST sender balance 0 (no fee deducted, full amount sent), got %d", got)
	}
	if got := ethTestStore.BalanceOf(toSide); got != 20 {
		t.Fatalf("expected recipient to receive the full 20 ETHTEST at their resolved derived address, got %d", got)
	}
}

func TestApplyBlockReward_SweepsCollectedFeesToValidator(t *testing.T) {
	resetApplyTestState()

	const validator = "fee-test-validator"
	cctStore.Mint(cctTransferFeePoolAddress(), cctW(7))
	treasuryStore.Treasury.CCTBurnedPendingReward = 10

	block := core.Block{Validator: validator}
	applyBlockReward(block)

	// Block reward is 90% of the burned-CCT accumulator (10 -> 9),
	// plus the swept 7 CCT of transfer fees.
	if got := cctStore.BalanceOf(validator); got != cctW(9+7) {
		t.Fatalf("expected validator to receive block reward (9) plus swept fees (7) = 16, got %s", got)
	}
	if got := cctStore.BalanceOf(cctTransferFeePoolAddress()); got != "0" {
		t.Fatalf("expected fee pool drained to 0 after sweep, got %s", got)
	}
	if treasuryStore.Treasury.CCTBurnedPendingReward != 0 {
		t.Fatalf("expected CCTBurnedPendingReward reset to 0 after payout, got %d", treasuryStore.Treasury.CCTBurnedPendingReward)
	}
}

func TestApplyCompanyApply_DeductsFeeAndBlocksWhenInsufficient(t *testing.T) {
	resetApplyTestState()
	companyStore = core.NewCompanyStore()
	moderatorStore = core.NewModeratorStore()
	treasuryStore.Treasury.FoundationAddress = "fee-test-foundation-addr"

	const moderator = "fee-test-hub-moderator"
	moderatorStore.Register(moderator, "moderator-pubkey", 900)

	const applicant = "fee-test-hub-applicant"
	// applyCompanyApply requires HubMinimumStakeCCT staked before it
	// will even consider the fee - see the reordering fix in
	// api/apply.go (stake check runs before the fee is deducted, so an
	// under-staked applicant is never charged for a doomed application).
	stakingStore.Stakes[applicant] = map[string]uint64{"CCT": HubMinimumStakeCCT}
	cctStore.Mint(applicant, cctW(HubApplyFeeCCT))

	tx := core.Transaction{
		ID:   "test-hub-apply",
		From: applicant,
		Type: core.TxCompanyApply,
		Data: `{"name":"Test Hub","country":"Nigeria"}`,
	}
	applyCompanyApply(tx)

	if got := cctStore.BalanceOf(applicant); got != "0" {
		t.Fatalf("expected applicant balance drained by the fee, got %s", got)
	}
	// HubApplyFeeCCT (25) split 40% admin / 40% moderator / 20%
	// validator pool (see api/apply.go's applyCompanyApply) - replaces
	// the old single-destination registrationFeeAddress model.
	if got := cctStore.BalanceOf(treasuryStore.Treasury.FoundationAddress); got != cctW(10) {
		t.Fatalf("expected admin (Foundation Treasury) share of 10, got %s", got)
	}
	if got := cctStore.BalanceOf(moderator); got != cctW(10) {
		t.Fatalf("expected the assigned moderator's share of 10, got %s", got)
	}
	if got := cctStore.BalanceOf(cctTransferFeePoolAddress()); got != cctW(5) {
		t.Fatalf("expected the validator pool's share of 5, got %s", got)
	}
	app, ok := companyStore.Get(applicant)
	if !ok {
		t.Fatalf("expected a company application to be recorded")
	}
	if app.ModeratorAddress != moderator {
		t.Fatalf("expected the application to be assigned to %s, got %s", moderator, app.ModeratorAddress)
	}

	// A second applicant with neither stake nor CCT should not get an
	// application recorded (rejected by the stake check, before the
	// fee is ever considered).
	const poorApplicant = "fee-test-hub-applicant-poor"
	tx2 := core.Transaction{
		ID:   "test-hub-apply-poor",
		From: poorApplicant,
		Type: core.TxCompanyApply,
		Data: `{"name":"Poor Hub"}`,
	}
	applyCompanyApply(tx2)
	if _, ok := companyStore.Get(poorApplicant); ok {
		t.Fatalf("expected no application recorded for an applicant who couldn't cover the stake requirement")
	}

	// A third applicant clears the stake requirement but can't cover
	// the fee - also no application, and no partial charge.
	const unfundedApplicant = "fee-test-hub-applicant-unfunded"
	stakingStore.Stakes[unfundedApplicant] = map[string]uint64{"CCT": HubMinimumStakeCCT}
	tx3 := core.Transaction{
		ID:   "test-hub-apply-unfunded",
		From: unfundedApplicant,
		Type: core.TxCompanyApply,
		Data: `{"name":"Unfunded Hub"}`,
	}
	applyCompanyApply(tx3)
	if _, ok := companyStore.Get(unfundedApplicant); ok {
		t.Fatalf("expected no application recorded for a staked-but-unfunded applicant")
	}
	// The unfunded applicant's attempt must not have moved anything at
	// all - the upfront cctAdapter.BalanceOf check in applyCompanyApply
	// exists specifically to prevent a partial charge (some legs of the
	// 3-way split succeeding, others failing) when an applicant can't
	// actually cover the whole fee.
	if got := cctStore.BalanceOf(treasuryStore.Treasury.FoundationAddress); got != cctW(10) {
		t.Fatalf("expected admin share to still be only the first applicant's 10, got %s", got)
	}
	if got := cctStore.BalanceOf(moderator); got != cctW(10) {
		t.Fatalf("expected moderator share to still be only the first applicant's 10, got %s", got)
	}
	if got := cctStore.BalanceOf(cctTransferFeePoolAddress()); got != cctW(5) {
		t.Fatalf("expected validator pool share to still be only the first applicant's 5, got %s", got)
	}
}

// TestApplyCompanyReview_ChargesNoFeeEvenWithZeroBalance is the direct
// positive proof for this session's fee-consolidation decision: a hub
// with a genuinely ZERO CCT balance can still be successfully approved,
// because the only fee is now the one already paid in full at
// application time (see applyCompanyApply) - there's no hidden
// approval-time balance gate left anywhere in this path.
func TestApplyCompanyReview_ChargesNoFeeEvenWithZeroBalance(t *testing.T) {
	resetApplyTestState()
	companyStore = core.NewCompanyStore()

	const moderator = "zero-fee-test-moderator"
	const hub = "zero-fee-test-hub"
	companyStore.Apply(&core.CompanyApplication{
		Address:          hub,
		Name:             "Zero Balance Hub",
		Country:          "Nigeria",
		Status:           core.CompanyPending,
		ModeratorAddress: moderator,
	})
	app, _ := companyStore.Get(hub)
	app.CertificateHash = "some-real-hash"

	if got := cctStore.BalanceOf(hub); got != "0" {
		t.Fatalf("test setup issue: expected the hub to start with a zero CCT balance, got %s", got)
	}

	approveData, _ := json.Marshal(companyReviewData{Address: hub, Approve: true})
	applyCompanyReview(core.Transaction{
		ID:        "zero-fee-review",
		From:      moderator,
		Type:      core.TxCompanyReview,
		Data:      string(approveData),
		Timestamp: 1000,
	})

	app, ok := companyStore.Get(hub)
	if !ok || app.Status != core.CompanyVerified {
		t.Fatalf("expected the hub to be VERIFIED despite a zero balance, got %+v", app)
	}
	if got := cctStore.BalanceOf(hub); got != "0" {
		t.Fatalf("expected the hub's balance to remain untouched (still 0), got %s", got)
	}
}

func TestApplyModeratorRegister_DeductsFeeAndBlocksWhenInsufficient(t *testing.T) {
	resetApplyTestState()
	moderatorStore = core.NewModeratorStore()

	const addr = "fee-test-moderator"
	walletStore.Get(addr).Reputation = 50
	// applyModeratorRegister now also requires moderatorStakeRequirementCCT
	// staked before it will even consider the fee - see the stake-check
	// addition in api/apply.go (mirrors applyCompanyApply's stake-before-fee
	// ordering).
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": DefaultModeratorStakeRequirementCCT}
	cctStore.Mint(addr, cctW(ModeratorRegisterFeeCCT))

	tx := core.Transaction{ID: "test-mod-register", From: addr, Type: core.TxModeratorRegister, Timestamp: 1000}
	applyModeratorRegister(tx)

	if got := cctStore.BalanceOf(addr); got != "0" {
		t.Fatalf("expected moderator's balance drained by the fee, got %s", got)
	}
	if !moderatorStore.IsModerator(addr) {
		t.Fatalf("expected address to be registered as a moderator")
	}

	const poorAddr = "fee-test-moderator-poor"
	walletStore.Get(poorAddr).Reputation = 50
	stakingStore.Stakes[poorAddr] = map[string]uint64{"CCT": DefaultModeratorStakeRequirementCCT}
	tx2 := core.Transaction{ID: "test-mod-register-poor", From: poorAddr, Type: core.TxModeratorRegister, Timestamp: 1000}
	applyModeratorRegister(tx2)
	if moderatorStore.IsModerator(poorAddr) {
		t.Fatalf("expected registration to fail for an address that couldn't cover the fee")
	}

	const unstakedAddr = "fee-test-moderator-unstaked"
	walletStore.Get(unstakedAddr).Reputation = 50
	cctStore.Mint(unstakedAddr, cctW(ModeratorRegisterFeeCCT))
	tx3 := core.Transaction{ID: "test-mod-register-unstaked", From: unstakedAddr, Type: core.TxModeratorRegister, Timestamp: 1000}
	applyModeratorRegister(tx3)
	if moderatorStore.IsModerator(unstakedAddr) {
		t.Fatalf("expected registration to fail for an address with no staked CCT")
	}
	if got := cctStore.BalanceOf(unstakedAddr); got != cctW(ModeratorRegisterFeeCCT) {
		t.Fatalf("expected no fee charged when the stake requirement rejects registration, got %s", got)
	}
}

func TestApplyValidatorRegister_DeductsFeeAndBlocksWhenInsufficient(t *testing.T) {
	resetApplyTestState()
	validatorStore = core.NewValidatorStore()

	const addr = "fee-test-validator-applicant"
	w := walletStore.Get(addr)
	w.Reputation = 50
	validatorApplicationStore.Apps[addr] = &core.ValidatorApplication{Address: addr, Status: core.ValidatorApplicationApproved}
	// applyValidatorRegister calls stakingStore.SyncWallet, which
	// overwrites wallet.StakedCCT from the staking ledger - setting it
	// directly on the wallet would just get clobbered back to 0. CCT
	// specifically (not ETHTEST) - PoSR gates validator eligibility on
	// CCT stake, see api/apply.go's applyValidatorRegister.
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": 2000}
	cctStore.Mint(addr, cctW(ValidatorRegisterFeeCCT))

	tx := core.Transaction{
		ID:   "test-validator-register",
		From: addr,
		Type: core.TxValidatorRegister,
		Data: `{"nodeId":"node-1","nodePublicKey":"abc"}`,
	}
	applyValidatorRegister(tx)

	if got := cctStore.BalanceOf(addr); got != "0" {
		t.Fatalf("expected validator applicant's balance drained by the fee, got %s", got)
	}
	if _, ok := validatorStore.Get(addr); !ok {
		t.Fatalf("expected address to be registered as a validator")
	}

	const poorAddr = "fee-test-validator-applicant-poor"
	pw := walletStore.Get(poorAddr)
	pw.Reputation = 50
	validatorApplicationStore.Apps[poorAddr] = &core.ValidatorApplication{Address: poorAddr, Status: core.ValidatorApplicationApproved}
	stakingStore.Stakes[poorAddr] = map[string]uint64{"CCT": 2000}
	tx2 := core.Transaction{
		ID:   "test-validator-register-poor",
		From: poorAddr,
		Type: core.TxValidatorRegister,
		Data: `{"nodeId":"node-1","nodePublicKey":"abc"}`,
	}
	applyValidatorRegister(tx2)
	if _, ok := validatorStore.Get(poorAddr); ok {
		t.Fatalf("expected validator registration to fail for an address that couldn't cover the fee")
	}
}
