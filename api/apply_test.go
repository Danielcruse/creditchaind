package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// resetApplyTestState gives each test a clean slate for every
// package-level store applyTxOnce/ApplyTransaction touch for a plain
// CCT transfer - these are real singletons shared across the package,
// so tests that mutate them must not leak state into each other.
func resetApplyTestState() {
	walletStore = core.NewWalletStore()
	tokenStore = core.NewTokenStore()
	appliedTx.mu.Lock()
	appliedTx.seen = make(map[string]bool)
	appliedTx.mu.Unlock()
}

// buildSignedTransferTx builds a genuinely-signed TxExchange
// transaction (a plain CCT transfer) exactly the way a real client
// would - same envelope/signature shape as core.SignedEnvelope, packed
// into the transaction's Auth* fields the way recordTx does.
func buildSignedTransferTx(t *testing.T, to string, amount uint64, ts int64) (core.Transaction, string) {
	t.Helper()

	pub, priv, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(pub)

	payload := tokenTransferData{To: to, Amount: amount}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	env := core.SignedEnvelope{
		Address:   from,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: ts,
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(priv, env.CanonicalMessage())

	tx := core.Transaction{
		ID:            "test-tx-" + from + "-" + to,
		From:          from,
		To:            to,
		Amount:        amount,
		Type:          core.TxExchange,
		Timestamp:     ts,
		AuthAddress:   from,
		AuthPublicKey: hex.EncodeToString(pub),
		AuthTimestamp: ts,
		AuthPayload:   payloadJSON,
		AuthSignature: hex.EncodeToString(sig),
	}
	return tx, from
}

func TestApplyTxOnce_ValidTransferMovesBalance(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	tx, from := buildSignedTransferTx(t, to, 100, 1000)
	tokenStore.Mint(from, 100)

	applyTxOnce(tx)

	if got := tokenStore.BalanceOf(from); got != 0 {
		t.Fatalf("expected sender balance 0 after transfer, got %d", got)
	}
	if got := tokenStore.BalanceOf(to); got != 100 {
		t.Fatalf("expected recipient balance 100, got %d", got)
	}
}

func TestApplyTxOnce_DuplicateIDAppliedOnce(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	tx, from := buildSignedTransferTx(t, to, 100, 1000)
	tokenStore.Mint(from, 200)

	applyTxOnce(tx)
	applyTxOnce(tx) // same tx.ID, replayed - must be a no-op the second time

	if got := tokenStore.BalanceOf(to); got != 100 {
		t.Fatalf("expected recipient balance 100 (transfer applied exactly once), got %d", got)
	}
	if got := tokenStore.BalanceOf(from); got != 100 {
		t.Fatalf("expected sender to have 100 left (only debited once), got %d", got)
	}
}

func TestApplyTxOnce_TamperedSignatureRejected(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	tx, from := buildSignedTransferTx(t, to, 100, 1000)
	tokenStore.Mint(from, 100)

	// Flip the signature to something that won't verify - simulates a
	// corrupted or forged transaction reaching a node.
	tx.AuthSignature = "00" + tx.AuthSignature[2:]

	applyTxOnce(tx)

	if got := tokenStore.BalanceOf(from); got != 100 {
		t.Fatalf("expected balance untouched after a bad signature, got %d (sender should still have 100)", got)
	}
	if got := tokenStore.BalanceOf(to); got != 0 {
		t.Fatalf("expected recipient to receive nothing from a rejected transaction, got %d", got)
	}
}

func TestApplyTxOnce_AmountMismatchBetweenSignedAndClaimedRejected(t *testing.T) {
	resetApplyTestState()

	const to = "recipient-address"
	// Signed payload says 100, but the transaction itself (what a
	// malicious block producer could rewrite after the fact) claims
	// 999 - verifyTxAuth's TxExchange case exists specifically to catch
	// this: a validly-signed envelope whose Transaction.Amount was
	// altered after signing.
	tx, from := buildSignedTransferTx(t, to, 100, 1000)
	tx.Amount = 999
	tokenStore.Mint(from, 999)

	applyTxOnce(tx)

	if got := tokenStore.BalanceOf(to); got != 0 {
		t.Fatalf("expected the amount-mismatched transaction to be rejected entirely, recipient got %d", got)
	}
	if got := tokenStore.BalanceOf(from); got != 999 {
		t.Fatalf("expected sender's balance untouched, got %d", got)
	}
}

func TestApplyTxOnce_RecipientMismatchBetweenSignedAndClaimedRejected(t *testing.T) {
	resetApplyTestState()

	// Signed payload authorizes paying "recipient-address", but the
	// transaction's own To field was redirected to "attacker-address" -
	// must be rejected, not silently redirected.
	tx, from := buildSignedTransferTx(t, "recipient-address", 100, 1000)
	tx.To = "attacker-address"
	tokenStore.Mint(from, 100)

	applyTxOnce(tx)

	if got := tokenStore.BalanceOf("attacker-address"); got != 0 {
		t.Fatalf("expected a redirected recipient to receive nothing, got %d", got)
	}
	if got := tokenStore.BalanceOf(from); got != 100 {
		t.Fatalf("expected sender's balance untouched, got %d", got)
	}
}

func TestApplyBlock_DuplicateBlockAppliedOnce(t *testing.T) {
	resetApplyTestState()
	appliedBlock.mu.Lock()
	appliedBlock.seen = make(map[string]bool)
	appliedBlock.mu.Unlock()

	const to = "recipient-address"
	tx, from := buildSignedTransferTx(t, to, 50, 2000)
	tokenStore.Mint(from, 50)

	block := core.Block{
		Index:        1,
		Hash:         "test-block-hash",
		Timestamp:    2000,
		Transactions: []core.Transaction{tx},
	}

	ApplyBlock(block)
	ApplyBlock(block) // same Hash, replayed - must be a no-op the second time

	if got := tokenStore.BalanceOf(to); got != 50 {
		t.Fatalf("expected recipient balance 50 (block applied exactly once), got %d", got)
	}
}
