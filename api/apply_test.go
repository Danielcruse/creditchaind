package api

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// cctW converts a whole-CCT test amount into the smallest-unit decimal
// string cctStore actually stores (see core/cct_amount.go) - shared
// across every test file in this package so CCT seeding/assertions
// read the same way a plain whole-number test always has, without
// each call site hand-rolling the conversion.
func cctW(whole uint64) string {
	return core.FormatCctSmallest(core.CctWholeToSmallest(whole))
}

// cctWPlusFee adds the real CCT transfer fee (a sub-1-CCT smallest-unit
// amount, see api/apply.go's DefaultCCTTransferFeeSmallestUnit) on top
// of a whole-CCT amount - cctW alone can't express a fractional
// component, so tests that need "enough to cover amount + fee exactly"
// go through this instead of hand-rolling the big.Int math per call site.
func cctWPlusFee(whole uint64) string {
	total := new(big.Int).Add(core.CctWholeToSmallest(whole), big.NewInt(int64(DefaultCCTTransferFeeSmallestUnit)))
	return core.FormatCctSmallest(total)
}

// resetApplyTestState gives each test a clean slate for every
// package-level store applyTxOnce/ApplyTransaction touch for a plain
// CCT transfer - these are real singletons shared across the package,
// so tests that mutate them must not leak state into each other.
func resetApplyTestState() {
	// mempool is a plain package-level var (api/server.go), not owned by
	// any *Store this function already resets - without clearing it too,
	// a pending transaction left behind by one test's recordTx call (an
	// HTTP handler test, e.g. HandleApplyCompany/HandleSetHubRate) leaks
	// into any later test in the same process that asserts on
	// mempool.Transactions' exact contents, regardless of which package
	// state that later test itself resets.
	mempool = core.Mempool{}
	walletStore = core.NewWalletStore()
	tokenStore = core.NewTokenStore()
	cctStore = core.NewCctStore()
	treasuryStore = core.NewTreasuryStore()
	loanStore = core.NewLoanStore()
	loanOfferStore = core.NewLoanOfferStore()
	loanRequestStore = core.NewLoanRequestStore()
	negotiationStore = core.NewNegotiationStore()
	collateralStore = core.NewCollateralStore()
	stakingStore = core.NewStakingStore()
	proposalStore = core.NewProposalStore()
	cctEthTestPool = core.NewLiquidityPool("ETHTEST")
	cctUsdcTestPool = core.NewLiquidityPool("USDCTEST")
	yenStore = core.NewTokenStore()
	recoveryStore = core.NewRecoveryStore()
	bridgedEthStore = core.NewTokenStore()
	ethCustodialStore = core.NewEthCustodialStore()
	bridgedBtcStore = core.NewTokenStore()
	btcAddressStore = core.NewBtcAddressStore()
	validatorApplicationStore = core.NewValidatorApplicationStore()
	// selfSignedEnvelope/reporterAddress (used by node-authored
	// transactions like TxBridgeDeposit) sign against the package-level
	// nodeIdentity - zero-value by default in tests, which produces an
	// unverifiable signature. A real generated identity here lets tests
	// build genuinely-valid node-authored transactions the same way
	// production code does.
	nodeIdentity = core.NewNodeIdentity()
	appliedTx.mu.Lock()
	appliedTx.seen = make(map[string]bool)
	appliedTx.mu.Unlock()
	slashDedup.mu.Lock()
	slashDedup.seen = make(map[string]bool)
	slashDedup.mu.Unlock()
	resetMissedBlockReportersForTest()
	resetReputationMiningSweepForTest()
	resetLegacyStakeMigrationSweepForTest()
	adminMultisigStore = core.NewAdminMultisigStore()
	yeneClaimStore = core.NewYeneClaimStore()
	bridgeValidatorStore = core.NewBridgeValidatorStore()
	vaultLockAttestationStore = core.NewVaultLockAttestationStore()
	vaultReleaseApprovalStore = core.NewVaultReleaseApprovalStore()
	ethCcc1Store = core.NewDecimalTokenStore(18)
	usdcCcc1Store = core.NewDecimalTokenStore(6)
	stethCcc1Store = core.NewDecimalTokenStore(18)
	btcCcc1Store = core.NewTokenStore()
	// ethCcc1TransferAdapter/usdcCcc1TransferAdapter/stethCcc1TransferAdapter
	// (api/token.go) each capture a *pointer* to the store above at
	// package-init time - the reassignments just above swap
	// ethCcc1Store/usdcCcc1Store/stethCcc1Store to brand new instances,
	// but without rebuilding these adapters too, they'd keep wrapping
	// the ORIGINAL, now-orphaned pre-reset stores. Any code path that
	// resolves ETH_CCC1/USDC_CCC1/STETH_CCC1 through resolveTokenStore
	// (the wholeUnitStore interface, not the raw *core.DecimalTokenStore
	// directly) would then silently read/write stale state after a
	// reset - caught the hard way via applyBlockReward's
	// marketplace-fee-pool sweep test, which was the first test in this
	// suite to exercise that path post-reset. Also matters for
	// collateralEscrowAddress("STETH_CCC1") specifically - it's a fixed,
	// shared synthetic address, so without this reset, a leftover
	// balance from an earlier STETH_CCC1 collateral test would silently
	// pollute every later one's escrow-balance assertions.
	ethCcc1TransferAdapter = ccc1DecimalWholeUnitAdapter{store: ethCcc1Store}
	usdcCcc1TransferAdapter = ccc1DecimalWholeUnitAdapter{store: usdcCcc1Store}
	stethCcc1TransferAdapter = ccc1DecimalWholeUnitAdapter{store: stethCcc1Store}
	bridgeWithdrawReserved.mu.Lock()
	bridgeWithdrawReserved.wei = make(map[string]uint64)
	bridgeWithdrawReserved.mu.Unlock()
	// core.ActiveProtocolManifest is package-level mutable state (audit
	// findings C-06/C-09) - reset to its zero value (Set: false) so a
	// test that commits a manifest can never leak it into an unrelated
	// later test's admin/bridge-validator-threshold assertions.
	core.ActiveProtocolManifest = core.ProtocolManifest{}
}

