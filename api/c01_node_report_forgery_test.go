package api

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// buildForgedNodeReportTx builds a genuinely, validly self-signed
// node-authored-shaped transaction from an ARBITRARY, unregistered
// keypair - exactly what audit finding C-01 describes: "Any attacker
// can create their own valid Ed25519 wallet signature over
// attacker-chosen JSON" and have it accepted as a privileged report,
// because the old check only proved Data == AuthPayload, never that
// the signer was an authorized node.
func buildForgedNodeReportTx(t *testing.T, txType core.TransactionType, dataJSON []byte) core.Transaction {
	t.Helper()
	attackerPub, attackerPriv, _ := core.GenerateKeyPair()
	attackerAddr := core.GenerateAddress(attackerPub)

	env := core.SignedEnvelope{
		Address:   attackerAddr,
		PublicKey: hex.EncodeToString(attackerPub),
		Timestamp: 9000,
		Payload:   dataJSON,
	}
	sig := core.SignMessage(attackerPriv, env.CanonicalMessage())

	return core.Transaction{
		ID:            "forged-" + string(txType),
		From:          attackerAddr,
		Type:          txType,
		Data:          string(dataJSON),
		Timestamp:     env.Timestamp,
		AuthAddress:   attackerAddr,
		AuthPublicKey: env.PublicKey,
		AuthTimestamp: env.Timestamp,
		AuthPayload:   dataJSON,
		AuthSignature: hex.EncodeToString(sig),
	}
}

func TestVerifyTxAuth_RejectsForgedVaultReleaseComplete(t *testing.T) {
	resetApplyTestState()
	// Deliberately NOT calling registerCurrentNodeAsAuthorizedValidator -
	// this is the whole point: no real validator vouches for the
	// attacker's key.

	data, _ := json.Marshal(vaultReleaseCompleteData{
		BurnTxId:               "someone-elses-real-burn",
		ExternalHash:           "0xfake",
		FeeRecipientEthAddress: "0xattackerwallet",
	})
	tx := buildForgedNodeReportTx(t, core.TxVaultReleaseComplete, data)

	if verifyTxAuth(tx) {
		t.Fatalf("expected a forged TxVaultReleaseComplete from an unauthorized key to be rejected - this would let an attacker falsely mark someone else's real pending withdrawal as released")
	}
}

func TestVerifyTxAuth_RejectsForgedSlashReport(t *testing.T) {
	resetApplyTestState()

	data, _ := json.Marshal(slashReportData{
		ValidatorAddress: "some-honest-validator",
		Reason:        "missed_block",
	})
	tx := buildForgedNodeReportTx(t, core.TxSlashReport, data)

	if verifyTxAuth(tx) {
		t.Fatalf("expected a forged TxSlashReport from an unauthorized key to be rejected - unlimited forged reports under distinct attacker addresses could previously Sybil-slash an honest validator")
	}
}

func TestVerifyTxAuth_RejectsForgedBridgeDeposit(t *testing.T) {
	resetApplyTestState()

	data, _ := json.Marshal(bridgeDepositReportData{
		CreditChainAddress: "attacker-controlled-address",
		NewBalanceWei:      "999999999999999999999",
	})
	tx := buildForgedNodeReportTx(t, core.TxBridgeDeposit, data)

	if verifyTxAuth(tx) {
		t.Fatalf("expected a forged TxBridgeDeposit from an unauthorized key to be rejected - this would mint unbacked ETHB")
	}
}

func TestIsAuthorizedNodeIdentity_EnvAllowlist(t *testing.T) {
	resetApplyTestState()

	pub, _, _ := core.GenerateKeyPair()
	keyHex := hex.EncodeToString(pub)

	if isAuthorizedNodeIdentity(keyHex) {
		t.Fatalf("expected an unlisted key to be rejected before the env var is set")
	}

	t.Setenv("AUTHORIZED_NODE_REPORTER_PUBLIC_KEYS", "aaaa,"+strings.ToUpper(keyHex)+",bbbb")
	if !isAuthorizedNodeIdentity(keyHex) {
		t.Fatalf("expected the allowlisted key to be accepted (case-insensitively) once configured")
	}

	otherPub, _, _ := core.GenerateKeyPair()
	if isAuthorizedNodeIdentity(hex.EncodeToString(otherPub)) {
		t.Fatalf("expected a key NOT in the allowlist to still be rejected")
	}
}

func TestVerifyTxAuth_AcceptsGenuineReportFromAuthorizedNode(t *testing.T) {
	resetApplyTestState()
	registerCurrentNodeAsAuthorizedValidator(t)

	data, _ := json.Marshal(slashReportData{
		ValidatorAddress: "some-validator",
		Reason:        "missed_block",
	})
	env := selfSignedEnvelope(data)
	tx := core.Transaction{
		ID:            "genuine-slash-report",
		From:          reporterAddress(),
		Type:          core.TxSlashReport,
		Data:          string(data),
		Timestamp:     env.Timestamp,
		AuthAddress:   env.Address,
		AuthPublicKey: env.PublicKey,
		AuthTimestamp: env.Timestamp,
		AuthPayload:   env.Payload,
		AuthSignature: env.Signature,
	}

	if !verifyTxAuth(tx) {
		t.Fatalf("expected a genuine, authorized node's report to still verify - the fix must not break legitimate reporting")
	}
}
