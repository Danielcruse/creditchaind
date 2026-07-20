package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func TestAdminMultisigConfig_EnabledRequiresReachableThreshold(t *testing.T) {
	resetApplyTestState()

	t.Setenv("ADMIN_MULTISIG_ADDRESSES", "aaa,bbb,ccc")
	t.Setenv("ADMIN_MULTISIG_THRESHOLD", "2")
	if !adminMultisigEnabled() {
		t.Fatal("expected multisig to be enabled with a reachable threshold (2-of-3)")
	}
	if got := adminMultisigThreshold(); got != 2 {
		t.Fatalf("expected threshold 2, got %d", got)
	}
	if len(adminMultisigSigners()) != 3 {
		t.Fatalf("expected 3 parsed signers, got %d", len(adminMultisigSigners()))
	}

	// M > N must never enable multisig - a permanently-unreachable
	// threshold is worse than refusing to enable at all.
	t.Setenv("ADMIN_MULTISIG_THRESHOLD", "5")
	if adminMultisigEnabled() {
		t.Fatal("expected multisig to stay disabled when threshold exceeds the signer count")
	}
}

func TestAdminMultisig_ProposeCountsAsFirstApprovalButDoesNotExecuteAlone(t *testing.T) {
	resetApplyTestState()

	const signerA, signerB, signerC = "signer-a", "signer-b", "signer-c"
	t.Setenv("ADMIN_MULTISIG_ADDRESSES", signerA+","+signerB+","+signerC)
	t.Setenv("ADMIN_MULTISIG_THRESHOLD", "3")

	mintData, _ := json.Marshal(tokenMintData{Address: "recipient-1", Amount: 500, Symbol: "CCT"})
	proposeData, _ := json.Marshal(adminMultisigProposeData{ActionType: core.TxTokenMint, Payload: string(mintData)})

	applyAdminMultisigPropose(core.Transaction{
		ID:          "proposal-1",
		AuthAddress: signerA,
		Data:        string(proposeData),
		Timestamp:   1000,
	})

	proposal, ok := adminMultisigStore.Get("proposal-1")
	if !ok {
		t.Fatal("expected a proposal to be created")
	}
	if len(proposal.Approvals) != 1 || !proposal.Approvals[signerA] {
		t.Fatalf("expected exactly the proposer's own approval recorded, got %v", proposal.Approvals)
	}
	if proposal.Status != core.AdminMultisigPending {
		t.Fatalf("expected status PENDING with 1 of 3 required approvals, got %s", proposal.Status)
	}
	if got := tokenStore.BalanceOf("recipient-1"); got != 0 {
		t.Fatalf("expected no mint yet with only 1 of 3 approvals, got balance %d", got)
	}
}

func TestAdminMultisig_ExecutesExactlyOnceOnceThresholdReached(t *testing.T) {
	resetApplyTestState()

	const signerA, signerB, signerC = "signer-a", "signer-b", "signer-c"
	t.Setenv("ADMIN_MULTISIG_ADDRESSES", signerA+","+signerB+","+signerC)
	t.Setenv("ADMIN_MULTISIG_THRESHOLD", "3")

	mintData, _ := json.Marshal(tokenMintData{Address: "recipient-2", Amount: 777, Symbol: "CCT"})
	proposeData, _ := json.Marshal(adminMultisigProposeData{ActionType: core.TxTokenMint, Payload: string(mintData)})

	applyAdminMultisigPropose(core.Transaction{ID: "proposal-2", AuthAddress: signerA, Data: string(proposeData), Timestamp: 1000})

	approve := func(id, signer string, ts int64) {
		data, _ := json.Marshal(adminMultisigApproveData{ProposalID: id})
		applyAdminMultisigApprove(core.Transaction{ID: id + "-approve-" + signer, AuthAddress: signer, Data: string(data), Timestamp: ts})
	}

	approve("proposal-2", signerB, 1001)
	if got := tokenStore.BalanceOf("recipient-2"); got != 0 {
		t.Fatalf("expected no mint yet with only 2 of 3 approvals, got balance %d", got)
	}

	approve("proposal-2", signerC, 1002)
	if got := tokenStore.BalanceOf("recipient-2"); got != 777 {
		t.Fatalf("expected the mint to execute exactly once threshold (3) was reached, got balance %d", got)
	}

	proposal, _ := adminMultisigStore.Get("proposal-2")
	if proposal.Status != core.AdminMultisigExecuted {
		t.Fatalf("expected status EXECUTED, got %s", proposal.Status)
	}
	if proposal.ExecutedAt != 1002 {
		t.Fatalf("expected ExecutedAt to be the timestamp of the threshold-crossing approval (1002), got %d", proposal.ExecutedAt)
	}

	// A late 4th "approval" attempt (e.g. a signer resubmitting, or the
	// proposer approving their own already-executed proposal again)
	// must never double-mint.
	approve("proposal-2", signerA, 1003)
	if got := tokenStore.BalanceOf("recipient-2"); got != 777 {
		t.Fatalf("expected balance to stay at 777 after a late approval on an already-executed proposal, got %d", got)
	}
}

