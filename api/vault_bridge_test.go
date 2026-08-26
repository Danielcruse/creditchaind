package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestBridgeValidatorSet_IsSeparateFromAdminMultisig confirms the
// deliberate design decision (core/bridge_validator.go's doc comment):
// an address configured as an admin-multisig signer is NOT
// automatically a bridge validator, and vice versa - these are two
// independent registries even though the same operator could staff both.
// Bridge-validator eligibility is now PoSR-only (the old
// BRIDGE_VALIDATOR_ADDRESSES allowlist was removed entirely,
// 2026-08-20), so "bridge-only"/"shared-addr" are made bridge-eligible
// by being real PoSR validators instead.
func TestBridgeValidatorSet_IsSeparateFromAdminMultisig(t *testing.T) {
	resetApplyTestState()

	t.Setenv("ADMIN_MULTISIG_ADDRESSES", "shared-addr,admin-only")
	t.Setenv("ADMIN_MULTISIG_THRESHOLD", "1")

	makeRealPoSRValidator("shared-addr")
	makeRealPoSRValidator("bridge-only")

	if !isAdminMultisigSigner("admin-only") || isBridgeValidatorAddress("admin-only") {
		t.Fatal("expected admin-only signer to be an admin multisig signer but NOT a bridge validator")
	}
	if !isBridgeValidatorAddress("bridge-only") || isAdminMultisigSigner("bridge-only") {
		t.Fatal("expected bridge-only address to be a bridge validator but NOT an admin multisig signer")
	}
	if !isAdminMultisigSigner("shared-addr") || !isBridgeValidatorAddress("shared-addr") {
		t.Fatal("expected an address configured in both roles to be recognized in both")
	}
}

func TestApplyBridgeValidatorRegister_RejectsUnauthorizedAddress(t *testing.T) {
	resetApplyTestState()
	t.Setenv("BRIDGE_VALIDATOR_THRESHOLD", "1")
	makeRealPoSRValidator("validator-a")

	data, _ := json.Marshal(bridgeValidatorRegisterData{
		EthSigningAddress: "0x1111111111111111111111111111111111111a",
		BtcPublicKeyHex:   "02" + repeatHex(64),
	})
	applyBridgeValidatorRegister(core.Transaction{AuthAddress: "attacker", Data: string(data), Timestamp: 1000})

	if _, ok := bridgeValidatorStore.Get("attacker"); ok {
		t.Fatal("expected an address that is not a real, active, sufficiently-staked PoSR validator to never be registered")
	}
}

func TestApplyBridgeValidatorRegister_HappyPath(t *testing.T) {
	resetApplyTestState()
	t.Setenv("BRIDGE_VALIDATOR_THRESHOLD", "1")
	makeRealPoSRValidator("validator-a")

	ethAddr := "0x" + repeatHex(39) + "a"
	btcPub := "02" + repeatHex(64)
	data, _ := json.Marshal(bridgeValidatorRegisterData{EthSigningAddress: ethAddr, BtcPublicKeyHex: btcPub})
	applyBridgeValidatorRegister(core.Transaction{AuthAddress: "validator-a", Data: string(data), Timestamp: 1000})

	v, ok := bridgeValidatorStore.Get("validator-a")
	if !ok {
		t.Fatal("expected the registration to be recorded")
	}
	if v.EthSigningAddress != ethAddr || v.BtcPublicKeyHex != btcPub {
		t.Fatalf("expected declared keys to be stored verbatim, got %+v", v)
	}
}

func repeatHex(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'a'
	}
	return string(out)
}

// registerThreeBridgeValidators is a test helper wiring up a 3-signer,
// 2-of-3 bridge-validator set - the shape every lock/release threshold
// test below needs. Eligibility is PoSR-only (no allowlist), so val-a/
// val-b/val-c are made real, active, sufficiently-staked PoSR
// validators directly.
func registerThreeBridgeValidators(t *testing.T) {
	t.Helper()
	t.Setenv("BRIDGE_VALIDATOR_THRESHOLD", "2")
	makeRealPoSRValidator("val-a")
	makeRealPoSRValidator("val-b")
	makeRealPoSRValidator("val-c")
}

