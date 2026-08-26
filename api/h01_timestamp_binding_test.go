package api

import (
	"testing"
	"time"
)

// TestVerifyTxAuth_RejectsProducerMutatedTimestamp is the regression
// test for audit finding H-01: core.ComputeBlockHash only hashes
// tx.ID, never a transaction's actual field values, so a block
// producer could previously rewrite Transaction.Timestamp after
// signing (e.g. to backdate an unbonding/undelegate maturesAt, or
// manipulate applyVaultGasPriceOracleUpdate's cooldown) with no
// detectable effect on the block hash. The fix binds Timestamp to the
// signed AuthTimestamp (core.NewAuthenticatedTransaction) and rejects
// any transaction where they diverge (verifyTxAuth).
func TestVerifyTxAuth_RejectsProducerMutatedTimestamp(t *testing.T) {
	resetApplyTestState()

	now := time.Now().Unix()
	tx, _ := buildSignedTransferTx(t, "recipient-address", 10, now)

	if !verifyTxAuth(tx) {
		t.Fatalf("expected the genuinely-signed, untampered transaction to verify")
	}

	// Simulate a block producer rewriting Timestamp after the fact -
	// exactly what core.ComputeBlockHash's tx.ID-only hashing can't
	// detect. AuthTimestamp (the signed value) is left untouched.
	tampered := tx
	tampered.Timestamp = now - 1_000_000 // backdated by ~11.5 days

	if verifyTxAuth(tampered) {
		t.Fatalf("expected a transaction with a producer-mutated Timestamp (diverging from the signed AuthTimestamp) to be rejected")
	}
}

// TestVerifyTxAuth_GrandfathersPreFixHistoricalTimestamps confirms the
// H01FixCutoverUnixSeconds gate does its job: a transaction whose
// AuthTimestamp predates the fix (i.e. every already-confirmed
// historical transaction, which was built before Timestamp was bound
// to AuthTimestamp and so may legitimately have Timestamp !=
// AuthTimestamp) must still verify - otherwise a fresh node
// replaying/resyncing the full historical chain would reject
// already-real, already-confirmed blocks.
func TestVerifyTxAuth_GrandfathersPreFixHistoricalTimestamps(t *testing.T) {
	resetApplyTestState()

	preFix := H01FixCutoverUnixSeconds - 3600 // one hour before cutover
	tx, _ := buildSignedTransferTx(t, "recipient-address", 10, preFix)

	// Simulate the pre-fix construction pattern, where Timestamp was
	// independently clock-derived rather than copied from
	// AuthTimestamp - deliberately diverging them here.
	tx.Timestamp = preFix - 5

	if !verifyTxAuth(tx) {
		t.Fatalf("expected a pre-cutover transaction to verify despite Timestamp != AuthTimestamp - this would break resyncing pre-fix chain history")
	}
}

// TestVerifyTxAuth_EnforcesBindingRightAtCutover confirms the gate's
// boundary is inclusive-at-cutover (>=), not off-by-one.
func TestVerifyTxAuth_EnforcesBindingRightAtCutover(t *testing.T) {
	resetApplyTestState()

	tx, _ := buildSignedTransferTx(t, "recipient-address", 10, H01FixCutoverUnixSeconds)
	tx.Timestamp = H01FixCutoverUnixSeconds - 1 // diverges from AuthTimestamp

	if verifyTxAuth(tx) {
		t.Fatalf("expected a transaction with AuthTimestamp exactly at the cutover to be subject to the binding check")
	}
}
