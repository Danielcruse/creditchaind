package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

const postH03CutoverTs = H03FixCutoverUnixSeconds + 3600

func TestReplayKeyForTx_PreCutoverUsesTxID(t *testing.T) {
	tx := core.Transaction{ID: "some-legacy-id", AuthTimestamp: H03FixCutoverUnixSeconds - 1, AuthSignature: "aabbcc"}
	if got := replayKeyForTx(tx); got != "some-legacy-id" {
		t.Fatalf("expected a pre-cutover transaction to key on tx.ID, got %q", got)
	}
}

func TestReplayKeyForTx_PostCutoverUsesSignatureHash(t *testing.T) {
	tx1 := core.Transaction{ID: "attacker-chosen-id", AuthTimestamp: postH03CutoverTs, AuthSignature: "aabbcc"}
	tx2 := core.Transaction{ID: "totally-different-id", AuthTimestamp: postH03CutoverTs, AuthSignature: "aabbcc"}

	key1 := replayKeyForTx(tx1)
	key2 := replayKeyForTx(tx2)

	if key1 == "attacker-chosen-id" {
		t.Fatal("expected a post-cutover transaction to NOT key on the raw tx.ID field")
	}
	if key1 != key2 {
		t.Fatalf("expected two transactions with the same AuthSignature but different tx.ID to produce the SAME replay key (%q vs %q) - the key must be signature-derived, not ID-derived", key1, key2)
	}

	tx3 := core.Transaction{ID: "attacker-chosen-id", AuthTimestamp: postH03CutoverTs, AuthSignature: "ddeeff"}
	if replayKeyForTx(tx3) == key1 {
		t.Fatal("expected a different AuthSignature to produce a different replay key even with the same tx.ID")
	}
}

// TestApplyTxOnce_GenuinelySignedForgeryCannotPoisonAVictimsID_PostCutover
// is the regression test for H-03's full fix - closing the residual
// gap the earlier partial mitigation explicitly disclosed (see
// H03FixCutoverUnixSeconds's doc comment): an attacker who genuinely
// controls a real (if unfunded) keypair signs a well-formed, valid
// transaction that copies a victim's tx.ID and would fail semantically
// (insufficient balance) - before this fix, since it passes
// verifyTxAuth, it would still have consumed the shared tx.ID and
// permanently blocked the victim's real transaction. After this fix,
// the two transactions have different real signatures and therefore
// different replay keys, so they can never collide at all.
func TestApplyTxOnce_GenuinelySignedForgeryCannotPoisonAVictimsID_PostCutover(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	genuine, from := buildSignedTransferTx(t, to, 100, postH03CutoverTs)
	cctStore.Mint(from, cctWPlusFee(100))

	// Attacker's own genuinely-signed transaction (their own real key,
	// their own real content) with zero funding, but claiming the exact
	// same tx.ID the victim's transaction already carries.
	forgery, attackerFrom := buildSignedTransferTx(t, "attacker-address", 999, postH03CutoverTs)
	forgery.ID = genuine.ID // attempt to squat on the victim's ID
	// attackerFrom is deliberately left unfunded - forgery must fail on
	// insufficient balance, the exact "genuinely signed but
	// semantically doomed" scenario that used to still poison the ID.

	applyTxOnce(forgery)

	if got := cctStore.BalanceOf("attacker-address"); got != "0" {
		t.Fatalf("expected the unfunded forgery to fail semantically (insufficient balance), attacker-address got %s", got)
	}
	if got := cctStore.BalanceOf(attackerFrom); got != "0" {
		t.Fatalf("expected the attacker's own (unfunded) balance to remain 0, got %s", got)
	}

	// The critical assertion: the victim's genuine transaction, sharing
	// the exact tx.ID the forgery tried to squat on, must still apply -
	// this is exactly what the earlier partial fix could NOT guarantee.
	applyTxOnce(genuine)

	if got := cctStore.BalanceOf(to); got != cctW(100) {
		t.Fatalf("expected the genuine transaction to still apply despite a genuinely-signed forgery claiming its ID first, recipient got %s", got)
	}
}

// TestApplyTxOnce_GenuineRetrySameSignatureDedupesCorrectly_PostCutover
// confirms the new signature-based key still correctly recognizes a
// real duplicate (the exact same signed transaction re-applied, e.g.
// gossip arriving twice) as a duplicate, not a distinct transaction -
// the core dedup property must survive this fix, not just the
// anti-poisoning property.
func TestApplyTxOnce_GenuineRetrySameSignatureDedupesCorrectly_PostCutover(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	tx, from := buildSignedTransferTx(t, to, 100, postH03CutoverTs)
	cctStore.Mint(from, cctW(200))

	applyTxOnce(tx)
	applyTxOnce(tx) // identical transaction, replayed - must be a no-op the second time

	if got := cctStore.BalanceOf(to); got != cctW(100) {
		t.Fatalf("expected the transfer applied exactly once, recipient got %s", got)
	}
}
