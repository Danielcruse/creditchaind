package api

import (
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestViewTransaction_VaultReleaseComplete_ResolvesRealDestinationAndAmount
// covers the real gap found while investigating a live tx-page
// screenshot: TxVaultReleaseComplete's own Data carries only a burn-tx
// reference and the external hash - never the real destination address
// or amount, which live on the ORIGINAL TxVaultBurn instead. Before
// this, the Explorer showed "To: (none - system-side)" and "Amount: 0"
// for a real, real-money confirmation transaction.
func TestViewTransaction_VaultReleaseComplete_ResolvesRealDestinationAndAmount(t *testing.T) {
	resetApplyTestState()

	burnData, _ := json.Marshal(vaultBurnData{
		Chain:                 "ETH",
		DestinationExtAddress: "0x000000000000000000000000000000000000ad",
		AmountSmallestUnit:    "1500000000000000000", // 1.5 ETH
	})
	burnTx := core.Transaction{
		ID:   "burn-tx-1",
		Type: core.TxVaultBurn,
		Data: string(burnData),
	}

	releaseData, _ := json.Marshal(vaultReleaseCompleteData{
		BurnTxId:     "burn-tx-1",
		ExternalHash: "0xec157c000000000000000000000000000000000000000000000000000cb396e",
	})
	releaseTx := core.Transaction{
		ID:   "release-tx-1",
		Type: core.TxVaultReleaseComplete,
		Data: string(releaseData),
	}

	blockchain = &core.Blockchain{Blocks: []core.Block{
		{Index: 0, Transactions: []core.Transaction{burnTx, releaseTx}},
	}}

	view := viewTransaction(releaseTx)

	if view.ExternalDestination != "0x000000000000000000000000000000000000ad" {
		t.Fatalf("ExternalDestination = %q, want the real destination address", view.ExternalDestination)
	}
	if view.ExternalDestinationChain != "ETH" {
		t.Fatalf("ExternalDestinationChain = %q, want ETH", view.ExternalDestinationChain)
	}
	if view.ExternalAmountSmallestUnit != "1500000000000000000" {
		t.Fatalf("ExternalAmountSmallestUnit = %q, want the real wei amount", view.ExternalAmountSmallestUnit)
	}
	if view.ExternalTxHash == "" {
		t.Fatalf("ExternalTxHash should be populated from the release's own Data")
	}
	if view.ExternalTxLink == "" {
		t.Fatalf("ExternalTxLink should be populated once ExternalTxHash and the burn's chain are known")
	}
	wantSummary := "Sent 1.5 ETH to 0x000000...0000ad - confirmed on the external network (0xec157c...cb396e)"
	if view.Summary != wantSummary {
		t.Fatalf("Summary = %q, want %q", view.Summary, wantSummary)
	}
}

// TestViewTransaction_VaultReleaseComplete_UnresolvableBurnFallsBackHonestly
// confirms a release whose referenced burn transaction can't be found
// (e.g. pruned, or a genuinely bad reference) degrades to the older,
// still-honest "withdrawal confirmed" wording instead of crashing or
// fabricating a destination/amount.
func TestViewTransaction_VaultReleaseComplete_UnresolvableBurnFallsBackHonestly(t *testing.T) {
	resetApplyTestState()

	releaseData, _ := json.Marshal(vaultReleaseCompleteData{
		BurnTxId:     "does-not-exist",
		ExternalHash: "0xabc123",
	})
	releaseTx := core.Transaction{
		ID:   "release-tx-2",
		Type: core.TxVaultReleaseComplete,
		Data: string(releaseData),
	}

	blockchain = &core.Blockchain{Blocks: []core.Block{
		{Index: 0, Transactions: []core.Transaction{releaseTx}},
	}}

	view := viewTransaction(releaseTx)

	if view.ExternalDestination != "" {
		t.Fatalf("ExternalDestination should stay empty when the burn tx can't be resolved, got %q", view.ExternalDestination)
	}
	wantSummary := "Withdrawal confirmed on the external network (0xabc123)"
	if view.Summary != wantSummary {
		t.Fatalf("Summary = %q, want %q", view.Summary, wantSummary)
	}
}
