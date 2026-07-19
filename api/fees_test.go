package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func TestApplyTransfer_CCTSendDeductsAmountPlusFeeIntoPool(t *testing.T) {
	resetApplyTestState()

	const from = "fee-test-sender"
	const to = "fee-test-recipient"
	tokenStore.Mint(from, 100)

	tx := core.Transaction{
		ID:     "test-cct-send",
		From:   from,
		To:     to,
		Amount: 20,
		Type:   core.TxExchange,
		Data:   `{"to":"` + to + `","amount":20,"symbol":"CCT"}`,
	}
	applyTransfer(tx)

	if got := tokenStore.BalanceOf(from); got != 100-20-CCTTransferFee {
		t.Fatalf("expected sender balance %d (100-20-fee), got %d", 100-20-CCTTransferFee, got)
	}
	if got := tokenStore.BalanceOf(to); got != 20 {
		t.Fatalf("expected recipient to receive 20, got %d", got)
	}
	if got := tokenStore.BalanceOf(cctTransferFeePoolAddress()); got != CCTTransferFee {
		t.Fatalf("expected fee pool to hold %d, got %d", CCTTransferFee, got)
	}
}

func TestApplyTransfer_CCTSendInsufficientForFeeMovesNothing(t *testing.T) {
	resetApplyTestState()

	const from = "fee-test-sender-2"
	const to = "fee-test-recipient-2"
	// Exactly enough for the amount but not the fee on top of it.
	tokenStore.Mint(from, 20)

	tx := core.Transaction{
		ID:     "test-cct-send-2",
		From:   from,
		To:     to,
		Amount: 20,
		Type:   core.TxExchange,
		Data:   `{"to":"` + to + `","amount":20,"symbol":"CCT"}`,
	}
	applyTransfer(tx)

	if got := tokenStore.BalanceOf(from); got != 20 {
		t.Fatalf("expected sender balance untouched at 20, got %d", got)
	}
	if got := tokenStore.BalanceOf(to); got != 0 {
		t.Fatalf("expected recipient to receive nothing, got %d", got)
	}
}

func TestApplyTransfer_NonCCTSendCarriesNoFee(t *testing.T) {
	resetApplyTestState()

	const from = "fee-test-sender-3"
	const to = "fee-test-recipient-3"
	// ETHTEST lives at a derived sub-address, not the raw wallet
	// address (see resolveBalanceAddress) - mint to the exact address
	// applyTransfer will resolve to, same as a real ETHTEST balance
	// would already sit there.
	fromSide := resolveBalanceAddress(from, "", "ETHTEST")
	ethTestStore.Mint(fromSide, 20)

	tx := core.Transaction{
		ID:     "test-ethtest-send",
		From:   from,
		To:     to,
		Amount: 20,
		Type:   core.TxExchange,
		Data:   `{"to":"` + to + `","amount":20,"symbol":"ETHTEST"}`,
	}
	applyTransfer(tx)

	if got := ethTestStore.BalanceOf(fromSide); got != 0 {
		t.Fatalf("expected ETHTEST sender balance 0 (no fee deducted, full amount sent), got %d", got)
	}
	if got := ethTestStore.BalanceOf(to); got != 20 {
		t.Fatalf("expected recipient to receive the full 20 ETHTEST, got %d", got)
	}
}

func TestApplyBlockReward_SweepsCollectedFeesToValidator(t *testing.T) {
	resetApplyTestState()

	const validator = "fee-test-validator"
	tokenStore.Mint(cctTransferFeePoolAddress(), 7)

	block := core.Block{Validator: validator}
	applyBlockReward(block)

	if got := tokenStore.BalanceOf(validator); got != ProduceBlockReward+7 {
		t.Fatalf("expected validator to receive block reward (%d) plus swept fees (7) = %d, got %d", ProduceBlockReward, ProduceBlockReward+7, got)
	}
	if got := tokenStore.BalanceOf(cctTransferFeePoolAddress()); got != 0 {
		t.Fatalf("expected fee pool drained to 0 after sweep, got %d", got)
	}
}

