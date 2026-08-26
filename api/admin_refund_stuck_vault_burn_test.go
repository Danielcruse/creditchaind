package api

import (
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func seedStuckEthApproval(id, burnCcAddress, releaseAmount, feeAmount string) {
	vaultReleaseApprovalStore.Create(&core.VaultReleaseApproval{
		ID:                           id,
		Chain:                        "ETH",
		BurnCcAddress:                burnCcAddress,
		DestinationExtAddress:        "0xexternal",
		AmountSmallestUnit:           releaseAmount,
		ReleaseID:                    1,
		Signatures:                   map[string][]byte{"some-validator": []byte("stale-sig")},
		Status:                       core.VaultReleasePending,
		CreatedAt:                    1000,
		PendingFeeAmountSmallestUnit: feeAmount,
	})
}

// TestApplyAdminRefundStuckVaultBurn_RefundsFullOriginalAmount is the
// core regression test for the real incident: a burn whose entire
// value got clamped into the fee (release amount 0) must be refunded
// in full - release + fee - back to the burner's real self-custodial
// address, and the approval must flip to REFUNDED so it can never be
// signed/relayed/refunded again.
func TestApplyAdminRefundStuckVaultBurn_RefundsFullOriginalAmount(t *testing.T) {
	resetApplyTestState()

	const ccAddr, ethAddr = "burner-cc-addr", "0xf2002bEaAEC93e7533AD497E3ee4B78B9c637ee4"
	ethAddressStore.Set(&core.EthWatchedAddress{CreditChainAddress: ccAddr, EthAddress: ethAddr})
	seedStuckEthApproval("burn-stuck-1", ccAddr, "0", "2640000000000000")

	data, _ := json.Marshal(adminRefundStuckVaultBurnData{BurnTxId: "burn-stuck-1"})
	applyAdminRefundStuckVaultBurn(core.Transaction{Data: string(data), Timestamp: 2000})

	normalized := normalizeEthAddress(ethAddr)
	if got := ethCcc1Store.BalanceOf(normalized); got != "2640000000000000" {
		t.Fatalf("expected the full original burned amount (release+fee) refunded, got %s", got)
	}

	approval, _ := vaultReleaseApprovalStore.Get("burn-stuck-1")
	if approval.Status != core.VaultReleaseRefunded {
		t.Fatalf("expected status REFUNDED, got %s", approval.Status)
	}
}

// TestApplyAdminRefundStuckVaultBurn_RefusesAnAlreadyReleasedApproval
// is the core safety property: refunding a RELEASED approval would
// double-spend, since the real value already left the vault contract.
func TestApplyAdminRefundStuckVaultBurn_RefusesAnAlreadyReleasedApproval(t *testing.T) {
	resetApplyTestState()

	const ccAddr, ethAddr = "burner-cc-addr", "0xf2002bEaAEC93e7533AD497E3ee4B78B9c637ee4"
	ethAddressStore.Set(&core.EthWatchedAddress{CreditChainAddress: ccAddr, EthAddress: ethAddr})
	seedStuckEthApproval("burn-released-1", ccAddr, "1000000000000000", "0")
	approval, _ := vaultReleaseApprovalStore.Get("burn-released-1")
	approval.Status = core.VaultReleaseReleased

	data, _ := json.Marshal(adminRefundStuckVaultBurnData{BurnTxId: "burn-released-1"})
	applyAdminRefundStuckVaultBurn(core.Transaction{Data: string(data), Timestamp: 2000})

	normalized := normalizeEthAddress(ethAddr)
	if got := ethCcc1Store.BalanceOf(normalized); got != "0" {
		t.Fatalf("expected no refund for an already-RELEASED approval, got %s credited", got)
	}
	if approval.Status != core.VaultReleaseReleased {
		t.Fatalf("expected status to remain RELEASED, got %s", approval.Status)
	}
}

// TestApplyAdminRefundStuckVaultBurn_IsNotReplayable confirms a second
// application against an already-REFUNDED approval is a safe no-op -
// no double-crediting.
func TestApplyAdminRefundStuckVaultBurn_IsNotReplayable(t *testing.T) {
	resetApplyTestState()

	const ccAddr, ethAddr = "burner-cc-addr", "0xf2002bEaAEC93e7533AD497E3ee4B78B9c637ee4"
	ethAddressStore.Set(&core.EthWatchedAddress{CreditChainAddress: ccAddr, EthAddress: ethAddr})
	seedStuckEthApproval("burn-replay-1", ccAddr, "0", "500000000000000")

	data, _ := json.Marshal(adminRefundStuckVaultBurnData{BurnTxId: "burn-replay-1"})
	applyAdminRefundStuckVaultBurn(core.Transaction{Data: string(data), Timestamp: 2000})
	applyAdminRefundStuckVaultBurn(core.Transaction{Data: string(data), Timestamp: 2001}) // replayed

	normalized := normalizeEthAddress(ethAddr)
	if got := ethCcc1Store.BalanceOf(normalized); got != "500000000000000" {
		t.Fatalf("expected exactly one refund (not doubled), got %s", got)
	}
}

// TestApplyAdminRefundStuckVaultBurn_NoOpWithoutARegisteredEthAddress
// confirms this refuses to guess a destination when the burner has no
// real, already-registered self-custodial address on file - the same
// "refuse rather than credit the wrong place" discipline as the HTTP
// fast-fail.
func TestApplyAdminRefundStuckVaultBurn_NoOpWithoutARegisteredEthAddress(t *testing.T) {
	resetApplyTestState()

	seedStuckEthApproval("burn-no-addr-1", "unregistered-cc-addr", "0", "100000000000000")

	data, _ := json.Marshal(adminRefundStuckVaultBurnData{BurnTxId: "burn-no-addr-1"})
	applyAdminRefundStuckVaultBurn(core.Transaction{Data: string(data), Timestamp: 2000})

	approval, _ := vaultReleaseApprovalStore.Get("burn-no-addr-1")
	if approval.Status != core.VaultReleasePending {
		t.Fatalf("expected the approval to remain PENDING when no real address can be resolved, got %s", approval.Status)
	}
}