// advancePastVaultReleaseConfirmationDepth sets blockchain to a chain
// core.VaultReleaseConfirmationDepthBlocks taller than whatever height
// it's already at - call this AFTER a burn (which stamps
// BurnBlockHeight at whatever height was current when it applied) and
// BEFORE submitting a release approval, for tests that only care about
// signature-collection/threshold behavior rather than the confirmation-
// depth gate itself (which has its own dedicated test).
func advancePastVaultReleaseConfirmationDepth(t *testing.T) {
	t.Helper()
	target := currentChainHeight() + core.VaultReleaseConfirmationDepthBlocks
	blocks := make([]core.Block, target+1)
	for i := range blocks {
		blocks[i].Index = uint64(i)
	}
	blockchain = &core.Blockchain{Blocks: blocks}
	t.Cleanup(func() { blockchain = nil })
}

func attestLock(chain, txHash, depositor, pubKey, amount, validator string, ts int64) {
	data, _ := json.Marshal(vaultLockAttestData{
		Chain:              chain,
		ExternalTxHash:     txHash,
		DepositorCcAddress: depositor,
		DepositorPublicKey: pubKey,
		AmountSmallestUnit: amount,
	})
	applyVaultLockAttest(core.Transaction{AuthAddress: validator, Data: string(data), Timestamp: ts})
}

func TestApplyVaultLockAttest_MintsExactlyOnceAtThreshold(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-1", "depositor-1-pubkey"

	attestLock("ETH", "0xdeadbeef", depositor, pubKey, "1000000000000000000", "val-a", 1000)
	depositAddr := resolveBalanceAddress(depositor, pubKey, "ETH_CCC1")
	if got := ethCcc1Store.BalanceOf(depositAddr); got != "0" {
		t.Fatalf("expected no mint yet with only 1 of 2 required attestations, got %s", got)
	}

	attestLock("ETH", "0xdeadbeef", depositor, pubKey, "1000000000000000000", "val-b", 1001)
	if got := ethCcc1Store.BalanceOf(depositAddr); got != "1000000000000000000" {
		t.Fatalf("expected exactly 1 ETH (in wei) minted once threshold (2) was reached, got %s", got)
	}

	id := vaultLockAttestationID(vaultLockAttestData{
		Chain:              "ETH",
		ExternalTxHash:     "0xdeadbeef",
		DepositorCcAddress: depositor,
		DepositorPublicKey: pubKey,
		AmountSmallestUnit: "1000000000000000000",
	})
	attestation, _ := vaultLockAttestationStore.Get(id)
	if attestation.Status != core.VaultLockMinted {
		t.Fatalf("expected status MINTED, got %s", attestation.Status)
	}

	// A late 3rd attestation (or a resubmitted one from val-a) must
	// never double-mint.
	attestLock("ETH", "0xdeadbeef", depositor, pubKey, "1000000000000000000", "val-c", 1002)
	attestLock("ETH", "0xdeadbeef", depositor, pubKey, "1000000000000000000", "val-a", 1003)
	if got := ethCcc1Store.BalanceOf(depositAddr); got != "1000000000000000000" {
		t.Fatalf("expected balance to stay at 1 ETH after late/duplicate attestations, got %s", got)
	}
}

func TestApplyVaultLockAttest_SameValidatorAttestingTwiceDoesNotDoubleCount(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-2", "depositor-2-pubkey"
	attestLock("BTC", "btctxid1", depositor, pubKey, "500000", "val-a", 1000)
	attestLock("BTC", "btctxid1", depositor, pubKey, "500000", "val-a", 1001) // same validator again

	id := vaultLockAttestationID(vaultLockAttestData{
		Chain:              "BTC",
		ExternalTxHash:     "btctxid1",
		DepositorCcAddress: depositor,
		DepositorPublicKey: pubKey,
		AmountSmallestUnit: "500000",
	})
	attestation, _ := vaultLockAttestationStore.Get(id)
	if len(attestation.Attestors) != 1 {
		t.Fatalf("expected exactly 1 distinct attestor after val-a attests twice, got %d", len(attestation.Attestors))
	}

	depositAddr := resolveBalanceAddress(depositor, pubKey, "BTC_CCC1")
	if got := btcCcc1Store.BalanceOf(depositAddr); got != 0 {
		t.Fatalf("expected no mint with only 1 distinct attestor against a threshold of 2, got %d", got)
	}
}

