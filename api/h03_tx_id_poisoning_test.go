package api

import "testing"

// TestApplyTxOnce_ForgedInvalidTxCannotPoisonAGenuineTxID is the
// regression test for audit finding H-03: applyTxOnce used to mark
// tx.ID as permanently "seen" before verifyTxAuth ever ran, so a
// forged, unauthenticated transaction that simply copies a real,
// still-pending transaction's ID would consume that ID forever - the
// real transaction (which already carries that exact ID baked in from
// when it was first signed, see core.newTxID) could then never be
// applied, not even by rebroadcasting the identical signed payload
// again. This builds a genuine signed transfer, applies a forged
// transaction reusing its ID first, and confirms the forgery is
// rejected AND the real transaction still goes through afterward.
func TestApplyTxOnce_ForgedInvalidTxCannotPoisonAGenuineTxID(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	genuine, from := buildSignedTransferTx(t, to, 100, 1000)
	cctStore.Mint(from, cctWPlusFee(100))

	// Forged: an attacker's own transaction, built over their own
	// content but with the VICTIM's real, still-pending transaction ID
	// copied onto it (publicly visible in the mempool) and no valid
	// signature over that content - exactly the free, no-private-key-
	// needed attack the audit describes. verifyTxAuth must reject this
	// on signature grounds; what H-03's fix actually closes is that the
	// rejection itself must not burn the shared ID.
	forged, _ := buildSignedTransferTx(t, "attacker-address", 999, 2000)
	forged.ID = genuine.ID
	forged.AuthSignature = "00" + forged.AuthSignature[2:]

	applyTxOnce(forged)

	if got := cctStore.BalanceOf("attacker-address"); got != "0" {
		t.Fatalf("expected the forged transaction to be rejected entirely, attacker-address got %s", got)
	}

	// The real transaction, carrying the exact same ID the forgery
	// tried to squat on, must still apply normally - this is the crux
	// of H-03: before the fix, this would silently no-op because
	// applyTxOnce already believed tx.ID had been "applied" by the
	// rejected forgery above.
	applyTxOnce(genuine)

	if got := cctStore.BalanceOf(to); got != cctW(100) {
		t.Fatalf("expected the genuine transaction (reusing the same ID a forgery already tried and failed to consume) to still apply, recipient got %s", got)
	}
	if got := cctStore.BalanceOf(from); got != "0" {
		t.Fatalf("expected sender's balance to reflect the genuine transfer + fee, got %s", got)
	}
}

// TestApplyTxOnce_RejectedTransactionIDRemainsAvailable is the more
// direct/minimal version of the same regression: applying an
// unauthenticated (garbage signature) transaction must never leave its
// ID marked as seen, full stop - independent of any specific attack
// scenario.
func TestApplyTxOnce_RejectedTransactionIDRemainsAvailable(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	tx, from := buildSignedTransferTx(t, to, 100, 1000)
	cctStore.Mint(from, cctWPlusFee(100))

	badSig := tx
	badSig.AuthSignature = "00" + badSig.AuthSignature[2:]

	applyTxOnce(badSig)

	appliedTx.mu.Lock()
	seen := appliedTx.seen[tx.ID]
	appliedTx.mu.Unlock()

	if seen {
		t.Fatal("expected a rejected (bad-signature) transaction's ID to NOT be marked as seen")
	}

	applyTxOnce(tx)
	if got := cctStore.BalanceOf(to); got != cctW(100) {
		t.Fatalf("expected the genuine transaction to still apply after a rejected same-ID transaction, recipient got %s", got)
	}
}