// registerCurrentNodeAsAuthorizedValidator registers a fresh validator
// whose AuthorizedNodePublicKey matches this test's own nodeIdentity
// (reset fresh by resetApplyTestState just above) - required since
// audit finding C-01's fix: node-authored reports (bridge deposits,
// vault release completion, slash reports, tx-hash records) are only
// accepted from a signer isAuthorizedNodeIdentity recognizes, and a
// bare resetApplyTestState leaves validatorStore empty. Call this
// after resetApplyTestState whenever a test submits one of those
// report types via selfSignedEnvelope/reporterAddress.
func registerCurrentNodeAsAuthorizedValidator(t *testing.T) {
	t.Helper()
	validatorStore.Register("test-authorized-validator-"+reporterAddress(), "test-node", hex.EncodeToString(nodeIdentity.PublicKey))
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
	// CCT sends also carry a real fee on top of the amount (see
	// api/apply.go's applyTransfer/DefaultCCTTransferFeeSmallestUnit) -
	// minted here alongside the transfer amount so the sender can cover
	// both exactly.
	cctStore.Mint(from, cctWPlusFee(100))

	applyTxOnce(tx)

	if got := cctStore.BalanceOf(from); got != "0" {
		t.Fatalf("expected sender balance 0 after transfer (amount + fee both spent), got %s", got)
	}
	if got := cctStore.BalanceOf(to); got != cctW(100) {
		t.Fatalf("expected recipient balance 100, got %s", got)
	}
}

func TestApplyTxOnce_DuplicateIDAppliedOnce(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	tx, from := buildSignedTransferTx(t, to, 100, 1000)
	cctStore.Mint(from, cctW(200))

	applyTxOnce(tx)
	applyTxOnce(tx) // same tx.ID, replayed - must be a no-op the second time

	if got := cctStore.BalanceOf(to); got != cctW(100) {
		t.Fatalf("expected recipient balance 100 (transfer applied exactly once), got %s", got)
	}
	// 200 - 100 (amount) - the real fee (charged once, not twice by the
	// replay).
	want := new(big.Int).Sub(core.CctWholeToSmallest(200-100), big.NewInt(int64(DefaultCCTTransferFeeSmallestUnit)))
	if got := cctStore.BalanceOf(from); got != core.FormatCctSmallest(want) {
		t.Fatalf("expected sender to have %s left (amount + fee debited exactly once), got %s", core.FormatCctSmallest(want), got)
	}
}

