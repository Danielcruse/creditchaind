package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestApplyVaultBurn_RefusesWithdrawalTooSmallToSurviveItsOwnFee is the
// core regression test for the real incident: a withdrawal smaller
// than the flat ETH/USDC release fee must never burn the user's CCC1
// balance at all - not burn it and then deliver a worthless $0
// release, as it used to.
func TestApplyVaultBurn_RefusesWithdrawalTooSmallToSurviveItsOwnFee(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	resetVaultReleaseGasPrice(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-too-small", "depositor-too-small-pubkey"
	attestLock("ETH", "0xseed", depositor, pubKey, "3000000000000000000", "val-a", 1000)
	attestLock("ETH", "0xseed", depositor, pubKey, "3000000000000000000", "val-b", 1001)

	depositAddr := resolveBalanceAddress(depositor, pubKey, "ETH_CCC1")
	before := ethCcc1Store.BalanceOf(depositAddr)

	// Well under the default fee (~0.00264 ETH) - the exact shape of
	// the real stuck-approval incident.
	tooSmall := "1000000000000000" // 0.001 ETH
	burnData, _ := json.Marshal(vaultBurnData{
		Chain:                 "ETH",
		DestinationExtAddress: "0xexternal",
		AmountSmallestUnit:    tooSmall,
	})
	applyVaultBurn(core.Transaction{ID: "burn-too-small", AuthAddress: depositor, AuthPublicKey: pubKey, Data: string(burnData), Timestamp: 1002})

	if got := ethCcc1Store.BalanceOf(depositAddr); got != before {
		t.Fatalf("expected the balance to be completely untouched (no burn at all), before=%s after=%s", before, got)
	}
	if _, ok := vaultReleaseApprovalStore.Get("burn-too-small"); ok {
		t.Fatal("expected no release-approval record to be created for a withdrawal too small to survive its own fee")
	}
}

// TestApplyVaultBurn_AllowsWithdrawalLargerThanTheFee confirms the fix
// doesn't overreach - a genuinely large-enough withdrawal still burns
// and opens a real release approval exactly as before.
func TestApplyVaultBurn_AllowsWithdrawalLargerThanTheFee(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	resetVaultReleaseGasPrice(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-big-enough", "depositor-big-enough-pubkey"
	attestLock("ETH", "0xseed", depositor, pubKey, "3000000000000000000", "val-a", 1000)
	attestLock("ETH", "0xseed", depositor, pubKey, "3000000000000000000", "val-b", 1001)

	burnData, _ := json.Marshal(vaultBurnData{
		Chain:                 "ETH",
		DestinationExtAddress: "0xexternal",
		AmountSmallestUnit:    "3000000000000000000", // 3 ETH - comfortably above the fee
	})
	applyVaultBurn(core.Transaction{ID: "burn-big-enough", AuthAddress: depositor, AuthPublicKey: pubKey, Data: string(burnData), Timestamp: 1002})

	approval, ok := vaultReleaseApprovalStore.Get("burn-big-enough")
	if !ok {
		t.Fatal("expected a release-approval record for a withdrawal comfortably larger than the fee")
	}
	if approval.AmountSmallestUnit == "0" {
		t.Fatal("expected a nonzero release amount")
	}
}

// TestApplyVaultBurn_BtcIsUnaffectedByTheEthUsdcFloorCheck confirms the
// new check is scoped to ETH/USDC only - BTC's percentage-based fee
// model can never consume more than 100% of the amount, so it never
// needed this guard, and the fix must not accidentally block it.
func TestApplyVaultBurn_BtcIsUnaffectedByTheEthUsdcFloorCheck(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerThreeBridgeValidators(t)
	t.Setenv("VAULT_FEE_RECIPIENT_BTC_CCC1_ADDRESS", "some-btc-ccc1-fee-recipient")

	const depositor, pubKey = "depositor-btc-small", "depositor-btc-small-pubkey"
	attestLock("BTC", "0xseed", depositor, pubKey, "10000", "val-a", 1000)
	attestLock("BTC", "0xseed", depositor, pubKey, "10000", "val-b", 1001)

	burnData, _ := json.Marshal(vaultBurnData{
		Chain:                 "BTC",
		DestinationExtAddress: "bc1qexternal",
		AmountSmallestUnit:    "10000", // tiny by ETH standards, fine for BTC's proportional fee
	})
	applyVaultBurn(core.Transaction{ID: "burn-btc-small", AuthAddress: depositor, AuthPublicKey: pubKey, Data: string(burnData), Timestamp: 1002})

	if _, ok := vaultReleaseApprovalStore.Get("burn-btc-small"); !ok {
		t.Fatal("expected a BTC release-approval record - the ETH/USDC-only floor check should never block BTC")
	}
}

func TestHandleVaultBurn_RejectsWithdrawalTooSmallWithClearError(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	resetVaultReleaseGasPrice(t)
	registerThreeBridgeValidators(t)

	pub, priv, _ := core.GenerateKeyPair()
	addr := core.GenerateAddress(pub)
	pubHex := hex.EncodeToString(pub)
	attestLock("ETH", "0xseed", addr, pubHex, "3000000000000000000", "val-a", 1000)
	attestLock("ETH", "0xseed", addr, pubHex, "3000000000000000000", "val-b", 1001)

	body := buildSignedEnvelopeJSON(t, pub, priv, vaultBurnData{
		Chain:                 "ETH",
		DestinationExtAddress: "0xexternal",
		AmountSmallestUnit:    "1000000000000000", // 0.001 ETH, too small
	})
	req := httptest.NewRequest(http.MethodPost, "/vault/burn", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleVaultBurn(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a withdrawal too small to survive its own fee, got %d: %s", rec.Code, rec.Body.String())
	}
}