func TestAdminMultisig_DuplicateApprovalFromSameSignerDoesNotDoubleCount(t *testing.T) {
	resetApplyTestState()

	const signerA, signerB, signerC = "signer-a", "signer-b", "signer-c"
	t.Setenv("ADMIN_MULTISIG_ADDRESSES", signerA+","+signerB+","+signerC)
	t.Setenv("ADMIN_MULTISIG_THRESHOLD", "2")

	mintData, _ := json.Marshal(tokenMintData{Address: "recipient-3", Amount: 100, Symbol: "CCT"})
	proposeData, _ := json.Marshal(adminMultisigProposeData{ActionType: core.TxTokenMint, Payload: string(mintData)})
	applyAdminMultisigPropose(core.Transaction{ID: "proposal-3", AuthAddress: signerA, Data: string(proposeData), Timestamp: 1000})

	approveData, _ := json.Marshal(adminMultisigApproveData{ProposalID: "proposal-3"})
	// signerB "approves" via two separately-submitted transactions
	// (e.g. a retried request) - must still only count once.
	applyAdminMultisigApprove(core.Transaction{ID: "approve-1", AuthAddress: signerB, Data: string(approveData), Timestamp: 1001})
	applyAdminMultisigApprove(core.Transaction{ID: "approve-2", AuthAddress: signerB, Data: string(approveData), Timestamp: 1002})

	proposal, _ := adminMultisigStore.Get("proposal-3")
	if len(proposal.Approvals) != 2 {
		t.Fatalf("expected exactly 2 distinct approvals (signerA, signerB), got %d", len(proposal.Approvals))
	}
	if got := tokenStore.BalanceOf("recipient-3"); got != 100 {
		t.Fatalf("expected the mint to have executed once (threshold 2, 2 distinct approvers), got balance %d", got)
	}
}

// TestNormalizeAdminAddress_DoesNotCorruptARawAddressStartingWithCC
// is a regression test for a real bug found while writing this
// feature's own smoke test: a randomly-generated address happened to
// start with the raw bytes "cc" (~1-in-256 chance, not hypothetical),
// and the naive strings.TrimPrefix(addr, "cc") this codebase's
// cc-display-prefix convention originally used would incorrectly
// strip those two genuine hex characters, corrupting the address.
func TestNormalizeAdminAddress_DoesNotCorruptARawAddressStartingWithCC(t *testing.T) {
	// A genuine 40-hex-char raw address whose first two characters
	// happen to be "c","c" - must be left completely untouched.
	rawAddrStartingWithCC := "cc28472b2cfa67d1333573560795516996a66790"
	if got := normalizeAdminAddress(rawAddrStartingWithCC); got != rawAddrStartingWithCC {
		t.Fatalf("expected a 40-char raw address starting with cc to be left untouched, got %q", got)
	}

	// A genuine cosmetic "cc"-prefixed display address (42 chars total,
	// "cc" + 40 hex) - the "cc" here IS a display decoration and must
	// be stripped down to the real 40-char raw address.
	displayAddr := "cc4d8e7f8d5688cda5c6ef68574f26dc259e40672a"
	want := "4d8e7f8d5688cda5c6ef68574f26dc259e40672a"
	if got := normalizeAdminAddress(displayAddr); got != want {
		t.Fatalf("expected the cc display prefix to be stripped, got %q, want %q", got, want)
	}
}