func TestApplyVaultLockAttest_UnauthorizedValidatorCannotAttest(t *testing.T) {
	resetApplyTestState()
	registerThreeBridgeValidators(t)

	attestLock("ETH", "0xabc", "depositor-3", "pubkey-3", "1", "not-a-validator", 1000)

	id := vaultLockAttestationID(vaultLockAttestData{
		Chain:              "ETH",
		ExternalTxHash:     "0xabc",
		DepositorCcAddress: "depositor-3",
		DepositorPublicKey: "pubkey-3",
		AmountSmallestUnit: "1",
	})
	if _, ok := vaultLockAttestationStore.Get(id); ok {
		t.Fatal("expected an attestation from an unauthorized address to create no record at all")
	}
}

func TestApplyVaultLockAttest_UsdcUsesDecimalStoreCorrectly(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-4", "depositor-4-pubkey"
	// 1234.56 USDC at 6 decimals = 1234560000 smallest units.
	attestLock("USDC", "0xusdctx", depositor, pubKey, "1234560000", "val-a", 1000)
	attestLock("USDC", "0xusdctx", depositor, pubKey, "1234560000", "val-b", 1001)

	depositAddr := resolveBalanceAddress(depositor, pubKey, "USDC_CCC1")
	if got := usdcCcc1Store.BalanceOf(depositAddr); got != "1234560000" {
		t.Fatalf("expected 1234560000 smallest-unit USDC minted, got %s", got)
	}
}

func TestVaultBurnAndReleaseApprove_SignatureCollectionAndExactlyOnceReadyState(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerThreeBridgeValidators(t)

	// Seed a burnable ETH_CCC1 balance the same way a real lock would.
	const depositor, pubKey = "depositor-5", "depositor-5-pubkey"
	attestLock("ETH", "0xseed", depositor, pubKey, "2000000000000000000", "val-a", 1000)
	attestLock("ETH", "0xseed", depositor, pubKey, "2000000000000000000", "val-b", 1001)

	burnData, _ := json.Marshal(vaultBurnData{
		Chain:                 "ETH",
		DestinationExtAddress: "0xexternal",
		AmountSmallestUnit:    "2000000000000000000",
	})
	burnTx := core.Transaction{ID: "burn-tx-1", AuthAddress: depositor, AuthPublicKey: pubKey, Data: string(burnData), Timestamp: 1002}
	applyVaultBurn(burnTx)

	depositAddr := resolveBalanceAddress(depositor, pubKey, "ETH_CCC1")
	if got := ethCcc1Store.BalanceOf(depositAddr); got != "0" {
		t.Fatalf("expected the full balance to be burned, got %s remaining", got)
	}

	approval, ok := vaultReleaseApprovalStore.Get("burn-tx-1")
	if !ok {
		t.Fatal("expected a release-approval record to be opened on a successful burn")
	}
	if approval.Status != core.VaultReleasePending {
		t.Fatalf("expected status PENDING immediately after burn, got %s", approval.Status)
	}

	advancePastVaultReleaseConfirmationDepth(t)

	sig1 := hex.EncodeToString([]byte("signature-from-val-a"))
	sig2 := hex.EncodeToString([]byte("signature-from-val-b"))

	approveData1, _ := json.Marshal(vaultReleaseApproveData{BurnTxId: "burn-tx-1", SignatureHex: sig1})
	applyVaultReleaseApprove(core.Transaction{AuthAddress: "val-a", Data: string(approveData1), Timestamp: 1003})

	approval, _ = vaultReleaseApprovalStore.Get("burn-tx-1")
	if len(approval.Signatures) != 1 {
		t.Fatalf("expected 1 signature after val-a approves, got %d", len(approval.Signatures))
	}
	// Real, irreversible external release is Phase B/C's job (a
	// node-local relay), not this consensus-critical apply function -
	// status must stay PENDING even once signatures exist.
	if approval.Status != core.VaultReleasePending {
		t.Fatalf("expected status to remain PENDING after 1 of 2 signatures (release execution is a separate, later phase), got %s", approval.Status)
	}

	approveData2, _ := json.Marshal(vaultReleaseApproveData{BurnTxId: "burn-tx-1", SignatureHex: sig2})
	applyVaultReleaseApprove(core.Transaction{AuthAddress: "val-b", Data: string(approveData2), Timestamp: 1004})

	approval, _ = vaultReleaseApprovalStore.Get("burn-tx-1")
	if len(approval.Signatures) != 2 {
		t.Fatalf("expected 2 distinct signatures once threshold reached, got %d", len(approval.Signatures))
	}
	if approval.Status != core.VaultReleasePending {
		t.Fatalf("expected Phase A to leave status PENDING even at threshold - a node-local relay (Phase B/C) owns flipping it to RELEASED, got %s", approval.Status)
	}
}

