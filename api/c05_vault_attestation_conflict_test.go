package api

import "testing"

// TestApplyVaultLockAttest_RejectsConflictingClaim is the regression
// test for audit finding C-05, covering both the original partial fix
// (2026-08-15) and the full fix (2026-08-16, see
// vaultLockAttestationID's doc comment): a malicious bridge validator
// front-runs a real deposit event by attesting first with their OWN
// inflated amount/recipient. Before ANY fix, an honest validator's
// later, correct attestation for the SAME (chain, externalTxHash,
// externalIndex) would have its vote silently counted toward the
// attacker's already-stored fraudulent claim - neither the fraudulent
// nor the real claim could ever safely reach threshold. The partial
// fix (still exercised by this test) made the fraudulent claim's votes
// never contaminate the honest one; the full fix goes further - the
// honest claim now naturally lives at its OWN, different, content-
// derived ID, so it isn't just protected from contamination, it can
// actually still reach threshold and mint normally, which the partial
// fix alone could NOT guarantee (a genuinely front-run real deposit
// used to get permanently stuck - see this test's final assertion,
// which the partial-fix-only version of this test did not check).
func TestApplyVaultLockAttest_RejectsConflictingClaim(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerThreeBridgeValidators(t)

	const realDepositor, realPubKey = "real-depositor", "real-depositor-pubkey"
	const attackerAddress, attackerPubKey = "attacker-cc-address", "attacker-pubkey"

	// The malicious validator (val-a) front-runs with a fraudulent
	// claim: same external event identifier, but their own recipient
	// and a wildly inflated amount.
	attestLock("ETH", "0xfrontrun", attackerAddress, attackerPubKey, "999000000000000000000", "val-a", 1000)

	// The honest validator (val-b) independently observed the REAL
	// event and attests with the correct recipient/amount.
	attestLock("ETH", "0xfrontrun", realDepositor, realPubKey, "1000000000000000000", "val-b", 1001)

	realDepositAddr := resolveBalanceAddress(realDepositor, realPubKey, "ETH_CCC1")
	attackerDepositAddr := resolveBalanceAddress(attackerAddress, attackerPubKey, "ETH_CCC1")

	if got := ethCcc1Store.BalanceOf(attackerDepositAddr); got != "0" {
		t.Fatalf("expected the fraudulent claim to never mint (only 1 of 2 required attestors, honest vote correctly rejected), got %s", got)
	}
	if got := ethCcc1Store.BalanceOf(realDepositAddr); got != "0" {
		t.Fatalf("expected the honest depositor to also not be minted yet (their own claim only has 1 attestor so far - val-b), got %s", got)
	}

	// A second honest validator (val-c) also observes the real event
	// and attests correctly - this is the legitimate path to threshold
	// for the REAL claim, and must succeed despite the attacker's
	// conflicting record sitting under the same event id.
	attestLock("ETH", "0xfrontrun", realDepositor, realPubKey, "1000000000000000000", "val-c", 1002)

	if got := ethCcc1Store.BalanceOf(attackerDepositAddr); got != "0" {
		t.Fatalf("expected the fraudulent claim to STILL never mint, got %s", got)
	}
	// The full fix's actual improvement over the partial one: the real
	// depositor's claim now lives at its own content-derived ID,
	// completely unaffected by the attacker's front-run sitting at a
	// different ID - val-b + val-c is 2 honest votes, reaching the
	// threshold of 2, so this must mint normally rather than staying
	// stuck forever the way the partial fix alone left it.
	if got := ethCcc1Store.BalanceOf(realDepositAddr); got != "1000000000000000000" {
		t.Fatalf("expected the real depositor's claim to mint normally once 2 honest validators attest to it, despite the attacker's conflicting front-run record - got %s", got)
	}
}

// TestApplyVaultLockAttest_HonestMatchingAttestationsStillMint confirms
// the fix doesn't break the ordinary, non-adversarial path: multiple
// validators independently attesting to the SAME correct details must
// still mint exactly once at threshold, same as before this fix.
func TestApplyVaultLockAttest_HonestMatchingAttestationsStillMint(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "honest-depositor", "honest-depositor-pubkey"
	attestLock("ETH", "0xhonest", depositor, pubKey, "2000000000000000000", "val-a", 1000)
	attestLock("ETH", "0xhonest", depositor, pubKey, "2000000000000000000", "val-b", 1001)

	depositAddr := resolveBalanceAddress(depositor, pubKey, "ETH_CCC1")
	if got := ethCcc1Store.BalanceOf(depositAddr); got != "2000000000000000000" {
		t.Fatalf("expected the honest, matching attestations to mint normally at threshold, got %s", got)
	}
}
