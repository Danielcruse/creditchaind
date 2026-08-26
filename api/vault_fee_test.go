package api

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// resetVaultReleaseGasPrice restores the governance param to its
// default after a test overrides it - governanceParamsStore is a
// package-level global resetApplyTestState() does NOT clear (see its
// own doc comment), so any test that calls Set here would otherwise
// leak its override into every test that runs afterward in the same
// binary.
func resetVaultReleaseGasPrice(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		governanceParamsStore.Set("VaultReleaseGasPriceWei", DefaultVaultReleaseGasPriceWei)
	})
}

func TestVaultReleaseEstimatedFeeWei_UsesDefaultGasPriceAndUnits(t *testing.T) {
	resetApplyTestState()
	resetVaultReleaseGasPrice(t)

	wantEth := new(big.Int).Mul(big.NewInt(DefaultVaultReleaseGasPriceWei), big.NewInt(vaultReleaseGasUnitsEth))
	wantEth.Mul(wantEth, big.NewInt(110))
	wantEth.Div(wantEth, big.NewInt(100))
	if got := vaultReleaseEstimatedFeeWei("ETH"); got.Cmp(wantEth) != 0 {
		t.Fatalf("ETH fee = %s, want %s", got, wantEth)
	}

	wantUsdc := new(big.Int).Mul(big.NewInt(DefaultVaultReleaseGasPriceWei), big.NewInt(vaultReleaseGasUnitsUsdc))
	wantUsdc.Mul(wantUsdc, big.NewInt(110))
	wantUsdc.Div(wantUsdc, big.NewInt(100))
	if got := vaultReleaseEstimatedFeeWei("USDC"); got.Cmp(wantUsdc) != 0 {
		t.Fatalf("USDC fee = %s, want %s", got, wantUsdc)
	}

	// USDC costs more gas than a plain ETH transfer (ERC20 transferFrom
	// overhead) - the fee must reflect that, not charge the same amount
	// regardless of which chain actually paid for it.
	if wantUsdc.Cmp(wantEth) <= 0 {
		t.Fatalf("expected USDC's fee (%s) to exceed ETH's (%s) - USDC release() costs more real gas", wantUsdc, wantEth)
	}

	if got := vaultReleaseEstimatedFeeWei("BTC"); got.Sign() != 0 {
		t.Fatalf("expected BTC to have no gas-cost-based fee (it keeps the old bps model), got %s", got)
	}
}

func TestVaultReleaseEstimatedFeeWei_RespectsGovernanceOverride(t *testing.T) {
	resetApplyTestState()
	resetVaultReleaseGasPrice(t)

	governanceParamsStore.Set("VaultReleaseGasPriceWei", 100_000_000_000) // 100 gwei

	want := new(big.Int).Mul(big.NewInt(100_000_000_000), big.NewInt(vaultReleaseGasUnitsEth))
	want.Mul(want, big.NewInt(110))
	want.Div(want, big.NewInt(100))
	if got := vaultReleaseEstimatedFeeWei("ETH"); got.Cmp(want) != 0 {
		t.Fatalf("after raising gas price, ETH fee = %s, want %s", got, want)
	}
}

func TestSplitVaultReleaseFee_EthNoLongerNeedsAConfiguredRecipient(t *testing.T) {
	resetApplyTestState()
	resetVaultReleaseGasPrice(t)
	// Deliberately NOT setting VAULT_FEE_RECIPIENT_ETH_ADDRESS - the old
	// bps model refused to charge anything without one configured
	// (vaultFeeRecipientConfigured); the new gas-cost model must still
	// charge the fee regardless, since it's held rather than credited
	// to a fixed address up front.
	t.Setenv("VAULT_FEE_RECIPIENT_ETH_ADDRESS", "")

	amount := "5000000000000000000" // 5 ETH
	release, fee := splitVaultReleaseFee("ETH", amount)

	wantFee := vaultReleaseEstimatedFeeWei("ETH")
	if fee != wantFee.String() {
		t.Fatalf("fee = %s, want %s", fee, wantFee)
	}

	amountBig, _ := new(big.Int).SetString(amount, 10)
	releaseBig, _ := new(big.Int).SetString(release, 10)
	sum := new(big.Int).Add(releaseBig, wantFee)
	if sum.Cmp(amountBig) != 0 {
		t.Fatalf("release (%s) + fee (%s) = %s, want the original amount %s", release, fee, sum, amount)
	}
}