func TestApplyTxOnce_TamperedSignatureRejected(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	tx, from := buildSignedTransferTx(t, to, 100, 1000)
	cctStore.Mint(from, cctW(100))

	// Flip the signature to something that won't verify - simulates a
	// corrupted or forged transaction reaching a node.
	tx.AuthSignature = "00" + tx.AuthSignature[2:]

	applyTxOnce(tx)

	if got := cctStore.BalanceOf(from); got != cctW(100) {
		t.Fatalf("expected balance untouched after a bad signature, got %s (sender should still have 100)", got)
	}
	if got := cctStore.BalanceOf(to); got != "0" {
		t.Fatalf("expected recipient to receive nothing from a rejected transaction, got %s", got)
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
	cctStore.Mint(from, cctW(999))

	applyTxOnce(tx)

	if got := cctStore.BalanceOf(to); got != "0" {
		t.Fatalf("expected the amount-mismatched transaction to be rejected entirely, recipient got %s", got)
	}
	if got := cctStore.BalanceOf(from); got != cctW(999) {
		t.Fatalf("expected sender's balance untouched, got %s", got)
	}
}

func TestApplyTxOnce_RecipientMismatchBetweenSignedAndClaimedRejected(t *testing.T) {
	resetApplyTestState()

	// Signed payload authorizes paying "recipient-address", but the
	// transaction's own To field was redirected to "attacker-address" -
	// must be rejected, not silently redirected.
	tx, from := buildSignedTransferTx(t, "recipient-address", 100, 1000)
	tx.To = "attacker-address"
	cctStore.Mint(from, cctW(100))

	applyTxOnce(tx)

	if got := cctStore.BalanceOf("attacker-address"); got != "0" {
		t.Fatalf("expected a redirected recipient to receive nothing, got %s", got)
	}
	if got := cctStore.BalanceOf(from); got != cctW(100) {
		t.Fatalf("expected sender's balance untouched, got %s", got)
	}
}

// buildSignedHDMigrateTx builds a genuinely-signed TxHDWalletMigrate,
// signed by the OLD keypair (oldPub/oldPriv) - the defining difference
// from TxRecoverWallet, whose signer is the new address instead. See
// core/transactions.go's TxHDWalletMigrate doc comment.
func buildSignedHDMigrateTx(t *testing.T, oldPub, oldPriv []byte, newAddress, newPublicKey string, ts int64) core.Transaction {
	t.Helper()

	oldAddress := core.GenerateAddress(oldPub)
	payload := hdWalletMigrateData{NewAddress: newAddress, NewPublicKey: newPublicKey}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	env := core.SignedEnvelope{
		Address:   oldAddress,
		PublicKey: hex.EncodeToString(oldPub),
		Timestamp: ts,
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(oldPriv, env.CanonicalMessage())

	return core.Transaction{
		ID:            "test-hd-migrate-" + oldAddress + "-" + newAddress,
		From:          oldAddress,
		To:            newAddress,
		Type:          core.TxHDWalletMigrate,
		Timestamp:     ts,
		Data:          string(payloadJSON),
		AuthAddress:   oldAddress,
		AuthPublicKey: hex.EncodeToString(oldPub),
		AuthTimestamp: ts,
		AuthPayload:   payloadJSON,
		AuthSignature: hex.EncodeToString(sig),
	}
}

// TestApplyHDWalletMigrate_ValidMigrationMovesAssetsAndFreezesOldAddress
// exercises the full path (applyTxOnce, not applyHDWalletMigrate
// directly) so verifyTxAuth is genuinely exercised too - confirms CCT,
// REPT, staked CCT, YENE, ETHTEST, USDCTEST, and an open loan's
// Borrower all move from the old address to the new one, and that the
// old address is frozen afterward (applyTxOnce's existing
// recoveryStore.IsMigrated guard, reused verbatim from the
// TxRecoverWallet flow, rejects any further transaction from it).
func TestApplyHDWalletMigrate_ValidMigrationMovesAssetsAndFreezesOldAddress(t *testing.T) {
	resetApplyTestState()

	oldPub, oldPriv, _ := core.GenerateKeyPair()
	oldAddress := core.GenerateAddress(oldPub)
	oldPubHex := hex.EncodeToString(oldPub)

	newPub, _, _ := core.GenerateKeyPair()
	newAddress := core.GenerateAddress(newPub)
	newPubHex := hex.EncodeToString(newPub)

	cctStore.Mint(oldAddress, cctW(100))
	reptStore.Mint(oldAddress, 5)
	stakingStore.Stakes[oldAddress] = map[string]uint64{"CCT": 30}
	walletStore.Get(oldAddress).Reputation = 77

	oldEthAddr := resolveBalanceAddress(oldAddress, oldPubHex, "ETHTEST")
	ethTestStore.Mint(oldEthAddr, 12)
	oldUsdcAddr := resolveBalanceAddress(oldAddress, oldPubHex, "USDCTEST")
	usdcTestStore.Mint(oldUsdcAddr, 34)
	oldYenAddr := resolveBalanceAddress(oldAddress, oldPubHex, "YENE")
	yenStore.Mint(oldYenAddr, 56)

	loan := &core.Loan{ID: "test-hd-migrate-loan", Borrower: oldAddress, Status: core.LoanFunded}
	loanStore.Loans[loan.ID] = loan

	tx := buildSignedHDMigrateTx(t, oldPub, oldPriv, newAddress, newPubHex, 1000)
	applyTxOnce(tx)

	if got := cctStore.BalanceOf(newAddress); got != cctW(100) {
		t.Fatalf("expected 100 CCT moved to new address, got %s", got)
	}
	if got := reptStore.BalanceOf(newAddress); got != 5 {
		t.Fatalf("expected 5 REPT moved to new address, got %d", got)
	}
	if got := stakingStore.Stakes[newAddress]["CCT"]; got != 30 {
		t.Fatalf("expected 30 staked CCT moved to new address, got %d", got)
	}
	if got := walletStore.Get(newAddress).Reputation; got != 77 {
		t.Fatalf("expected reputation 77 moved to new address, got %d", got)
	}
	if got := walletStore.Get(oldAddress).Reputation; got != 0 {
		t.Fatalf("expected old address reputation zeroed, got %d", got)
	}

	newEthAddr := resolveBalanceAddress(newAddress, newPubHex, "ETHTEST")
	if got := ethTestStore.BalanceOf(newEthAddr); got != 12 {
		t.Fatalf("expected 12 ETHTEST moved to new sub-address, got %d", got)
	}
	newUsdcAddr := resolveBalanceAddress(newAddress, newPubHex, "USDCTEST")
	if got := usdcTestStore.BalanceOf(newUsdcAddr); got != 34 {
		t.Fatalf("expected 34 USDCTEST moved to new sub-address, got %d", got)
	}
	newYenAddr := resolveBalanceAddress(newAddress, newPubHex, "YENE")
	if got := yenStore.BalanceOf(newYenAddr); got != 56 {
		t.Fatalf("expected 56 YENE moved to new sub-address, got %d", got)
	}

	if loan.Borrower != newAddress {
		t.Fatalf("expected loan.Borrower rewritten to new address, got %s", loan.Borrower)
	}

	if !recoveryStore.IsMigrated(oldAddress) {
		t.Fatalf("expected old address to be marked migrated/frozen")
	}

	// A follow-up transaction from the now-frozen old address must be
	// rejected by applyTxOnce's existing guard, not applied.
	followUp, _ := buildSignedTransferTx(t, "someone-else", 1, 2000)
	followUp.From = oldAddress
	followUp.AuthAddress = oldAddress
	cctStore.Mint(oldAddress, cctW(1+CCTTransferFee))
	applyTxOnce(followUp)
	if got := cctStore.BalanceOf("someone-else"); got != "0" {
		t.Fatalf("expected a transaction from the frozen old address to be rejected, but it applied")
	}
}

// TestApplyHDWalletMigrate_WrongKeySignatureRejected confirms a
// migration transaction signed by a key OTHER than the address being
// migrated is rejected by verifyTxAuth - nothing moves, and the old
// address is not marked migrated.
func TestApplyHDWalletMigrate_WrongKeySignatureRejected(t *testing.T) {
	resetApplyTestState()

	oldPub, _, _ := core.GenerateKeyPair()
	oldAddress := core.GenerateAddress(oldPub)
	_, wrongPriv, _ := core.GenerateKeyPair() // a different keypair entirely

	newPub, _, _ := core.GenerateKeyPair()
	newAddress := core.GenerateAddress(newPub)
	newPubHex := hex.EncodeToString(newPub)

	cctStore.Mint(oldAddress, cctW(100))

	// Signed with the wrong private key but claiming to be oldAddress -
	// buildSignedHDMigrateTx's own signature won't match oldPub.
	tx := buildSignedHDMigrateTx(t, oldPub, wrongPriv, newAddress, newPubHex, 1000)
	applyTxOnce(tx)

	if got := cctStore.BalanceOf(newAddress); got != "0" {
		t.Fatalf("expected nothing to move on a bad signature, got %s CCT at new address", got)
	}
	if got := cctStore.BalanceOf(oldAddress); got != cctW(100) {
		t.Fatalf("expected old address balance untouched, got %s", got)
	}
	if recoveryStore.IsMigrated(oldAddress) {
		t.Fatalf("expected old address NOT marked migrated after a rejected transaction")
	}
}

// TestApplyHDWalletMigrate_DoubleMigrationRejected confirms a second,
// otherwise-valid migration attempt from an already-migrated address
// is rejected (MarkMigrated's own atomicity, not just the
// applyTxOnce-level freeze guard) - protects against, e.g., two
// competing "new address" migrations both racing to claim the same
// old address.
func TestApplyHDWalletMigrate_DoubleMigrationRejected(t *testing.T) {
	resetApplyTestState()

	oldPub, oldPriv, _ := core.GenerateKeyPair()
	oldAddress := core.GenerateAddress(oldPub)

	firstNewPub, _, _ := core.GenerateKeyPair()
	firstNewAddress := core.GenerateAddress(firstNewPub)
	firstNewPubHex := hex.EncodeToString(firstNewPub)

	secondNewPub, _, _ := core.GenerateKeyPair()
	secondNewAddress := core.GenerateAddress(secondNewPub)
	secondNewPubHex := hex.EncodeToString(secondNewPub)

	cctStore.Mint(oldAddress, cctW(100))

	firstTx := buildSignedHDMigrateTx(t, oldPub, oldPriv, firstNewAddress, firstNewPubHex, 1000)
	applyTxOnce(firstTx)
	if got := cctStore.BalanceOf(firstNewAddress); got != cctW(100) {
		t.Fatalf("expected first migration to succeed and move 100 CCT, got %s", got)
	}

	// applyTxOnce's own guard (recoveryStore.IsMigrated(tx.AuthAddress))
	// would already reject this since it runs before verifyTxAuth is
	// even consulted for the second transaction's own logic - this
	// confirms that end-to-end.
	secondTx := buildSignedHDMigrateTx(t, oldPub, oldPriv, secondNewAddress, secondNewPubHex, 2000)
	applyTxOnce(secondTx)
	if got := cctStore.BalanceOf(secondNewAddress); got != "0" {
		t.Fatalf("expected second migration from an already-migrated address to be rejected, got %s CCT moved", got)
	}
}

func TestApplyBlock_DuplicateBlockAppliedOnce(t *testing.T) {
	resetApplyTestState()
	appliedBlock.mu.Lock()
	appliedBlock.seen = make(map[string]bool)
	appliedBlock.mu.Unlock()

	const to = "recipient-address"
	tx, from := buildSignedTransferTx(t, to, 50, 2000)
	cctStore.Mint(from, cctW(50+CCTTransferFee))

	block := core.Block{
		Index:        1,
		Hash:         "test-block-hash",
		Timestamp:    2000,
		Transactions: []core.Transaction{tx},
	}

	ApplyBlock(block)
	ApplyBlock(block) // same Hash, replayed - must be a no-op the second time

	if got := cctStore.BalanceOf(to); got != cctW(50) {
		t.Fatalf("expected recipient balance 50 (block applied exactly once), got %s", got)
	}
}

// TestProcessLoanDefaults_OverdueLoanForfeitsCollateralAndReputationToLender
// verifies the peer-funded loan marketplace's core economics: a
// borrower who misses their due date loses BOTH their collateral and
// reputation to the lender, who is left strictly better off - never
// just made whole, per core.ReputationDeltaLoanDefaultLenderGain's
// doc comment.
// TestProcessLoanDefaults_OverdueLoanForfeitsCollateralAndReputationToLender
// drives an overdue loan through the FULL Default Protection sequence
// (Warning -> MarginCall -> GracePeriod -> liquidate -> AwaitingFinalRedemption
// -> processFinalRedemptions) - a single processLoanDefaults call no
// longer instantly liquidates/defaults, unlike before this feature;
// each stage now requires its own dwell/deadline to actually pass (see
// core/loan_risk.go's constants and advanceLoanLifecycle's doc
// comment, api/apply.go).
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

	asOf := int64(2000) // well past DueAt
	processLoanDefaults(asOf)
	if loan.RiskStage != core.LoanRiskWarning {
		t.Fatalf("expected RiskStage WARNING on first overdue sweep, got %q", loan.RiskStage)
	}
	if loan.Status != core.LoanFunded {
		t.Fatalf("expected loan to remain FUNDED through the Warning stage, got %s", loan.Status)
	}

	asOf += core.LoanRiskWarningDwellSeconds + 1
	processLoanDefaults(asOf)
	if loan.RiskStage != core.LoanRiskMarginCall {
		t.Fatalf("expected RiskStage MARGIN_CALL after the warning dwell, got %q", loan.RiskStage)
	}

	asOf += core.LoanRiskMarginCallDwellSeconds + 1
	processLoanDefaults(asOf)
	if loan.RiskStage != core.LoanRiskGracePeriod {
		t.Fatalf("expected RiskStage GRACE_PERIOD after the margin call dwell, got %q", loan.RiskStage)
	}

	asOf = loan.RiskStageDeadline + 1
	processLoanDefaults(asOf)
	if loan.RiskStage != core.LoanRiskAwaitingFinalRedemption {
		t.Fatalf("expected RiskStage AWAITING_FINAL_REDEMPTION after the grace period expired, got %q", loan.RiskStage)
	}
	if got := stakingStore.Stakes[lender]["CCT"]; got != 20 {
		t.Fatalf("expected lender to receive the borrower's 20 staked CCT collateral, got %d", got)
	}
	// CCT collateral has no live price (collateralValueInYen always
	// returns ok=false for it), so liquidateLoan treats it as a full
	// shortfall - matches the pre-lifecycle-feature behavior exactly,
	// just reached later.
	if loan.LiquidatedShortfall != 50 {
		t.Fatalf("expected the full 50 shortfall (CCT has no live price), got %d", loan.LiquidatedShortfall)
	}

	asOf = loan.RiskStageDeadline + 1
	processFinalRedemptions(asOf)
	if loan.RiskStage != core.LoanRiskDefaulted {
		t.Fatalf("expected RiskStage DEFAULTED after the final redemption window expired, got %q", loan.RiskStage)
	}
	if loan.Status != core.LoanDefault {
		t.Fatalf("expected loan status DEFAULT, got %s", loan.Status)
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
	cctStore.Mint(p2pOfferEscrowAddress(id), cctW(amount))
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

	if got := cctStore.BalanceOf(buyer); got != cctW(amount) {
		t.Fatalf("expected buyer to receive %d CCT escrow, got %s", amount, got)
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

// TestApplyP2PModeratorResolve_RefundOpensFaultChoiceInsteadOfImmediatePenalty
// is the mirror case - release=false (the buyer's payment claim was
// false) refunds the seller immediately, but - since a refund collects
// no trade fee to pay the moderator from - no longer penalizes the
// buyer's reputation immediately either. Instead it opens a
// core.DisputeFaultChoice giving the buyer 72 hours to pay the
// moderator's would-be fee or accept the penalty; see
// api/dispute_fault_choice_test.go for the pay/decline/timeout paths
// this choice can resolve through.
func TestApplyP2PModeratorResolve_RefundOpensFaultChoiceInsteadOfImmediatePenalty(t *testing.T) {
	resetApplyTestState()
	p2pOfferStore = core.NewP2POfferStore()
	reputationEventStore = core.NewReputationEventStore()
	disputeFaultChoiceStore = core.NewDisputeFaultChoiceStore()

	const seller = "modresolve-test-seller-2"
	const buyer = "modresolve-test-buyer-2"
	const moderator = "modresolve-test-moderator-2"
	const amount = 100_000 // large enough that the fee choice's amount is meaningfully nonzero

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

	if got := cctStore.BalanceOf(seller); got != cctW(amount) {
		t.Fatalf("expected seller to be refunded %d CCT escrow, got %s", amount, got)
	}
	if got := walletStore.Get(buyer).Reputation; got != 50 {
		t.Fatalf("expected buyer reputation UNTOUCHED immediately after a refund resolution (no fee to pay the moderator from yet), got %d", got)
	}
	if got := walletStore.Get(seller).Reputation; got != 50 {
		t.Fatalf("expected seller reputation untouched at 50, got %d", got)
	}
	if _, ok := reputationEventStore.Get(tx.ID); ok {
		t.Fatal("expected no reputation event recorded yet - the choice hasn't been resolved")
	}

	choice, ok := disputeFaultChoiceStore.Get("test-offer-refund")
	if !ok {
		t.Fatal("expected a pending DisputeFaultChoice to have been created")
	}
	if choice.AtFaultAddress != buyer || choice.ResolvingModerator != moderator || choice.Symbol != "CCT" {
		t.Fatalf("expected the choice to target buyer, resolved by moderator, in CCT, got %+v", choice)
	}
	if choice.Status != core.DisputeFaultChoicePending {
		t.Fatalf("expected status PENDING, got %s", choice.Status)
	}
	wantFee := core.PlatformFeeAmount(amount) * 10 / 100
	if choice.FeeAmount != wantFee {
		t.Fatalf("expected fee amount %d (10%% of the would-be platform fee), got %d", wantFee, choice.FeeAmount)
	}
	if choice.Deadline != tx.Timestamp+core.DisputeFaultChoiceWindowSeconds {
		t.Fatalf("expected a 72-hour deadline from tx.Timestamp, got %d (created at %d)", choice.Deadline, tx.Timestamp)
	}
}

func TestApplyTreasuryPurchaseConfirm(t *testing.T) {
	resetApplyTestState()

	const treasuryAddr = "treasury-address"
	const buyer = "buyer-address"

	treasuryStore.SetFoundationAddress(treasuryAddr)
	cctStore.Mint(treasuryAddr, cctW(1000))

	treasuryStore.CreatePurchase(&core.PurchaseRequest{
		ID:           "purchase-1",
		BuyerAddress: buyer,
		CCTAmount:    100,
		Status:       core.PurchaseAwaitingPayment,
	})

	data, _ := json.Marshal(treasuryPurchaseConfirmData{PurchaseID: "purchase-1"})
	tx := core.Transaction{
		ID:        "confirm-tx-1",
		Type:      core.TxTreasuryPurchaseConfirm,
		Data:      string(data),
		Timestamp: 1000,
	}
	applyTreasuryPurchaseConfirm(tx)

	if got := cctStore.BalanceOf(buyer); got != cctW(100) {
		t.Fatalf("expected buyer balance 100, got %s", got)
	}
	if got := cctStore.BalanceOf(treasuryAddr); got != cctW(900) {
		t.Fatalf("expected treasury balance 900 after paying out 100, got %s", got)
	}

	purchase, ok := treasuryStore.GetPurchase("purchase-1")
	if !ok || purchase.Status != core.PurchaseConfirmed {
		t.Fatalf("expected purchase to be CONFIRMED, got %+v (ok=%v)", purchase, ok)
	}
}

func TestApplyTreasuryPurchaseConfirmInsufficientBalance(t *testing.T) {
	resetApplyTestState()

	const treasuryAddr = "treasury-address"
	const buyer = "buyer-address"

	treasuryStore.SetFoundationAddress(treasuryAddr)
	// Deliberately no mint - treasury balance is 0, less than the
	// requested 100 CCT.

	treasuryStore.CreatePurchase(&core.PurchaseRequest{
		ID:           "purchase-2",
		BuyerAddress: buyer,
		CCTAmount:    100,
		Status:       core.PurchaseAwaitingPayment,
	})

	data, _ := json.Marshal(treasuryPurchaseConfirmData{PurchaseID: "purchase-2"})
	tx := core.Transaction{
		ID:        "confirm-tx-2",
		Type:      core.TxTreasuryPurchaseConfirm,
		Data:      string(data),
		Timestamp: 1000,
	}
	applyTreasuryPurchaseConfirm(tx)

	if got := tokenStore.BalanceOf(buyer); got != 0 {
		t.Fatalf("expected buyer balance 0 (insufficient-balance transfer should silently no-op), got %d", got)
	}

	purchase, ok := treasuryStore.GetPurchase("purchase-2")
	if !ok || purchase.Status != core.PurchaseAwaitingPayment {
		t.Fatalf("expected purchase to remain AWAITING_PAYMENT (confirmation failed), got %+v (ok=%v)", purchase, ok)
	}
}

// TestApplyBorrow_DefaultsToYenE confirms a self-funded borrow (see
// api/apply.go's applyBorrow) mints into the borrower's YEN-e
// sub-address and denominates the resulting loan in YENE, not the old
// USDCTEST default - the Phase B currency migration.
func TestApplyBorrow_DefaultsToYenE(t *testing.T) {
	resetApplyTestState()

	const borrower = "borrow-yene-test-borrower"
	collateralStore.Add(borrower, "CCT", 100)

	pub, _, _ := core.GenerateKeyPair()
	pubHex := hex.EncodeToString(pub)

	tx := core.Transaction{
		ID:            "test-borrow-yene",
		To:            borrower,
		Amount:        50,
		Type:          core.TxBorrow,
		Timestamp:     1000,
		AuthPublicKey: pubHex,
	}

	applyBorrow(tx)

	var loan *core.Loan
	for _, l := range loanStore.Loans {
		if l.Borrower == borrower {
			loan = l
			break
		}
	}
	if loan == nil {
		t.Fatalf("expected a loan to be created for borrower")
	}
	if loan.Symbol != "YENE" {
		t.Fatalf("expected loan.Symbol YENE, got %s", loan.Symbol)
	}

	yenSide := resolveBalanceAddress(borrower, pubHex, "YENE")
	if got := yenStore.BalanceOf(yenSide); got != 50 {
		t.Fatalf("expected 50 YENE minted to the borrower's YENE sub-address, got %d", got)
	}
}

// TestCollateralValueInYen_KnownReserves confirms collateralValueInYen
// matches the same two-hop quote applySwapTokenToToken already uses
// (OETH -> CCT -> USDCTEST-equivalent), and that CCT collateral and
// empty/unseeded pools both correctly report ok=false rather than a
// fabricated value.
func TestCollateralValueInYen_KnownReserves(t *testing.T) {
	resetApplyTestState()

	cctEthTestPool.CCTReserve = core.FormatCctSmallest(core.CctWholeToSmallest(2_000_000))
	cctEthTestPool.TokenReserve = 1_000_000
	cctUsdcTestPool.CCTReserve = core.FormatCctSmallest(core.CctWholeToSmallest(1_000_000))
	cctUsdcTestPool.TokenReserve = 1_000_000

	value, ok := collateralValueInYen("OETH", 1000, 1000)
	if !ok {
		t.Fatalf("expected a live price with seeded reserves")
	}

	quotedCCT, quoteOK := cctEthTestPool.QuoteTokenForCCT(1000)
	if !quoteOK {
		t.Fatalf("test setup invalid: expected the ETHTEST leg to quote successfully")
	}
	wantValue, quoteOK := cctUsdcTestPool.QuoteCCTForToken(quotedCCT)
	if !quoteOK {
		t.Fatalf("test setup invalid: expected the USDCTEST leg to quote successfully")
	}
	if value != wantValue {
		t.Fatalf("expected collateralValueInYen to match the same two-hop quote (%d), got %d", wantValue, value)
	}

	if _, ok := collateralValueInYen("CCT", 1000, 1000); ok {
		t.Fatalf("expected CCT collateral to never report a live price (CCT is the pricing numeraire)")
	}

	// A pool that was never seeded (zero reserves, as cctYenPool
	// deliberately stays in Phase A) must also report ok=false, not a
	// fabricated zero/infinite value.
	cctEthTestPool = core.NewLiquidityPool("ETHTEST")
	if _, ok := collateralValueInYen("OETH", 1000, 1000); ok {
		t.Fatalf("expected no live price against an unseeded (zero-reserve) pool")
	}
}

// TestLiquidateLoan_FullCoverageRefundsExcess covers liquidateLoan's
// "collateral value >= owed" branch: only a proportional slice of
// collateral is seized to the lender, the rest is refunded to the
// borrower, shortfall is 0, and the loan is marked REPAID (not
// DEFAULT) since the lender was made whole exactly as on a normal
// repayment.
func TestLiquidateLoan_FullCoverageRefundsExcess(t *testing.T) {
	resetApplyTestState()

	const borrower = "liquidate-full-borrower"
	const lender = "liquidate-full-lender"

	cctEthTestPool.CCTReserve = core.FormatCctSmallest(core.CctWholeToSmallest(2_000_000))
	cctEthTestPool.TokenReserve = 1_000_000
	cctUsdcTestPool.CCTReserve = core.FormatCctSmallest(core.CctWholeToSmallest(1_000_000))
	cctUsdcTestPool.TokenReserve = 1_000_000

	loan := &core.Loan{
		ID:               "test-liquidate-full",
		Borrower:         borrower,
		Lender:           lender,
		Symbol:           "YENE",
		Amount:           100,
		Status:           core.LoanFunded,
		CollateralSymbol: "OETH",
		CollateralAmount: 1000,
	}
	loanStore.Loans[loan.ID] = loan

	collateralValue, ok := collateralValueInYen("OETH", 1000, 1000)
	if !ok || collateralValue < 100 {
		t.Fatalf("test setup invalid: expected a live price >= the 100 owed, got %d (ok=%v)", collateralValue, ok)
	}
	wantSeize := 1000 * 100 / collateralValue
	wantRefund := 1000 - wantSeize

	shortfall := liquidateLoan(loan, 1000, "test")

	if shortfall != 0 {
		t.Fatalf("expected shortfall 0 (collateral covers the debt), got %d", shortfall)
	}
	if loan.Status != core.LoanRepaid {
		t.Fatalf("expected loan status REPAID, got %s", loan.Status)
	}
	if loan.CollateralAmount != 0 {
		t.Fatalf("expected loan.CollateralAmount fully disposed (0), got %d", loan.CollateralAmount)
	}
	if got := walletStore.Get(lender).OETHBalance; got != wantSeize {
		t.Fatalf("expected lender to receive %d OETH (proportional seize), got %d", wantSeize, got)
	}
	if got := walletStore.Get(borrower).OETHBalance; got != wantRefund {
		t.Fatalf("expected borrower to be refunded %d OETH (leftover collateral), got %d", wantRefund, got)
	}
}

// TestLiquidateLoan_InsufficientCollateralComputesShortfall covers the
// opposite branch: collateral value falls short of what's owed, so
// everything is seized to the lender and the shortfall is exactly
// what liquidation failed to recover - loan.Status is left untouched
// (the caller, e.g. processLoanDefaults, is responsible for flipping
// it to DEFAULT once it sees a nonzero shortfall).
func TestLiquidateLoan_InsufficientCollateralComputesShortfall(t *testing.T) {
	resetApplyTestState()

	const borrower = "liquidate-short-borrower"
	const lender = "liquidate-short-lender"

	cctEthTestPool.CCTReserve = core.FormatCctSmallest(core.CctWholeToSmallest(2_000_000))
	cctEthTestPool.TokenReserve = 1_000_000
	cctUsdcTestPool.CCTReserve = core.FormatCctSmallest(core.CctWholeToSmallest(1_000_000))
	cctUsdcTestPool.TokenReserve = 1_000_000

	loan := &core.Loan{
		ID:               "test-liquidate-short",
		Borrower:         borrower,
		Lender:           lender,
		Symbol:           "YENE",
		Amount:           100000, // deliberately far more than the thin collateral can cover
		Status:           core.LoanFunded,
		CollateralSymbol: "OETH",
		CollateralAmount: 10,
	}
	loanStore.Loans[loan.ID] = loan

	collateralValue, ok := collateralValueInYen("OETH", 10, 1000)
	if !ok || collateralValue >= 100000 {
		t.Fatalf("test setup invalid: expected a live price well under the 100000 owed, got %d (ok=%v)", collateralValue, ok)
	}
	wantShortfall := 100000 - collateralValue

	shortfall := liquidateLoan(loan, 1000, "test")

	if shortfall != wantShortfall {
		t.Fatalf("expected shortfall %d, got %d", wantShortfall, shortfall)
	}
	if loan.Status != core.LoanFunded {
		t.Fatalf("expected liquidateLoan to leave status untouched (caller flips to DEFAULT), got %s", loan.Status)
	}
	if loan.CollateralAmount != 0 {
		t.Fatalf("expected all collateral seized, got %d left", loan.CollateralAmount)
	}
	if got := walletStore.Get(lender).OETHBalance; got != 10 {
		t.Fatalf("expected lender to receive all 10 OETH collateral, got %d", got)
	}
	if got := walletStore.Get(borrower).OETHBalance; got != 0 {
		t.Fatalf("expected borrower to receive no refund, got %d", got)
	}
}

// setupLoanRequestFulfillTest builds a real OPEN LoanRequest plus a
// matching EXECUTED Negotiation ready to be fulfilled - the same
// state HandleStartNegotiation+HandleConfirmNegotiation would have
// produced by the time applyLoanRequestFulfill runs for real.
func setupLoanRequestFulfillTest(t *testing.T, ratePercent, maxInterestPercent uint64) (tx core.Transaction, lender, borrower, lenderPubHex, borrowerPubHex string) {
	t.Helper()

	borrowerPub, _, _ := core.GenerateKeyPair()
	borrowerPubHex = hex.EncodeToString(borrowerPub)
	borrower = core.GenerateAddress(borrowerPub)

	lenderPub, _, _ := core.GenerateKeyPair()
	lenderPubHex = hex.EncodeToString(lenderPub)
	lender = core.GenerateAddress(lenderPub)

	lenderYenSide := resolveBalanceAddress(lender, lenderPubHex, "YENE")
	yenStore.Mint(lenderYenSide, 1000)

	loanRequestStore.Create(&core.LoanRequest{
		ID: "req-1", Borrower: borrower, BorrowerPublicKey: borrowerPubHex,
		RequestedSymbol: "YENE", Amount: 1000,
		CollateralSymbol: "OETH", CollateralAmount: 1500,
		MaxInterestPercent: maxInterestPercent, Status: core.LoanRequestOpen, CreatedAt: 1,
	})

	negotiationStore.Create(&core.Negotiation{
		ID: "neg-1", RequestID: "req-1", Lender: lender, Borrower: borrower,
		Symbol: "YENE", Amount: 1000, RatePercent: ratePercent,
		Status: core.NegotiationOpen, CreatedAt: 1,
	})
	negotiationStore.Execute("neg-1")

	data, _ := json.Marshal(negotiationActionData{NegotiationID: "neg-1"})
	tx = core.Transaction{
		ID: "tx-fulfill-1", From: lender, Type: core.TxLoanRequestFulfill,
		Data: string(data), Timestamp: 1000, AuthPublicKey: lenderPubHex,
	}
	return tx, lender, borrower, lenderPubHex, borrowerPubHex
}

// TestApplyLoanRequestFulfill_HappyPath confirms the lender-initiated
// mirror of applyLoanOfferDraw actually moves funds, marks the
// request FULFILLED, and creates a Loan with the negotiated rate and
// the borrower's originally-reserved collateral.
func TestApplyLoanRequestFulfill_HappyPath(t *testing.T) {
	resetApplyTestState()

	tx, lender, borrower, lenderPubHex, borrowerPubHex := setupLoanRequestFulfillTest(t, 8, 10)
	applyLoanRequestFulfill(tx)

	borrowerYenSide := resolveBalanceAddress(borrower, borrowerPubHex, "YENE")
	lenderYenSide := resolveBalanceAddress(lender, lenderPubHex, "YENE")
	if got := yenStore.BalanceOf(borrowerYenSide); got != 1000 {
		t.Fatalf("expected borrower to receive 1000 YENE, got %d", got)
	}
	if got := yenStore.BalanceOf(lenderYenSide); got != 0 {
		t.Fatalf("expected lender's YENE balance fully drawn down, got %d", got)
	}

	req, ok := loanRequestStore.Get("req-1")
	if !ok || req.Status != core.LoanRequestFulfilled {
		t.Fatalf("expected request status FULFILLED, got %+v (ok=%v)", req, ok)
	}

	var loan *core.Loan
	for _, l := range loanStore.Loans {
		if l.Borrower == borrower {
			loan = l
		}
	}
	if loan == nil {
		t.Fatalf("expected a Loan to be created for the borrower")
	}
	if loan.Lender != lender || loan.Interest != 8 || loan.CollateralSymbol != "OETH" || loan.CollateralAmount != 1500 {
		t.Fatalf("unexpected loan fields: %+v", loan)
	}
}

// TestApplyLoanRequestFulfill_RejectsRateAboveMax confirms a
// negotiated rate above the borrower's stated MaxInterestPercent is
// rejected even if it somehow reached apply time (defense-in-depth -
// HandleStartNegotiation already blocks this at submission time).
func TestApplyLoanRequestFulfill_RejectsRateAboveMax(t *testing.T) {
	resetApplyTestState()

	tx, _, borrower, _, borrowerPubHex := setupLoanRequestFulfillTest(t, 15, 10) // 15% > 10% max
	applyLoanRequestFulfill(tx)

	req, _ := loanRequestStore.Get("req-1")
	if req.Status != core.LoanRequestOpen {
		t.Fatalf("expected request to remain OPEN when the rate exceeds the max, got %s", req.Status)
	}
	borrowerYenSide := resolveBalanceAddress(borrower, borrowerPubHex, "YENE")
	if got := yenStore.BalanceOf(borrowerYenSide); got != 0 {
		t.Fatalf("expected no funds moved when the rate exceeds the max, got %d", got)
	}
}

// TestApplyLoanRequestFulfill_RejectsDoubleFulfill confirms applying
// the same fulfillment twice only ever moves funds once -
// LoanRequestStore.Fulfill's own atomicity is what prevents the
// second call from doing anything, not a separate negotiation-status
// check.
func TestApplyLoanRequestFulfill_RejectsDoubleFulfill(t *testing.T) {
	resetApplyTestState()

	tx, _, borrower, _, borrowerPubHex := setupLoanRequestFulfillTest(t, 8, 10)
	applyLoanRequestFulfill(tx)
	applyLoanRequestFulfill(tx) // replay

	borrowerYenSide := resolveBalanceAddress(borrower, borrowerPubHex, "YENE")
	if got := yenStore.BalanceOf(borrowerYenSide); got != 1000 {
		t.Fatalf("expected exactly 1000 YENE moved (not double), got %d", got)
	}

	loanCount := 0
	for _, l := range loanStore.Loans {
		if l.Borrower == borrower {
			loanCount++
		}
	}
	if loanCount != 1 {
		t.Fatalf("expected exactly 1 loan created, got %d", loanCount)
	}
}

// TestLenderPortfolio_InterestEarnedComputedAtRepayment confirms
// GET /lender/portfolio's InterestEarned is the actual repaid
// interest currency amount (repayAmountFor - principal for REPAID
// loans only), not core.LenderStats.InterestEarned's stale rate-at-
// fund-time figure (see audit/KNOWN_GAPS.md).
func TestLenderPortfolio_InterestEarnedComputedAtRepayment(t *testing.T) {
	resetApplyTestState()

	const lender = "portfolio-test-lender"
	loanStore.Loans["loan-repaid"] = &core.Loan{
		ID: "loan-repaid", Borrower: "b1", Lender: lender,
		Amount: 1000, Interest: 10, Status: core.LoanRepaid,
	}
	loanStore.Loans["loan-active"] = &core.Loan{
		ID: "loan-active", Borrower: "b2", Lender: lender,
		Amount: 500, Interest: 20, Status: core.LoanFunded,
	}
	loanStore.Loans["loan-defaulted"] = &core.Loan{
		ID: "loan-defaulted", Borrower: "b3", Lender: lender,
		Amount: 200, Interest: 5, Status: core.LoanDefault,
	}

	req := httptest.NewRequest(http.MethodGet, "/lender/portfolio?address="+lender, nil)
	rec := httptest.NewRecorder()
	HandleLenderPortfolio(rec, req)

	var resp lenderPortfolioResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}

	// repayAmountFor(loan-repaid) = 1000 + 1000*10/100 = 1100, so
	// interest actually collected = 1100 - 1000 = 100.
	if resp.InterestEarned != 100 {
		t.Fatalf("expected InterestEarned 100 (from the REPAID loan only), got %d", resp.InterestEarned)
	}
	if resp.ActiveLoans != 1 || resp.CompletedLoans != 1 || resp.DefaultedLoans != 1 {
		t.Fatalf("unexpected loan counts: %+v", resp)
	}
	if resp.DefaultExposure != 200 {
		t.Fatalf("expected DefaultExposure 200, got %d", resp.DefaultExposure)
	}
	if resp.Diversification != 3 {
		t.Fatalf("expected Diversification 3 (three distinct borrowers), got %d", resp.Diversification)
	}
}

// TestProtocolStats_AverageReputationScopedToParticipants confirms
// GET /protocol/stats's AverageReputation only counts wallets that
// have actually borrowed or lent, not every registered wallet
// (which would dilute the figure with brand-new wallets sitting at
// the default Reputation=50 with zero lending activity).
func TestProtocolStats_AverageReputationScopedToParticipants(t *testing.T) {
	resetApplyTestState()

	borrower := walletStore.Get("stats-borrower")
	borrower.Reputation = 80
	lender := walletStore.Get("stats-lender")
	lender.Reputation = 60
	// A wallet that never borrowed or lent - must not affect the average.
	_ = walletStore.Get("stats-uninvolved")

	loanStore.Loans["loan-1"] = &core.Loan{
		ID: "loan-1", Borrower: "stats-borrower", Lender: "stats-lender",
		Amount: 1000, Interest: 10, Status: core.LoanFunded,
	}

	req := httptest.NewRequest(http.MethodGet, "/protocol/stats", nil)
	rec := httptest.NewRecorder()
	HandleProtocolStats(rec, req)

	var resp protocolStatsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}

	if resp.AverageRepute != 70 { // (80+60)/2, uninvolved wallet excluded
		t.Fatalf("expected AverageReputation 70 (scoped to the 2 participants), got %d", resp.AverageRepute)
	}
	if resp.ActiveLoans != 1 || resp.TotalBorrowed != 1000 {
		t.Fatalf("unexpected loan aggregation: %+v", resp)
	}
}
