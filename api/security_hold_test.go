package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// allowDepositsForTest temporarily lifts the deposit hold for a single
// test - existing crediting-logic tests need this to keep exercising
// applyBridgeDeposit/applyBridgeBtcDeposit/applyVaultLockAttest's real
// behavior, which still needs to be correct for whenever the hold
// actually lifts in production.
func allowDepositsForTest(t *testing.T) {
	t.Helper()
	before := DepositsHeldForSecurityRemediation
	DepositsHeldForSecurityRemediation = false
	t.Cleanup(func() { DepositsHeldForSecurityRemediation = before })
}

func TestApplyBridgeDeposit_HeldDuringSecurityRemediation(t *testing.T) {
	resetApplyTestState()
	DepositsHeldForSecurityRemediation = true
	// Registered so this test isolates the deposit hold specifically -
	// without this, the report would also be rejected by the
	// isAuthorizedNodeIdentity check (audit finding C-01), which would
	// make this test pass for the wrong reason.
	registerCurrentNodeAsAuthorizedValidator(t)

	ccAddress, _, ethbAddress := setupCustodialAccount(t, "0xdeadbeef00000000000000000000000000dead")
	tx := buildBridgeDepositTx("held-dep-1", ccAddress, "1000000000000000000")
	applyTxOnce(tx)

	if got := bridgedEthStore.BalanceOf(ethbAddress); got != 0 {
		t.Fatalf("expected the held deposit to credit nothing, got %d", got)
	}
}

func TestApplyBridgeBtcDeposit_HeldDuringSecurityRemediation(t *testing.T) {
	resetApplyTestState()
	DepositsHeldForSecurityRemediation = true
	registerCurrentNodeAsAuthorizedValidator(t)

	pub, _, _ := core.GenerateKeyPair()
	ccAddress := core.GenerateAddress(pub)
	pubHex := hex.EncodeToString(pub)
	btcAddressStore.Set(&core.BtcWatchedAddress{
		CreditChainAddress:   ccAddress,
		CreditChainPublicKey: pubHex,
		BtcAddress:           "tb1qheldtest00000000000000000000000000000",
	})
	btcbAddress := resolveBalanceAddress(ccAddress, pubHex, "BTCB")

	data, _ := json.Marshal(btcBridgeDepositReportData{
		CreditChainAddress: ccAddress,
		NewBalanceSats:     50_000,
	})
	env := selfSignedEnvelope(data)
	tx := core.Transaction{
		ID:            "held-btc-dep-1",
		From:          reporterAddress(),
		Type:          core.TxBridgeBtcDeposit,
		Data:          string(data),
		Timestamp:     env.Timestamp,
		AuthAddress:   env.Address,
		AuthPublicKey: env.PublicKey,
		AuthTimestamp: env.Timestamp,
		AuthPayload:   env.Payload,
		AuthSignature: env.Signature,
	}
	applyTxOnce(tx)

	if got := bridgedBtcStore.BalanceOf(btcbAddress); got != 0 {
		t.Fatalf("expected the held BTC deposit to credit nothing, got %d", got)
	}
}

func TestApplyVaultLockAttest_HeldDuringSecurityRemediation(t *testing.T) {
	resetApplyTestState()
	registerThreeBridgeValidators(t)
	DepositsHeldForSecurityRemediation = true

	const depositor, pubKey = "held-vault-depositor", "held-vault-depositor-pubkey"
	attestLock("STETH", "0xheldvaulttx", depositor, pubKey, "1000000000000000000", "val-a", 1000)
	attestLock("STETH", "0xheldvaulttx", depositor, pubKey, "1000000000000000000", "val-b", 1001)

	depositAddr := resolveBalanceAddress(depositor, pubKey, "STETH_CCC1")
	if got := stethCcc1Store.BalanceOf(depositAddr); got != "0" {
		t.Fatalf("expected the held vault attestation to mint nothing, got %s", got)
	}
}

func TestApplyBridgeDeposit_CreditsWhenNotHeld(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerCurrentNodeAsAuthorizedValidator(t)

	ccAddress, _, ethbAddress := setupCustodialAccount(t, "0xdeadbeef00000000000000000000000000dea1")
	tx := buildBridgeDepositTx("unheld-dep-1", ccAddress, "1000000000000000000")
	applyTxOnce(tx)

	if got := bridgedEthStore.BalanceOf(ethbAddress); got != 1000000000000000000 {
		t.Fatalf("expected the unheld deposit to credit normally, got %d", got)
	}
}
