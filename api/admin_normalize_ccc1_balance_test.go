package api

import (
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestApplyAdminNormalizeCcc1Balance_MovesLegacyKeyToNormalizedForm is
// the direct regression test for the real incident this tool exists to
// fix: a balance minted under a mixed-case ETH_CCC1 key (by the
// creditVaultReleaseFeeToRelayer bug, then again by the pre-fix
// applyAdminTransfer) that TxAdminTransfer itself can no longer reach
// once it started normalizing both sides.
func TestApplyAdminNormalizeCcc1Balance_MovesLegacyKeyToNormalizedForm(t *testing.T) {
	resetApplyTestState()

	const mixedCase = "0xf2002bEaAEC93e7533AD497E3ee4B78B9c637ee4"
	const lowercase = "0xf2002beaaec93e7533ad497e3ee4b78b9c637ee4"

	// Minted directly under the raw mixed-case key - mirrors exactly
	// how the pre-fix bug produced this state (never went through
	// normalizeEthAddress at all).
	ethCcc1Store.Mint(mixedCase, "2640000000000000")

	data, _ := json.Marshal(adminNormalizeCcc1BalanceData{Symbol: "ETH_CCC1", LegacyAddress: mixedCase})
	applyAdminNormalizeCcc1Balance(core.Transaction{Data: string(data), Timestamp: 1000})

	if got := ethCcc1Store.BalanceOf(mixedCase); got != "0" {
		t.Fatalf("legacy mixed-case key balance = %s, want 0 (fully migrated away)", got)
	}
	// The lookup HandleVaultBalance actually performs (already
	// normalized, since normalizeEthAddress("...lowercase...") is a
	// no-op on an already-lowercase string).
	if got := ethCcc1Store.BalanceOf(lowercase); got != "2640000000000000" {
		t.Fatalf("normalized key balance = %s, want 2640000000000000", got)
	}
}

// TestApplyAdminNormalizeCcc1Balance_IsIdempotent confirms a second run
// against an already-migrated (now-zero) legacy key is a harmless no-op,
// not a duplicate credit - same replay-safety convention every other
// apply function in this file follows.
func TestApplyAdminNormalizeCcc1Balance_IsIdempotent(t *testing.T) {
	resetApplyTestState()

	const mixedCase = "0xf2002bEaAEC93e7533AD497E3ee4B78B9c637ee4"
	const lowercase = "0xf2002beaaec93e7533ad497e3ee4b78b9c637ee4"

	ethCcc1Store.Mint(mixedCase, "1000000000000000")
	data, _ := json.Marshal(adminNormalizeCcc1BalanceData{Symbol: "ETH_CCC1", LegacyAddress: mixedCase})

	applyAdminNormalizeCcc1Balance(core.Transaction{Data: string(data), Timestamp: 1000})
	applyAdminNormalizeCcc1Balance(core.Transaction{Data: string(data), Timestamp: 1001}) // replayed

	if got := ethCcc1Store.BalanceOf(lowercase); got != "1000000000000000" {
		t.Fatalf("after a duplicate run, normalized balance = %s, want exactly 1000000000000000 (migrated once, not twice)", got)
	}
}

// TestApplyAdminNormalizeCcc1Balance_AlreadyNormalizedKeyIsNoOp confirms
// this never touches a legacy address that's already in normalized
// (lowercase) form - nothing to migrate, so it must not burn/re-mint a
// balance that was never actually misplaced.
func TestApplyAdminNormalizeCcc1Balance_AlreadyNormalizedKeyIsNoOp(t *testing.T) {
	resetApplyTestState()

	const lowercase = "0xf2002beaaec93e7533ad497e3ee4b78b9c637ee4"
	ethCcc1Store.Mint(lowercase, "500000000000000")

	data, _ := json.Marshal(adminNormalizeCcc1BalanceData{Symbol: "ETH_CCC1", LegacyAddress: lowercase})
	applyAdminNormalizeCcc1Balance(core.Transaction{Data: string(data), Timestamp: 1000})

	if got := ethCcc1Store.BalanceOf(lowercase); got != "500000000000000" {
		t.Fatalf("balance = %s, want unchanged 500000000000000 - an already-normalized key should never be touched", got)
	}
}
