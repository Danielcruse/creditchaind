package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestApplyVaultLockAttest_StethUsesDecimalStoreCorrectly mirrors
// TestApplyVaultLockAttest_UsdcUsesDecimalStoreCorrectly exactly - the
// STETH chain must mint STETH_CCC1 through the same threshold-attested
// lock-and-mint path ETH_CCC1/USDC_CCC1/BTC_CCC1 already use, with no
// new consensus-critical code of its own (see api/lido_stake.go's own
// doc comment).
func TestApplyVaultLockAttest_StethUsesDecimalStoreCorrectly(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-steth", "depositor-steth-pubkey"
	// 2.5 stETH at 18 decimals.
	attestLock("STETH", "0xstethtx", depositor, pubKey, "2500000000000000000", "val-a", 1000)
	attestLock("STETH", "0xstethtx", depositor, pubKey, "2500000000000000000", "val-b", 1001)

	depositAddr := resolveBalanceAddress(depositor, pubKey, "STETH_CCC1")
	if got := stethCcc1Store.BalanceOf(depositAddr); got != "2500000000000000000" {
		t.Fatalf("expected 2500000000000000000 smallest-unit stETH minted, got %s", got)
	}

	id := vaultLockAttestationID(vaultLockAttestData{
		Chain:              "STETH",
		ExternalTxHash:     "0xstethtx",
		DepositorCcAddress: depositor,
		DepositorPublicKey: pubKey,
		AmountSmallestUnit: "2500000000000000000",
	})
	attestation, ok := vaultLockAttestationStore.Get(id)
	if !ok || attestation.Status != core.VaultLockMinted {
		t.Fatalf("expected status MINTED, got %+v", attestation)
	}
}

// TestResolveBalanceAddress_StethCcc1UnifiesWithSelfCustodialEthAddress
// confirms STETH_CCC1 resolves to the SAME address ETH_CCC1/USDC_CCC1
// already unify with (a registered self-custodial ETH address) - not a
// separate derived hash, matching the "one real address for
// depositing/holding/sending" property those two already established
// (see api/subaddress.go's own doc comment for why this matters: a
// mismatch here would credit a real stETH deposit to an address the
// depositor's own balance lookups can never find).
func TestResolveBalanceAddress_StethCcc1UnifiesWithSelfCustodialEthAddress(t *testing.T) {
	resetApplyTestState()

	const ccAddress = "some-cc-address"
	const realEthAddress = "0x1234567890123456789012345678901234567890"
	ethAddressStore.Set(&core.EthWatchedAddress{CreditChainAddress: ccAddress, EthAddress: realEthAddress})

	gotEth := resolveBalanceAddress(ccAddress, "pubkey", "ETH_CCC1")
	gotSteth := resolveBalanceAddress(ccAddress, "pubkey", "STETH_CCC1")

	if gotEth != gotSteth {
		t.Fatalf("expected STETH_CCC1 to resolve to the same address as ETH_CCC1 (%s), got %s", gotEth, gotSteth)
	}
	if normalizeEthAddress(realEthAddress) != gotSteth {
		t.Fatalf("expected STETH_CCC1 to resolve to the registered self-custodial ETH address, got %s", gotSteth)
	}
}

// TestApplyVaultBurn_StethIsRejected confirms the deliberate v1
// limitation (staking is one-way for now, see HandleVaultBurn's own
// STETH check) actually holds at apply time too, not just the
// HTTP-handler fast-fail - a burn transaction that somehow reached
// apply with chain STETH must never burn anything or create a release
// approval.
func TestApplyVaultBurn_StethIsRejected(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerThreeBridgeValidators(t)

	const depositor, pubKey = "depositor-steth-2", "depositor-steth-2-pubkey"
	attestLock("STETH", "0xstethtx2", depositor, pubKey, "1000000000000000000", "val-a", 1000)
	attestLock("STETH", "0xstethtx2", depositor, pubKey, "1000000000000000000", "val-b", 1001)

	burnData, _ := json.Marshal(vaultBurnData{
		Chain:                 "STETH",
		DestinationExtAddress: "0xexternal",
		AmountSmallestUnit:    "1000000000000000000",
	})
	applyVaultBurn(core.Transaction{ID: "burn-steth", AuthAddress: depositor, AuthPublicKey: pubKey, Data: string(burnData), Timestamp: 1002})

	depositAddr := resolveBalanceAddress(depositor, pubKey, "STETH_CCC1")
	if got := stethCcc1Store.BalanceOf(depositAddr); got != "1000000000000000000" {
		t.Fatalf("expected the STETH_CCC1 balance to be untouched, got %s", got)
	}
	if _, ok := vaultReleaseApprovalStore.Get("burn-steth"); ok {
		t.Fatal("expected no release-approval record to be created for a rejected STETH burn")
	}
}

// TestHandleVaultBurn_StethRejectedWithClearError confirms the
// HTTP-layer fast-fail returns an explicit, actionable 400 instead of
// silently accepting the request and no-op'ing at apply time.
func TestHandleVaultBurn_StethRejectedWithClearError(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	body := buildSignedEnvelopeJSON(t, pub, priv, vaultBurnData{
		Chain:                 "STETH",
		DestinationExtAddress: "0xexternal",
		AmountSmallestUnit:    "1000000000000000000",
	})
	req := httptest.NewRequest(http.MethodPost, "/vault/burn", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	HandleVaultBurn(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a STETH withdrawal attempt, got %d: %s", rec.Code, rec.Body.String())
	}
}
