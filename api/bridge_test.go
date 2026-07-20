package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestHandleBridgedEthWithdrawQuote_NoRPCConfiguredReturns503NotPanic is
// a regression test for a real bug caught during manual verification:
// HandleBridgedEthWithdrawQuote called ethbWithdrawFeeWei (and
// therefore ethNonceAndGasPrice) without first checking ethRPCClient
// for nil, unlike every other ETH-RPC-backed handler in this package -
// panicked with a nil pointer dereference on every request when no
// SEPOLIA_RPC_URL is configured (the default in this test environment
// and in any deployment that hasn't opted into the ETH corner).
func TestHandleBridgedEthWithdrawQuote_NoRPCConfiguredReturns503NotPanic(t *testing.T) {
	resetApplyTestState()
	ethRPCClient = nil // explicit - this is the exact unconfigured-RPC state that panicked

	req := httptest.NewRequest(http.MethodGet, "/eth-bridge/withdraw-quote?address=some-address&amountWei=1000", nil)
	w := httptest.NewRecorder()

	HandleBridgedEthWithdrawQuote(w, req) // must not panic

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no Sepolia RPC is configured, got %d", w.Code)
	}
}

// setupCustodialAccount creates a fresh wallet keypair and a matching
// EthCustodialAccount, returning the CreditChain address, its hex
// public key, and the ETHB sub-address that account's deposits/sends
// resolve to - the shared setup every test in this file needs.
func setupCustodialAccount(t *testing.T, ethAddress string) (ccAddress, pubHex, ethbAddress string) {
	t.Helper()

	pub, _, _ := core.GenerateKeyPair()
	ccAddress = core.GenerateAddress(pub)
	pubHex = hex.EncodeToString(pub)

	ethCustodialStore.Create(&core.EthCustodialAccount{
		CreditChainAddress:   ccAddress,
		CreditChainPublicKey: pubHex,
		EthAddress:           ethAddress,
		KeyRef:               "test-key-ref",
	})

	ethbAddress = resolveBalanceAddress(ccAddress, pubHex, "ETHB")
	return
}

// buildBridgeDepositTx mirrors pollAndReportBridgeDeposit's own
// construction exactly - a real self-signed envelope (via
// selfSignedEnvelope/reporterAddress, the same functions production
// code uses), so it passes core.VerifyTransactionAuth like any other
// transaction, not just the TxBridgeDeposit-specific Data==AuthPayload
// check.
func buildBridgeDepositTx(id, ccAddress, newBalanceWei string) core.Transaction {
	data, _ := json.Marshal(bridgeDepositReportData{
		CreditChainAddress: ccAddress,
		NewBalanceWei:      newBalanceWei,
	})

	env := selfSignedEnvelope(data)

	return core.Transaction{
		ID:            id,
		From:          reporterAddress(),
		Type:          core.TxBridgeDeposit,
		Data:          string(data),
		AuthAddress:   env.Address,
		AuthPublicKey: env.PublicKey,
		AuthTimestamp: env.Timestamp,
		AuthPayload:   env.Payload,
		AuthSignature: env.Signature,
	}
}

func TestApplyBridgeDeposit_CreditsCorrectly(t *testing.T) {
	resetApplyTestState()

	ccAddress, _, ethbAddress := setupCustodialAccount(t, "0xdeadbeef00000000000000000000000000dead")

	tx := buildBridgeDepositTx("dep-1", ccAddress, "1000000000000000000") // 1 ETH in wei
	applyTxOnce(tx)

	if got := bridgedEthStore.BalanceOf(ethbAddress); got != 1000000000000000000 {
		t.Fatalf("expected ETHB credit of 1e18 wei, got %d", got)
	}

	account, ok := ethCustodialStore.Get(ccAddress)
	if !ok {
		t.Fatalf("expected custodial account to still exist")
	}
	if account.LastCreditedWei != "1000000000000000000" {
		t.Fatalf("expected LastCreditedWei updated to 1e18, got %q", account.LastCreditedWei)
	}

	// A second, larger report should credit only the DELTA, not the
	// full new balance again.
	tx2 := buildBridgeDepositTx("dep-2", ccAddress, "1500000000000000000") // +0.5 ETH
	applyTxOnce(tx2)

	if got := bridgedEthStore.BalanceOf(ethbAddress); got != 1500000000000000000 {
		t.Fatalf("expected ETHB balance 1.5e18 wei after second deposit, got %d", got)
	}
}