func TestApplyCompanyApply_DeductsFeeAndBlocksWhenInsufficient(t *testing.T) {
	resetApplyTestState()
	companyStore = core.NewCompanyStore()
	moderatorStore = core.NewModeratorStore()

	const applicant = "fee-test-hub-applicant"
	tokenStore.Mint(applicant, HubApplyFeeCCT)

	tx := core.Transaction{
		ID:   "test-hub-apply",
		From: applicant,
		Type: core.TxCompanyApply,
		Data: `{"name":"Test Hub"}`,
	}
	applyCompanyApply(tx)

	if got := tokenStore.BalanceOf(applicant); got != 0 {
		t.Fatalf("expected applicant balance drained by the fee, got %d", got)
	}
	if got := tokenStore.BalanceOf(registrationFeeAddress()); got != HubApplyFeeCCT {
		t.Fatalf("expected registration fee address to hold %d, got %d", HubApplyFeeCCT, got)
	}
	if _, ok := companyStore.Get(applicant); !ok {
		t.Fatalf("expected a company application to be recorded")
	}

	// A second applicant with insufficient CCT should not get an
	// application recorded, and the fee address should be untouched by
	// the failed attempt.
	const poorApplicant = "fee-test-hub-applicant-poor"
	tx2 := core.Transaction{
		ID:   "test-hub-apply-poor",
		From: poorApplicant,
		Type: core.TxCompanyApply,
		Data: `{"name":"Poor Hub"}`,
	}
	applyCompanyApply(tx2)
	if _, ok := companyStore.Get(poorApplicant); ok {
		t.Fatalf("expected no application recorded for an applicant who couldn't cover the fee")
	}
}

func TestApplyModeratorRegister_DeductsFeeAndBlocksWhenInsufficient(t *testing.T) {
	resetApplyTestState()
	moderatorStore = core.NewModeratorStore()

	const addr = "fee-test-moderator"
	walletStore.Get(addr).Reputation = 50
	tokenStore.Mint(addr, ModeratorRegisterFeeCCT)

	tx := core.Transaction{ID: "test-mod-register", From: addr, Type: core.TxModeratorRegister, Timestamp: 1000}
	applyModeratorRegister(tx)

	if got := tokenStore.BalanceOf(addr); got != 0 {
		t.Fatalf("expected moderator's balance drained by the fee, got %d", got)
	}
	if !moderatorStore.IsModerator(addr) {
		t.Fatalf("expected address to be registered as a moderator")
	}

	const poorAddr = "fee-test-moderator-poor"
	walletStore.Get(poorAddr).Reputation = 50
	tx2 := core.Transaction{ID: "test-mod-register-poor", From: poorAddr, Type: core.TxModeratorRegister, Timestamp: 1000}
	applyModeratorRegister(tx2)
	if moderatorStore.IsModerator(poorAddr) {
		t.Fatalf("expected registration to fail for an address that couldn't cover the fee")
	}
}

func TestApplyValidatorRegister_DeductsFeeAndBlocksWhenInsufficient(t *testing.T) {
	resetApplyTestState()
	validatorStore = core.NewValidatorStore()

	const addr = "fee-test-validator-applicant"
	w := walletStore.Get(addr)
	w.Reputation = 50
	// applyValidatorRegister calls stakingStore.SyncWallet, which
	// overwrites wallet.StakedCCT from the staking ledger - setting it
	// directly on the wallet would just get clobbered back to 0. CCT
	// specifically (not ETHTEST) - PoSR gates validator eligibility on
	// CCT stake, see api/apply.go's applyValidatorRegister.
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": 2000}
	tokenStore.Mint(addr, ValidatorRegisterFeeCCT)

	tx := core.Transaction{
		ID:   "test-validator-register",
		From: addr,
		Type: core.TxValidatorRegister,
		Data: `{"nodeId":"node-1","nodePublicKey":"abc"}`,
	}
	applyValidatorRegister(tx)

	if got := tokenStore.BalanceOf(addr); got != 0 {
		t.Fatalf("expected validator applicant's balance drained by the fee, got %d", got)
	}
	if _, ok := validatorStore.Get(addr); !ok {
		t.Fatalf("expected address to be registered as a validator")
	}

	const poorAddr = "fee-test-validator-applicant-poor"
	pw := walletStore.Get(poorAddr)
	pw.Reputation = 50
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