// TestApplyVaultReleaseApprove_RejectsSignatureBeforeConfirmationDepth is
// the core regression test for the 2026-08-20 confirmation-depth
// requirement (core.VaultReleaseConfirmationDepthBlocks): a bridge
// validator's signature must be rejected (not recorded at all) until
// the burn is buried under enough further CreditChain blocks, and must
// be accepted once it is.
func TestApplyVaultReleaseApprove_RejectsSignatureBeforeConfirmationDepth(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-depth", "depositor-depth-pubkey"
	attestLock("ETH", "0xseed-depth", depositor, pubKey, "1000000000000000000", "val-a", 1000)
	attestLock("ETH", "0xseed-depth", depositor, pubKey, "1000000000000000000", "val-b", 1001)

	burnData, _ := json.Marshal(vaultBurnData{
		Chain:                 "ETH",
		DestinationExtAddress: "0xexternal",
		AmountSmallestUnit:    "1000000000000000000",
	})
	applyVaultBurn(core.Transaction{ID: "burn-depth", AuthAddress: depositor, AuthPublicKey: pubKey, Data: string(burnData), Timestamp: 1002})

	sig := hex.EncodeToString([]byte("signature-from-val-a"))
	approveData, _ := json.Marshal(vaultReleaseApproveData{BurnTxId: "burn-depth", SignatureHex: sig})

	// No blocks built on top yet (blockchain is nil in this test, so
	// currentChainHeight() reports 0 both at burn time and now) -
	// the signature must be rejected outright, not just deferred.
	applyVaultReleaseApprove(core.Transaction{AuthAddress: "val-a", Data: string(approveData), Timestamp: 1003})
	approval, _ := vaultReleaseApprovalStore.Get("burn-depth")
	if len(approval.Signatures) != 0 {
		t.Fatalf("expected the signature to be rejected before the burn is confirmed deep enough, got %d recorded", len(approval.Signatures))
	}

	// Buried, but one block short of the required depth - still rejected.
	blockchain = &core.Blockchain{Blocks: make([]core.Block, core.VaultReleaseConfirmationDepthBlocks)}
	for i := range blockchain.Blocks {
		blockchain.Blocks[i].Index = uint64(i)
	}
	t.Cleanup(func() { blockchain = nil })
	applyVaultReleaseApprove(core.Transaction{AuthAddress: "val-a", Data: string(approveData), Timestamp: 1004})
	approval, _ = vaultReleaseApprovalStore.Get("burn-depth")
	if len(approval.Signatures) != 0 {
		t.Fatalf("expected the signature to still be rejected one block short of the required depth, got %d recorded", len(approval.Signatures))
	}

	// Now deep enough - must be accepted.
	advancePastVaultReleaseConfirmationDepth(t)
	applyVaultReleaseApprove(core.Transaction{AuthAddress: "val-a", Data: string(approveData), Timestamp: 1005})
	approval, _ = vaultReleaseApprovalStore.Get("burn-depth")
	if len(approval.Signatures) != 1 {
		t.Fatalf("expected the signature to be accepted once the burn is confirmed deep enough, got %d recorded", len(approval.Signatures))
	}
}

