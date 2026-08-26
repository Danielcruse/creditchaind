package api

import (
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestApplyAdminTransfer_NormalizesMixedCaseEthAddresses is a regression
// test for a real incident: using this exact tool to recover a balance
// stuck at a mixed-case ETH_CCC1 address (see vault_fee_test.go's own
// mixed-case regression test for the original incident) moved it to a
// SECOND mixed-case key, since applyAdminTransfer didn't normalize
// either - the "fix" reproduced the same bug it was meant to resolve.
// Discovered by re-checking the destination balance via the normal
// (always-lowercase) /vault/balance query immediately after the move.
func TestApplyAdminTransfer_NormalizesMixedCaseEthAddresses(t *testing.T) {
	resetApplyTestState()

	const mixedCaseFrom = "0x5B317b803861D466Cd56aD7754138De3585078d6"
	const mixedCaseTo = "0xf2002bEaAEC93e7533AD497E3ee4B78B9c637ee4"
	ethCcc1Store.Mint(normalizeEthAddress(mixedCaseFrom), "2640000000000000")

	data := adminTransferData{From: mixedCaseFrom, To: mixedCaseTo, Amount: 2640000000000000, Symbol: "ETH_CCC1"}
	payload, _ := json.Marshal(data)
	applyAdminTransfer(core.Transaction{Data: string(payload), Timestamp: 1000})

	// Same lookup HandleVaultBalance actually performs (normalized to
	// lowercase) - both the source (now empty) and destination (now
	// funded) must be reachable through it, not just the raw store.
	if got := ethCcc1Store.BalanceOf(normalizeEthAddress(mixedCaseFrom)); got != "0" {
		t.Fatalf("source balance (normalized) = %s, want 0 - should have been fully debited", got)
	}
	if got := ethCcc1Store.BalanceOf(normalizeEthAddress(mixedCaseTo)); got != "2640000000000000" {
		t.Fatalf("destination balance looked up via the normalized (lowercase) address = %s, want 2640000000000000 - credited under the wrong case", got)
	}
}

// TestApplyAdminTransfer_NormalizesCcPrefixedCreditChainAddresses is the
// CreditChain-native counterpart to the mixed-case ETH regression above:
// an operator typing "From"/"To" in the site's normal "cc"-prefixed
// display form (the only form CreditChain addresses are ever shown in
// elsewhere on the site) must still resolve to the same real, raw
// 40-character account a genuinely-stored balance actually lives at -
// otherwise this exact recovery tool would move a balance to a brand
// new, unreachable 42-character key, reproducing the same class of
// incident the mixed-case ETH fix above already closed for ETH-style
// assets.
func TestApplyAdminTransfer_NormalizesCcPrefixedCreditChainAddresses(t *testing.T) {
	resetApplyTestState()

	const rawFrom = "43df565874fc3b8415148b4a6e6f01e30cfe1eb8"
	const rawTo = "7864a654505f2e2d7e5c2fe33014df2c25c2fee4"

	cctStore.Mint(rawFrom, core.FormatCctSmallest(core.CctWholeToSmallest(3000)))

	data := adminTransferData{From: "cc" + rawFrom, To: "cc" + rawTo, Amount: 3000, Symbol: "CCT"}
	payload, _ := json.Marshal(data)
	applyAdminTransfer(core.Transaction{Data: string(payload), Timestamp: 1000})

	if got := core.CctSmallestToWholeFloor(cctStore.BalanceOf(rawFrom)); got != 0 {
		t.Fatalf("expected the source raw address to be fully drained, got %d CCT still there", got)
	}
	if got := core.CctSmallestToWholeFloor(cctStore.BalanceOf(rawTo)); got != 3000 {
		t.Fatalf("expected 3000 CCT to land at the destination's real raw address, got %d", got)
	}
	if got := cctStore.BalanceOf("cc" + rawTo); got != "0" {
		t.Fatalf("expected nothing credited under the cc-prefixed destination key, got %s", got)
	}
}
