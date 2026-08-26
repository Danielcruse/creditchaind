package api

import (
	"encoding/hex"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// setGovernanceParamsForTest sets governanceParamsStore keys for the
// duration of one test and restores the exact prior snapshot
// afterward - governanceParamsStore is a package-level singleton NOT
// reset by resetApplyTestState, so a test that sets a price param
// must clean up after itself or leak state into every later test.
func setGovernanceParamsForTest(t *testing.T, params map[string]uint64) {
	t.Helper()
	before := governanceParamsStore.Snapshot()
	for k, v := range params {
		governanceParamsStore.Set(k, v)
	}
	t.Cleanup(func() {
		governanceParamsStore.Restore(before)
	})
}

func TestCcc1TransferFeeSmallest_ZeroWhenPriceUnset(t *testing.T) {
	setGovernanceParamsForTest(t, map[string]uint64{
		"EthUsdCentsPerEth": 0,
		"BtcUsdCentsPerBtc": 0,
	})

	if fee := ccc1TransferFeeSmallest("ETH_CCC1"); fee != 0 {
		t.Fatalf("expected 0 ETH_CCC1 fee when EthUsdCentsPerEth is unset, got %d", fee)
	}
	if fee := ccc1TransferFeeSmallest("BTC_CCC1"); fee != 0 {
		t.Fatalf("expected 0 BTC_CCC1 fee when BtcUsdCentsPerBtc is unset, got %d", fee)
	}
}

func TestCcc1TransferFeeSmallest_ComputesFromGovernanceParams(t *testing.T) {
	setGovernanceParamsForTest(t, map[string]uint64{
		"EthUsdCentsPerEth":          200_000,   // $2,000.00/ETH
		"EthCcc1TransferFeeUsdCents": 300,       // $3.00 fee
		"BtcUsdCentsPerBtc":          5_000_000, // $50,000.00/BTC
		"BtcCcc1TransferFeeUsdCents": 150,       // $1.50 fee
	})

	// $3 fee at $2000/ETH = 0.0015 ETH = 1,500,000,000,000,000 wei.
	if got, want := ccc1TransferFeeSmallest("ETH_CCC1"), uint64(1_500_000_000_000_000); got != want {
		t.Fatalf("ETH_CCC1 fee = %d, want %d", got, want)
	}
	// $1.50 fee at $50,000/BTC = 0.00003 BTC = 3,000 sats.
	if got, want := ccc1TransferFeeSmallest("BTC_CCC1"), uint64(3_000); got != want {
		t.Fatalf("BTC_CCC1 fee = %d, want %d", got, want)
	}
	// USDC_CCC1 needs no price param - fixed 1:1 USD peg at 6 decimals.
	// Default (200 cents = $2.00) applies since it wasn't overridden.
	if got, want := ccc1TransferFeeSmallest("USDC_CCC1"), DefaultUsdcCcc1TransferFeeUsdCents*10_000; got != want {
		t.Fatalf("USDC_CCC1 fee = %d, want %d", got, want)
	}
}

// TestApplyTransfer_Ccc1ChargesFeeAndSplits is the actual enforcement
// property this feature exists for: a plain ETH_CCC1 wallet-to-wallet
// transfer must move exactly the sent amount to the recipient, debit
// amount+fee from the sender, and split that fee 50/20/10/10/10 exactly
// like every other platform fee (see
// TestSplitPlatformFee_SplitsFiftyTwentyTenTenTen).
func TestApplyTransfer_Ccc1ChargesFeeAndSplits(t *testing.T) {
	resetApplyTestState()
	treasuryStore.Treasury.FoundationAddress = "foundation-addr"
	setGovernanceParamsForTest(t, map[string]uint64{
		"EthUsdCentsPerEth":          200_000, // $2,000.00/ETH
		"EthCcc1TransferFeeUsdCents": 300,     // $3.00 -> 1,500,000,000,000,000 wei fee
	})

	fromPub, _, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(fromPub)
	fromPubHex := hex.EncodeToString(fromPub)

	toPub, _, _ := core.GenerateKeyPair()
	to := core.GenerateAddress(toPub)
	walletStore.Register(&core.Wallet{Address: to, PublicKeyHex: hex.EncodeToString(toPub)})

	const amount = uint64(10_000_000_000_000_000) // 0.01 ETH
	const fee = uint64(1_500_000_000_000_000)     // 0.0015 ETH, matches the params above

	store, err := resolveTokenStore("ETH_CCC1")
	if err != nil {
		t.Fatalf("resolveTokenStore: %v", err)
	}
	// resolveBalanceAddress derives ETH_CCC1's real balance key from
	// (address, pubkey) rather than using the CreditChain address
	// directly (see api/subaddress.go) - mint/check there, exactly what
	// applyTransfer will independently resolve too, not at the bare
	// CreditChain address.
	fromSide := resolveBalanceAddress(from, fromPubHex, "ETH_CCC1")
	toSide := resolveBalanceAddress(to, hex.EncodeToString(toPub), "ETH_CCC1")
	store.Mint(fromSide, amount+fee)

	tx := core.Transaction{
		ID:            "test-ccc1-transfer",
		From:          from,
		To:            to,
		Amount:        amount,
		Type:          core.TxExchange,
		Data:          `{"symbol":"ETH_CCC1"}`,
		AuthPublicKey: fromPubHex,
	}
	applyTransfer(tx)

	if got := store.BalanceOf(fromSide); got != 0 {
		t.Fatalf("expected sender balance 0 (amount + fee both spent), got %d", got)
	}
	if got := store.BalanceOf(toSide); got != amount {
		t.Fatalf("expected recipient to receive exactly the sent amount %d, got %d", amount, got)
	}
	if got, want := store.BalanceOf(treasuryStore.Treasury.FoundationAddress), fee*10/100; got != want {
		t.Fatalf("expected Foundation to receive 10%% of the fee (%d), got %d", want, got)
	}
	if got, want := store.BalanceOf(platformFeeValidatorPoolAddress("ETH_CCC1")), fee*20/100; got != want {
		t.Fatalf("expected validator pool to receive 20%% of the fee (%d), got %d", want, got)
	}
	if got, want := store.BalanceOf(outernetOperatorAddress), fee*10/100; got != want {
		t.Fatalf("expected the Outernet operator address to receive 10%% of the fee (%d), got %d", want, got)
	}
	if got, want := store.BalanceOf(platformFeeModeratorPoolAddress("ETH_CCC1")), fee*10/100; got != want {
		t.Fatalf("expected the moderator pool to receive 10%% of the fee (%d), got %d", want, got)
	}
}

// TestApplyTransfer_Ccc1NoFeeConfiguredStillTransfers proves the
// fail-open behavior end to end: with no price configured, the
// transfer still succeeds and moves the full amount, charging nothing.
func TestApplyTransfer_Ccc1NoFeeConfiguredStillTransfers(t *testing.T) {
	resetApplyTestState()
	setGovernanceParamsForTest(t, map[string]uint64{"EthUsdCentsPerEth": 0})

	fromPub, _, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(fromPub)
	fromPubHex := hex.EncodeToString(fromPub)

	toPub, _, _ := core.GenerateKeyPair()
	to := core.GenerateAddress(toPub)
	walletStore.Register(&core.Wallet{Address: to, PublicKeyHex: hex.EncodeToString(toPub)})

	const amount = uint64(5_000_000_000_000_000)

	store, _ := resolveTokenStore("ETH_CCC1")
	fromSide := resolveBalanceAddress(from, fromPubHex, "ETH_CCC1")
	toSide := resolveBalanceAddress(to, hex.EncodeToString(toPub), "ETH_CCC1")
	store.Mint(fromSide, amount)

	tx := core.Transaction{
		ID: "test-ccc1-transfer-no-fee", From: from, To: to, Amount: amount,
		Type: core.TxExchange, Data: `{"symbol":"ETH_CCC1"}`, AuthPublicKey: fromPubHex,
	}
	applyTransfer(tx)

	if got := store.BalanceOf(fromSide); got != 0 {
		t.Fatalf("expected sender balance 0 (full amount spent, no fee), got %d", got)
	}
	if got := store.BalanceOf(toSide); got != amount {
		t.Fatalf("expected recipient to receive the full amount %d, got %d", amount, got)
	}
}