func TestSplitVaultReleaseFee_BtcStillUsesBpsModelAndStillRequiresRecipient(t *testing.T) {
	resetApplyTestState()

	// No BTC fee recipient configured - old behavior (charge nothing) must
	// be completely unchanged by this redesign.
	t.Setenv("VAULT_FEE_RECIPIENT_BTC_CCC1_ADDRESS", "")
	release, fee := splitVaultReleaseFee("BTC", "100000000") // 1 BTC in sats
	if fee != "0" || release != "100000000" {
		t.Fatalf("expected BTC with no configured recipient to charge nothing, got release=%s fee=%s", release, fee)
	}

	t.Setenv("VAULT_FEE_RECIPIENT_BTC_CCC1_ADDRESS", "some-btc-ccc1-subaddress")
	governanceParamsStore.Set("VaultReleaseFeeBps", DefaultVaultReleaseFeeBps)
	release, fee = splitVaultReleaseFee("BTC", "100000000")
	if fee != "500000" { // 0.5% of 1 BTC = 0.005 BTC = 500,000 sats
		t.Fatalf("expected BTC's flat 50bps fee (500000 sats), got %s", fee)
	}
	if release != "99500000" {
		t.Fatalf("expected release = 99500000 sats, got %s", release)
	}
}

func TestSplitVaultReleaseFee_FeeExceedsAmountCapsToFullAmount(t *testing.T) {
	resetApplyTestState()
	resetVaultReleaseGasPrice(t)
	governanceParamsStore.Set("VaultReleaseGasPriceWei", 100_000_000_000_000) // absurdly high, guarantees fee > amount

	release, fee := splitVaultReleaseFee("ETH", "1000") // 1000 wei - far less than any real gas cost
	if release != "0" {
		t.Fatalf("expected release to cap at 0 when the fee exceeds the amount, got %s", release)
	}
	if fee != "1000" {
		t.Fatalf("expected the full amount to become the fee, got %s", fee)
	}
}

func TestApplyVaultBurn_EthHoldsFeeWithoutCreditingAnyoneYet(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	resetVaultReleaseGasPrice(t)
	registerThreeBridgeValidators(t)
	t.Setenv("VAULT_FEE_RECIPIENT_ETH_ADDRESS", "0xoldfixedrecipient")

	const depositor, pubKey = "depositor-fee-1", "depositor-fee-1-pubkey"
	attestLock("ETH", "0xseed", depositor, pubKey, "3000000000000000000", "val-a", 1000)
	attestLock("ETH", "0xseed", depositor, pubKey, "3000000000000000000", "val-b", 1001)

	burnData, _ := json.Marshal(vaultBurnData{
		Chain:                 "ETH",
		DestinationExtAddress: "0xexternal",
		AmountSmallestUnit:    "3000000000000000000",
	})
	applyVaultBurn(core.Transaction{ID: "burn-fee-1", AuthAddress: depositor, AuthPublicKey: pubKey, Data: string(burnData), Timestamp: 1002})

	approval, ok := vaultReleaseApprovalStore.Get("burn-fee-1")
	if !ok {
		t.Fatal("expected a release-approval record")
	}

	wantFee := vaultReleaseEstimatedFeeWei("ETH")
	if approval.PendingFeeAmountSmallestUnit != wantFee.String() {
		t.Fatalf("PendingFeeAmountSmallestUnit = %s, want %s", approval.PendingFeeAmountSmallestUnit, wantFee)
	}

	amount, _ := new(big.Int).SetString("3000000000000000000", 10)
	wantRelease := new(big.Int).Sub(amount, wantFee)
	if approval.AmountSmallestUnit != wantRelease.String() {
		t.Fatalf("approval.AmountSmallestUnit = %s, want %s", approval.AmountSmallestUnit, wantRelease)
	}

	// The whole point of deferring: even with an old-style fixed
	// recipient configured (VAULT_FEE_RECIPIENT_ETH_ADDRESS above), it
	// must NOT have received anything at burn time for ETH/USDC anymore.
	if got := ethCcc1Store.BalanceOf("0xoldfixedrecipient"); got != "0" {
		t.Fatalf("expected the old fixed recipient to receive nothing at burn time, got %s", got)
	}
}