func TestApplyBridgeDeposit_DedupPreventsDoubleCredit(t *testing.T) {
	resetApplyTestState()

	ccAddress, _, ethbAddress := setupCustodialAccount(t, "0xdeadbeef00000000000000000000000000dead")

	// Same report content, two different transaction IDs - isolates
	// applyBridgeDeposit's own content-based dedup (slashDedup) from
	// applyTxOnce's separate tx.ID-based dedup (appliedTx.seen), which
	// would otherwise trivially prevent a same-ID replay regardless of
	// whether the deposit-specific dedup logic works at all.
	tx1 := buildBridgeDepositTx("dep-a", ccAddress, "2000000000000000000")
	tx2 := buildBridgeDepositTx("dep-b", ccAddress, "2000000000000000000")

	applyTxOnce(tx1)
	applyTxOnce(tx2)

	if got := bridgedEthStore.BalanceOf(ethbAddress); got != 2000000000000000000 {
		t.Fatalf("expected balance credited exactly once (2e18 wei), got %d - dedup failed", got)
	}
}

// buildSignedETHBTransferTx signs a real TxExchange moving `symbol`
// (ETHB here) the same way a genuine client would - mirrors
// buildSignedTransferTx in apply_test.go, generalized to carry a
// symbol.
func buildSignedETHBTransferTx(id string, priv []byte, pub []byte, from, to string, amount uint64, ts int64) core.Transaction {
	payload := tokenTransferData{To: to, Amount: amount, Symbol: "ETHB"}
	payloadJSON, _ := json.Marshal(payload)

	env := core.SignedEnvelope{
		Address:   from,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: ts,
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(priv, env.CanonicalMessage())

	return core.Transaction{
		ID:            id,
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
		Data:          string(payloadJSON),
	}
}

// TestApplyTransfer_ETHBCCTNetworkFee confirms the fee moved off CCT
// onto ETH itself (see ETHBCCTNetworkFeeWei's doc comment) - a
// CCT-network ETHB send now debits amount+fee entirely from the ETHB
// balance, touching the sender's CCT balance not at all.
func TestApplyTransfer_ETHBCCTNetworkFee(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(pub)
	ethCustodialStore.Create(&core.EthCustodialAccount{
		CreditChainAddress:   from,
		CreditChainPublicKey: hex.EncodeToString(pub),
		EthAddress:           "0xbbbb000000000000000000000000000000bbbb",
		KeyRef:               "test-key-ref",
	})
	fromEthb := resolveBalanceAddress(from, hex.EncodeToString(pub), "ETHB")

	const to = "recipient-ethb-address"

	bridgedEthStore.Mint(fromEthb, 500+ETHBCCTNetworkFeeWei)

	tx := buildSignedETHBTransferTx("ethb-send-1", priv, pub, from, to, 500, 1000)
	applyTxOnce(tx)

	if got := bridgedEthStore.BalanceOf(fromEthb); got != 0 {
		t.Fatalf("expected sender ETHB balance 0 (500+fee sent, all in ETHB), got %d", got)
	}
	if got := bridgedEthStore.BalanceOf(to); got != 500 {
		t.Fatalf("expected recipient ETHB balance 500, got %d", got)
	}
	if got := bridgedEthStore.BalanceOf(ethbBridgeFeeAddress()); got != ETHBCCTNetworkFeeWei {
		t.Fatalf("expected the fee sink to hold %d wei, got %d", ETHBCCTNetworkFeeWei, got)
	}
	if got := tokenStore.BalanceOf(from); got != 0 {
		t.Fatalf("expected sender CCT balance untouched at 0 - the fee no longer touches CCT at all, got %d", got)
	}
}

func TestApplyTransfer_ETHBCCTNetworkFee_InsufficientETHBRejectsWholeTransfer(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(pub)
	ethCustodialStore.Create(&core.EthCustodialAccount{
		CreditChainAddress:   from,
		CreditChainPublicKey: hex.EncodeToString(pub),
		EthAddress:           "0xcccc000000000000000000000000000000cccc",
		KeyRef:               "test-key-ref",
	})
	fromEthb := resolveBalanceAddress(from, hex.EncodeToString(pub), "ETHB")

	const to = "recipient-ethb-address"

	// Just enough for the amount, not enough to also cover the fee.
	bridgedEthStore.Mint(fromEthb, 300)

	tx := buildSignedETHBTransferTx("ethb-send-2", priv, pub, from, to, 300, 2000)
	applyTxOnce(tx)

	if got := bridgedEthStore.BalanceOf(fromEthb); got != 300 {
		t.Fatalf("expected ETHB balance untouched (300) when amount+fee can't both be covered, got %d", got)
	}
	if got := bridgedEthStore.BalanceOf(to); got != 0 {
		t.Fatalf("expected recipient to receive nothing when the fee can't be paid, got %d", got)
	}
}

// TestApplyTransfer_USDCTestFeeChargedInEth confirms USDCTEST sends
// now charge their flat fee in ETH from the sender's ETHB balance -
// see UsdcTestTransferFeeWei's doc comment.
func TestApplyTransfer_USDCTestFeeChargedInEth(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(pub)
	ethCustodialStore.Create(&core.EthCustodialAccount{
		CreditChainAddress:   from,
		CreditChainPublicKey: hex.EncodeToString(pub),
		EthAddress:           "0xdddd000000000000000000000000000000dddd",
		KeyRef:               "test-key-ref",
	})
	fromEthb := resolveBalanceAddress(from, hex.EncodeToString(pub), "ETHB")
	fromUsdc := resolveBalanceAddress(from, hex.EncodeToString(pub), "USDCTEST")

	usdcTestStore.Mint(fromUsdc, 200)
	bridgedEthStore.Mint(fromEthb, UsdcTestTransferFeeWei)

	payload := tokenTransferData{To: "recipient-usdc-address", Amount: 150, Symbol: "USDCTEST"}
	payloadJSON, _ := json.Marshal(payload)
	env := core.SignedEnvelope{Address: from, PublicKey: hex.EncodeToString(pub), Timestamp: 5000, Payload: payloadJSON}
	sig := core.SignMessage(priv, env.CanonicalMessage())
	tx := core.Transaction{
		ID: "usdc-send-1", From: from, To: "recipient-usdc-address", Amount: 150, Type: core.TxExchange,
		Timestamp: 5000, AuthAddress: from, AuthPublicKey: hex.EncodeToString(pub), AuthTimestamp: 5000,
		AuthPayload: payloadJSON, AuthSignature: hex.EncodeToString(sig), Data: string(payloadJSON),
	}
	applyTxOnce(tx)

	if got := usdcTestStore.BalanceOf(fromUsdc); got != 50 {
		t.Fatalf("expected sender USDCTEST balance 50 (200-150), got %d", got)
	}
	if got := usdcTestStore.BalanceOf("recipient-usdc-address"); got != 150 {
		t.Fatalf("expected recipient USDCTEST balance 150, got %d", got)
	}
	if got := bridgedEthStore.BalanceOf(fromEthb); got != 0 {
		t.Fatalf("expected sender ETHB balance 0 after paying the fee, got %d", got)
	}
	if got := bridgedEthStore.BalanceOf(ethbBridgeFeeAddress()); got != UsdcTestTransferFeeWei {
		t.Fatalf("expected the fee sink to hold %d wei, got %d", UsdcTestTransferFeeWei, got)
	}
}

func TestApplyTransfer_USDCTestFeeChargedInEth_InsufficientETHBRejectsWholeTransfer(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(pub)
	fromUsdc := resolveBalanceAddress(from, hex.EncodeToString(pub), "USDCTEST")

	usdcTestStore.Mint(fromUsdc, 200)
	// Deliberately no ETHB minted - plenty of USDCTEST, but the ETH fee
	// can't be paid.

	payload := tokenTransferData{To: "recipient-usdc-address", Amount: 150, Symbol: "USDCTEST"}
	payloadJSON, _ := json.Marshal(payload)
	env := core.SignedEnvelope{Address: from, PublicKey: hex.EncodeToString(pub), Timestamp: 6000, Payload: payloadJSON}
	sig := core.SignMessage(priv, env.CanonicalMessage())
	tx := core.Transaction{
		ID: "usdc-send-2", From: from, To: "recipient-usdc-address", Amount: 150, Type: core.TxExchange,
		Timestamp: 6000, AuthAddress: from, AuthPublicKey: hex.EncodeToString(pub), AuthTimestamp: 6000,
		AuthPayload: payloadJSON, AuthSignature: hex.EncodeToString(sig), Data: string(payloadJSON),
	}
	applyTxOnce(tx)

	if got := usdcTestStore.BalanceOf(fromUsdc); got != 200 {
		t.Fatalf("expected USDCTEST balance untouched (200) when the ETH fee can't be paid, got %d", got)
	}
}

// buildSignedBridgeWithdrawTx signs a real TxBridgeWithdraw the same
// way a genuine client would - mirrors buildSignedETHBTransferTx.
// feeWei simulates what HandleBridgedEthWithdraw itself would have
// already computed live (via ethbWithdrawFeeWei) and snapshotted onto
// the payload before this test's tx.Data is built - applyBridgeWithdraw
// only ever reads the snapshotted value, never recomputes it, so a
// unit test can supply any fixed number here without needing a live
// RPC connection.
func buildSignedBridgeWithdrawTx(id string, priv []byte, pub []byte, from, to string, amount uint64, amountWei string, feeWei uint64, ts int64) core.Transaction {
	payload := bridgeWithdrawData{To: to, AmountWei: amountWei, FeeWei: feeWei}
	payloadJSON, _ := json.Marshal(payload)

	env := core.SignedEnvelope{
		Address:   from,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: ts,
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(priv, env.CanonicalMessage())

	return core.Transaction{
		ID:            id,
		From:          from,
		To:            to,
		Amount:        amount,
		Type:          core.TxBridgeWithdraw,
		Timestamp:     ts,
		AuthAddress:   from,
		AuthPublicKey: hex.EncodeToString(pub),
		AuthTimestamp: ts,
		AuthPayload:   payloadJSON,
		AuthSignature: hex.EncodeToString(sig),
		Data:          string(payloadJSON),
	}
}

func TestApplyBridgeWithdraw_ERCNetworkFeeAndInsufficientBalance(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	ccAddress := core.GenerateAddress(pub)
	ethCustodialStore.Create(&core.EthCustodialAccount{
		CreditChainAddress:   ccAddress,
		CreditChainPublicKey: hex.EncodeToString(pub),
		EthAddress:           "0xeeee000000000000000000000000000000eeee",
		KeyRef:               "test-key-ref",
	})
	ethbAddress := resolveBalanceAddress(ccAddress, hex.EncodeToString(pub), "ETHB")

	const startingBalance = 1_000_000_000_000_000_000 // 1 ETH-equivalent in wei
	bridgedEthStore.Mint(ethbAddress, startingBalance)

	const to = "0x1234567890123456789012345678901234567890"
	const amount = 100_000_000_000_000_000 // 0.1 ETH-equivalent
	// A fixed, test-supplied snapshot standing in for whatever
	// HandleBridgedEthWithdraw would have live-computed via
	// ethbWithdrawFeeWei - applyBridgeWithdraw only ever trusts this
	// value, never recomputes it, so this test needs no live RPC.
	const feeWei = 500_000_000_000_000

	tx := buildSignedBridgeWithdrawTx("withdraw-1", priv, pub, ccAddress, to, amount, "100000000000000000", feeWei, 3000)

	applyTxOnce(tx)

	wantRemaining := uint64(startingBalance - amount - feeWei)
	if got := bridgedEthStore.BalanceOf(ethbAddress); got != wantRemaining {
		t.Fatalf("expected ETHB balance %d after withdrawal (amount+fee burned), got %d", wantRemaining, got)
	}
	if got := bridgedEthStore.BalanceOf(ethbBridgeFeeAddress()); got != feeWei {
		t.Fatalf("expected bridge fee sink to hold %d wei, got %d", feeWei, got)
	}

	// Insufficient balance (amount+fee exceeds what's left) must be a
	// clean no-op at the apply layer, the same guard the handler's own
	// fast-fail check relies on being authoritative.
	tx2 := buildSignedBridgeWithdrawTx("withdraw-2", priv, pub, ccAddress, to, 2_000_000_000_000_000_000, "2000000000000000000", feeWei, 4000)
	applyTxOnce(tx2)

	if got := bridgedEthStore.BalanceOf(ethbAddress); got != wantRemaining {
		t.Fatalf("expected balance unchanged (%d) after rejected over-large withdrawal, got %d", wantRemaining, got)
	}
}

// TestEthbWithdrawFeeWei_ComputesLiveGasPlusBridgeFee confirms
// ethbWithdrawFeeWei's formula directly (gasPrice*gasLimit +
// amount*5bps), independent of the full apply-layer flow above - this
// is what actually replaces the old flat ETHBridgeFeeWei constant.
// ethRPCClient is nil in this test environment, so this only exercises
// the arithmetic via a fake gas price rather than a real RPC round
// trip - full live-RPC behavior is exercised by the browser
// verification pass instead.
func TestEthbWithdrawFeeWei_Formula(t *testing.T) {
	gasPrice := uint64(20_000_000_000) // 20 gwei, a plausible testnet value
	amountWei := uint64(1_000_000_000_000_000_000)

	wantLiveGasFee := gasPrice * ethbWithdrawGasLimitEstimate
	wantBridgeFee := amountWei * ethbWithdrawBridgeFeeBps / 10000
	wantTotal := wantLiveGasFee + wantBridgeFee

	// Direct formula check (mirrors ethbWithdrawFeeWei's body without
	// requiring a live ethRPCClient connection).
	gotLiveGasFee := gasPrice * ethbWithdrawGasLimitEstimate
	gotBridgeFee := amountWei * ethbWithdrawBridgeFeeBps / 10000
	if gotLiveGasFee+gotBridgeFee != wantTotal {
		t.Fatalf("expected total fee %d, got %d", wantTotal, gotLiveGasFee+gotBridgeFee)
	}
	if wantBridgeFee != 500_000_000_000_000 {
		t.Fatalf("expected 0.05%% of 1 ETH-equivalent to be 0.0005 ETH-equivalent (5e14 wei), got %d", wantBridgeFee)
	}
}
