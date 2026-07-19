package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

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

	bridgedEthStore.Mint(fromEthb, 500)
	tokenStore.Mint(from, ETHBBridgeCCTNetworkFee)

	tx := buildSignedETHBTransferTx("ethb-send-1", priv, pub, from, to, 300, 1000)
	applyTxOnce(tx)

	if got := bridgedEthStore.BalanceOf(fromEthb); got != 200 {
		t.Fatalf("expected sender ETHB balance 200 (500-300), got %d", got)
	}
	if got := bridgedEthStore.BalanceOf(to); got != 300 {
		t.Fatalf("expected recipient ETHB balance 300, got %d", got)
	}
	if got := tokenStore.BalanceOf(from); got != 0 {
		t.Fatalf("expected sender CCT balance 0 after paying the flat fee, got %d", got)
	}
}

func TestApplyTransfer_ETHBCCTNetworkFee_InsufficientCCTRejectsWholeTransfer(t *testing.T) {
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

	bridgedEthStore.Mint(fromEthb, 500)
	// Deliberately no CCT minted for `from` - plenty of ETHB, but the
	// flat CCT fee can't be paid.

	tx := buildSignedETHBTransferTx("ethb-send-2", priv, pub, from, to, 300, 2000)
	applyTxOnce(tx)

	if got := bridgedEthStore.BalanceOf(fromEthb); got != 500 {
		t.Fatalf("expected ETHB balance untouched (500) when CCT fee can't be paid, got %d", got)
	}
	if got := bridgedEthStore.BalanceOf(to); got != 0 {
		t.Fatalf("expected recipient to receive nothing when the fee can't be paid, got %d", got)
	}
}

// buildSignedBridgeWithdrawTx signs a real TxBridgeWithdraw the same
// way a genuine client would - mirrors buildSignedETHBTransferTx.
func buildSignedBridgeWithdrawTx(id string, priv []byte, pub []byte, from, to string, amount uint64, amountWei string, ts int64) core.Transaction {
	payload := bridgeWithdrawData{To: to, AmountWei: amountWei}
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

	tx := buildSignedBridgeWithdrawTx("withdraw-1", priv, pub, ccAddress, to, amount, "100000000000000000", 3000)

	applyTxOnce(tx)

	wantRemaining := uint64(startingBalance - amount - ETHBridgeFeeWei)
	if got := bridgedEthStore.BalanceOf(ethbAddress); got != wantRemaining {
		t.Fatalf("expected ETHB balance %d after withdrawal (amount+fee burned), got %d", wantRemaining, got)
	}
	if got := bridgedEthStore.BalanceOf(ethbBridgeFeeAddress()); got != ETHBridgeFeeWei {
		t.Fatalf("expected bridge fee sink to hold %d wei, got %d", ETHBridgeFeeWei, got)
	}

	// Insufficient balance (amount+fee exceeds what's left) must be a
	// clean no-op at the apply layer, the same guard the handler's own
	// fast-fail check relies on being authoritative.
	tx2 := buildSignedBridgeWithdrawTx("withdraw-2", priv, pub, ccAddress, to, 2_000_000_000_000_000_000, "2000000000000000000", 4000)
	applyTxOnce(tx2)

	if got := bridgedEthStore.BalanceOf(ethbAddress); got != wantRemaining {
		t.Fatalf("expected balance unchanged (%d) after rejected over-large withdrawal, got %d", wantRemaining, got)
	}
}