func TestAdminMultisig_UnauthorizedSignerCannotProposeOrApprove(t *testing.T) {
	resetApplyTestState()

	const signerA, signerB = "signer-a", "signer-b"
	t.Setenv("ADMIN_MULTISIG_ADDRESSES", signerA+","+signerB)
	t.Setenv("ADMIN_MULTISIG_THRESHOLD", "1")

	if isAdminMultisigSigner("attacker") {
		t.Fatal("expected an unconfigured address to not be recognized as a multisig signer")
	}
	if !isAdminMultisigSigner(signerA) {
		t.Fatal("expected a configured address to be recognized as a multisig signer")
	}
}

func TestVerifyAdminMultisigTxAuth_OnlyAcceptsConfiguredSigners(t *testing.T) {
	resetApplyTestState()

	const signerA = "signer-a"
	t.Setenv("ADMIN_MULTISIG_ADDRESSES", signerA+",signer-b")
	t.Setenv("ADMIN_MULTISIG_THRESHOLD", "2")

	if !verifyAdminMultisigTxAuth(core.Transaction{AuthAddress: signerA}) {
		t.Fatal("expected a configured signer to pass verifyAdminMultisigTxAuth")
	}
	if verifyAdminMultisigTxAuth(core.Transaction{AuthAddress: "attacker"}) {
		t.Fatal("expected a non-configured address to fail verifyAdminMultisigTxAuth")
	}
}

// TestVerifyAdminTxAuth_RejectsDirectSubmissionOnceMultisigEnabled is
// the correctness-critical test for the whole feature: once multisig
// governs, none of the 9 admin action types may EVER take effect via
// a direct, single-signature submission again - not even one signed
// by an address that used to be (or still is, if left configured)
// the single ADMIN_WALLET_ADDRESS. Only executeAdminMultisigAction's
// internal, bypassing-this-check path may apply them.
func TestVerifyAdminTxAuth_RejectsDirectSubmissionOnceMultisigEnabled(t *testing.T) {
	resetApplyTestState()

	env, address := adminSignedEnvelope(t, map[string]int{"amount": 5})
	// Both an old single admin wallet AND multisig configured at once -
	// multisig must win, not just take priority for NEW requests but
	// actually reject this transaction type outright at apply time.
	t.Setenv("ADMIN_WALLET_ADDRESS", address)
	t.Setenv("ADMIN_MULTISIG_ADDRESSES", "signer-a,signer-b")
	t.Setenv("ADMIN_MULTISIG_THRESHOLD", "1")

	tx := core.NewAuthenticatedTransaction(env, "", "", 0, core.TxTokenMint, string(env.Payload))
	if verifyAdminTxAuth(tx) {
		t.Fatal("expected verifyAdminTxAuth to reject ANY direct submission of an admin action type once multisig is configured, even from the former single admin wallet")
	}
}

func TestRequireAdminAuthed_MultisigSupersedesSingleWalletTier(t *testing.T) {
	resetApplyTestState()

	// The OLD single admin wallet is still configured (simulating a
	// network mid-migration), but multisig is ALSO now configured -
	// a request signed by the old single wallet (which isn't one of
	// the multisig signers) must be rejected, not silently accepted
	// via the single-wallet tier.
	_, oldAdminAddress := adminSignedEnvelope(t, tokenMintData{Address: "x", Amount: 1})
	env, _ := adminSignedEnvelope(t, tokenMintData{Address: "x", Amount: 1})
	t.Setenv("ADMIN_WALLET_ADDRESS", oldAdminAddress)
	t.Setenv("ADMIN_MULTISIG_ADDRESSES", "signer-a,signer-b")
	t.Setenv("ADMIN_MULTISIG_THRESHOLD", "1")

	body, _ := json.Marshal(env)
	req := httptest.NewRequest("POST", "/token/mint", bytes.NewReader(body))

	var out tokenMintData
	_, mode, err := requireAdminAuthed(req, &out)
	if err == nil {
		t.Fatalf("expected a request signed by the old single admin wallet (not a multisig signer) to be rejected once multisig is configured, got mode=%v", mode)
	}
}