func TestApplyVaultReleaseComplete_CreditsPendingFeeToNamedRelayer(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	resetVaultReleaseGasPrice(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-fee-2", "depositor-fee-2-pubkey"
	attestLock("ETH", "0xseed2", depositor, pubKey, "3000000000000000000", "val-a", 1000)
	attestLock("ETH", "0xseed2", depositor, pubKey, "3000000000000000000", "val-b", 1001)

	burnData, _ := json.Marshal(vaultBurnData{
		Chain:                 "ETH",
		DestinationExtAddress: "0xexternal",
		AmountSmallestUnit:    "3000000000000000000",
	})
	applyVaultBurn(core.Transaction{ID: "burn-fee-2", AuthAddress: depositor, AuthPublicKey: pubKey, Data: string(burnData), Timestamp: 1002})

	approval, _ := vaultReleaseApprovalStore.Get("burn-fee-2")
	wantFee := approval.PendingFeeAmountSmallestUnit

	sig := hex.EncodeToString([]byte("sig"))
	approveData, _ := json.Marshal(vaultReleaseApproveData{BurnTxId: "burn-fee-2", SignatureHex: sig})
	applyVaultReleaseApprove(core.Transaction{AuthAddress: "val-a", Data: string(approveData), Timestamp: 1003})

	const relayer = "0xrelayerfeewallet"
	completeData, _ := json.Marshal(vaultReleaseCompleteData{
		BurnTxId:               "burn-fee-2",
		ExternalHash:           "0xrealtxhash",
		FeeRecipientEthAddress: relayer,
	})
	applyVaultReleaseComplete(core.Transaction{Data: string(completeData), Timestamp: 1004})

	if got := ethCcc1Store.BalanceOf(relayer); got != wantFee {
		t.Fatalf("relayer's credited fee = %s, want %s", got, wantFee)
	}

	approval, _ = vaultReleaseApprovalStore.Get("burn-fee-2")
	if approval.Status != core.VaultReleaseReleased {
		t.Fatalf("expected status RELEASED, got %s", approval.Status)
	}
}

func TestApplyVaultReleaseComplete_IsIdempotentAndDoesNotDoubleCreditFee(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	resetVaultReleaseGasPrice(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-fee-3", "depositor-fee-3-pubkey"
	attestLock("ETH", "0xseed3", depositor, pubKey, "3000000000000000000", "val-a", 1000)
	attestLock("ETH", "0xseed3", depositor, pubKey, "3000000000000000000", "val-b", 1001)

	burnData, _ := json.Marshal(vaultBurnData{Chain: "ETH", DestinationExtAddress: "0xexternal", AmountSmallestUnit: "3000000000000000000"})
	applyVaultBurn(core.Transaction{ID: "burn-fee-3", AuthAddress: depositor, AuthPublicKey: pubKey, Data: string(burnData), Timestamp: 1002})

	approval, _ := vaultReleaseApprovalStore.Get("burn-fee-3")
	wantFee := approval.PendingFeeAmountSmallestUnit

	const relayer = "0xrelayerfeewallet3"
	completeData, _ := json.Marshal(vaultReleaseCompleteData{BurnTxId: "burn-fee-3", ExternalHash: "0xhash1", FeeRecipientEthAddress: relayer})

	applyVaultReleaseComplete(core.Transaction{Data: string(completeData), Timestamp: 1004})
	applyVaultReleaseComplete(core.Transaction{Data: string(completeData), Timestamp: 1005}) // replayed/duplicate

	if got := ethCcc1Store.BalanceOf(relayer); got != wantFee {
		t.Fatalf("after a duplicate completion, relayer's fee = %s, want exactly %s (credited once, not twice)", got, wantFee)
	}
}

func TestApplyVaultReleaseComplete_NoRecipientNamedMeansFeeGoesUncredited(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	resetVaultReleaseGasPrice(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-fee-4", "depositor-fee-4-pubkey"
	attestLock("ETH", "0xseed4", depositor, pubKey, "3000000000000000000", "val-a", 1000)
	attestLock("ETH", "0xseed4", depositor, pubKey, "3000000000000000000", "val-b", 1001)

	burnData, _ := json.Marshal(vaultBurnData{Chain: "ETH", DestinationExtAddress: "0xexternal", AmountSmallestUnit: "3000000000000000000"})
	applyVaultBurn(core.Transaction{ID: "burn-fee-4", AuthAddress: depositor, AuthPublicKey: pubKey, Data: string(burnData), Timestamp: 1002})

	// Relayer never configured RELAYER_FEE_RECIPIENT_ETH_ADDRESS - an
	// empty FeeRecipientEthAddress must not panic and must not credit
	// anyone (there's nowhere for it to sensibly go).
	completeData, _ := json.Marshal(vaultReleaseCompleteData{BurnTxId: "burn-fee-4", ExternalHash: "0xhash4", FeeRecipientEthAddress: ""})
	applyVaultReleaseComplete(core.Transaction{Data: string(completeData), Timestamp: 1004})

	approval, _ := vaultReleaseApprovalStore.Get("burn-fee-4")
	if approval.Status != core.VaultReleaseReleased {
		t.Fatalf("expected the release itself to still complete even with no fee recipient, got status %s", approval.Status)
	}
}

// TestApplyVaultReleaseComplete_NormalizesMixedCaseFeeRecipient is a
// regression test for a real incident: RELAYER_FEE_RECIPIENT_ETH_ADDRESS
// is very likely to be configured in checksummed/mixed-case form (the
// conventional way an Ethereum address is displayed/copied), but
// HandleVaultBalance/resolveBalanceAddress always normalize a query to
// lowercase before checking a balance - so a fee credited under the raw,
// un-normalized address was permanently invisible to the normal balance
// endpoint, discovered only by tracing a live withdrawal's "missing" fee
// directly through the persisted state file.
func TestApplyVaultReleaseComplete_NormalizesMixedCaseFeeRecipient(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	resetVaultReleaseGasPrice(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-fee-5", "depositor-fee-5-pubkey"
	attestLock("ETH", "0xseed5", depositor, pubKey, "3000000000000000000", "val-a", 1000)
	attestLock("ETH", "0xseed5", depositor, pubKey, "3000000000000000000", "val-b", 1001)

	burnData, _ := json.Marshal(vaultBurnData{Chain: "ETH", DestinationExtAddress: "0xexternal", AmountSmallestUnit: "3000000000000000000"})
	applyVaultBurn(core.Transaction{ID: "burn-fee-5", AuthAddress: depositor, AuthPublicKey: pubKey, Data: string(burnData), Timestamp: 1002})

	approval, _ := vaultReleaseApprovalStore.Get("burn-fee-5")
	wantFee := approval.PendingFeeAmountSmallestUnit

	const mixedCaseRelayer = "0x5B317b803861D466Cd56aD7754138De3585078d6"
	completeData, _ := json.Marshal(vaultReleaseCompleteData{BurnTxId: "burn-fee-5", ExternalHash: "0xhash5", FeeRecipientEthAddress: mixedCaseRelayer})
	applyVaultReleaseComplete(core.Transaction{Data: string(completeData), Timestamp: 1004})

	// Same lookup HandleVaultBalance actually performs (normalized to
	// lowercase) - this must find the fee, not just the raw mixed-case
	// key the bug used to silently mint under.
	if got := ethCcc1Store.BalanceOf(normalizeEthAddress(mixedCaseRelayer)); got != wantFee {
		t.Fatalf("fee looked up via the normalized (lowercase) address = %s, want %s - credited under the wrong case", got, wantFee)
	}
}

func TestCreditVaultReleaseFee_OnlyAppliesToBtcAnymore(t *testing.T) {
	resetApplyTestState()
	t.Setenv("VAULT_FEE_RECIPIENT_ETH_ADDRESS", "0xshouldneverreceive")

	creditVaultReleaseFee("ETH", "1000000000000000000")
	if got := ethCcc1Store.BalanceOf("0xshouldneverreceive"); got != "0" {
		t.Fatalf("expected creditVaultReleaseFee to no-op for ETH now, but it credited %s", got)
	}

	creditVaultReleaseFee("USDC", "1000000")
	if got := usdcCcc1Store.BalanceOf("0xshouldneverreceive"); got != "0" {
		t.Fatalf("expected creditVaultReleaseFee to no-op for USDC now, but it credited %s", got)
	}
}