// TestVaultReleaseConfirmationsRemaining_TracksChainHeight confirms
// vaultReleaseConfirmationsRemaining - the shared helper
// HandleVaultReleaseApprove, applyVaultReleaseApprove, and the auto-sign
// watch loop all gate on - correctly reports full depth remaining right
// after a burn and 0 once the chain has advanced past it.
func TestVaultReleaseConfirmationsRemaining_TracksChainHeight(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-depth-http", "depositor-depth-http-pubkey"
	attestLock("ETH", "0xseed-depth-http", depositor, pubKey, "1000000000000000000", "val-a", 1000)
	attestLock("ETH", "0xseed-depth-http", depositor, pubKey, "1000000000000000000", "val-b", 1001)

	burnData, _ := json.Marshal(vaultBurnData{
		Chain:                 "ETH",
		DestinationExtAddress: "0xexternal",
		AmountSmallestUnit:    "1000000000000000000",
	})
	applyVaultBurn(core.Transaction{ID: "burn-depth-http", AuthAddress: depositor, AuthPublicKey: pubKey, Data: string(burnData), Timestamp: 1002})

	approval, ok := vaultReleaseApprovalStore.Get("burn-depth-http")
	if !ok {
		t.Fatal("expected a release-approval record to be opened on a successful burn")
	}
	if remaining := vaultReleaseConfirmationsRemaining(approval); remaining != core.VaultReleaseConfirmationDepthBlocks {
		t.Fatalf("expected full confirmation depth still remaining right after burn, got %d", remaining)
	}

	advancePastVaultReleaseConfirmationDepth(t)
	if remaining := vaultReleaseConfirmationsRemaining(approval); remaining != 0 {
		t.Fatalf("expected 0 confirmations remaining once the chain has advanced past the required depth, got %d", remaining)
	}
}

func TestApplyVaultBurn_InsufficientBalanceIsANoOp(t *testing.T) {
	resetApplyTestState()
	registerThreeBridgeValidators(t)

	burnData, _ := json.Marshal(vaultBurnData{
		Chain:                 "ETH",
		DestinationExtAddress: "0xexternal",
		AmountSmallestUnit:    "999999999999999999999",
	})
	applyVaultBurn(core.Transaction{ID: "burn-underfunded", AuthAddress: "poor-depositor", AuthPublicKey: "pubkey", Data: string(burnData), Timestamp: 1000})

	if _, ok := vaultReleaseApprovalStore.Get("burn-underfunded"); ok {
		t.Fatal("expected no release-approval record to be created when the burn itself fails for insufficient balance")
	}
}

func TestDecimalTokenStore_RoundTripsAt18And6Decimals(t *testing.T) {
	store18 := core.NewDecimalTokenStore(18)
	if err := store18.Mint("addr-1", "1000000000000000000"); err != nil {
		t.Fatalf("mint failed: %v", err)
	}
	if got := store18.BalanceOf("addr-1"); got != "1000000000000000000" {
		t.Fatalf("expected 1e18 smallest units, got %s", got)
	}
	if !store18.Transfer("addr-1", "addr-2", "400000000000000000") {
		t.Fatal("expected transfer to succeed")
	}
	if got := store18.BalanceOf("addr-1"); got != "600000000000000000" {
		t.Fatalf("expected 0.6 remaining (in smallest units), got %s", got)
	}
	if got := store18.BalanceOf("addr-2"); got != "400000000000000000" {
		t.Fatalf("expected 0.4 received (in smallest units), got %s", got)
	}

	store6 := core.NewDecimalTokenStore(6)
	if err := store6.Mint("addr-3", "1234560000"); err != nil {
		t.Fatalf("mint failed: %v", err)
	}
	if !store6.Burn("addr-3", "560000") {
		t.Fatal("expected burn to succeed")
	}
	if got := store6.BalanceOf("addr-3"); got != "1234000000" {
		t.Fatalf("expected 1234.0 USDC remaining (in smallest units), got %s", got)
	}
	if got := store6.TotalSupply; got != "1234000000" {
		t.Fatalf("expected TotalSupply to track the burn, got %s", got)
	}

	// Insufficient balance must fail cleanly, never go negative.
	if store6.Burn("addr-3", "999999999999") {
		t.Fatal("expected burn to fail on insufficient balance")
	}
	if store6.Transfer("addr-3", "addr-4", "999999999999") {
		t.Fatal("expected transfer to fail on insufficient balance")
	}
}
