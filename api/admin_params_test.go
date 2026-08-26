package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// setGovernanceParamForTest overrides a governanceParamsStore key for
// the duration of the test only. governanceParamsStore is package-level
// state that resetApplyTestState does NOT reset (other tests rely on
// its startup-seeded defaults surviving across the whole test binary
// run), so a bare Set here would leak into whichever test happens to
// run next - snapshot-then-restore via t.Cleanup keeps this override
// scoped to just this test.
//
// Uses SetAt (with time.Now(), always "fresh") rather than plain Set so
// staleness-gated reads (EthUsdCentsPerEth via collateralValueInYen -
// see core.GovernanceParamsStore.IsStale) see this value as live, not
// unset/stale - a test calling this helper is asserting "this value IS
// set" and shouldn't also have to separately think about staleness.
// Restores whatever SetAt timestamp (if any) existed before, not just
// the value, so this can't leak a false "was fresh" signal into a test
// that runs after this one and expects the key to read as never-SetAt.
func setGovernanceParamForTest(t *testing.T, key string, value uint64) {
	t.Helper()
	before := governanceParamsStore.Snapshot()
	beforeSetAt := governanceParamsStore.SnapshotSetAt()
	t.Cleanup(func() {
		governanceParamsStore.Restore(before)
		governanceParamsStore.RestoreSetAt(beforeSetAt)
	})
	governanceParamsStore.SetAt(key, value, time.Now().Unix())
}

func TestApplyAdminSetParam_AllowlistedKeySucceeds(t *testing.T) {
	resetApplyTestState()
	before := governanceParamsStore.Snapshot()
	t.Cleanup(func() { governanceParamsStore.Restore(before) })

	data, _ := json.Marshal(adminSetParamData{Key: "EthUsdCentsPerEth", Value: 12345})
	applyAdminSetParam(core.Transaction{ID: "admin-param-1", Data: string(data)})

	got, ok := governanceParamsStore.Get("EthUsdCentsPerEth")
	if !ok || got != 12345 {
		t.Fatalf("expected EthUsdCentsPerEth set to 12345, got %d (ok=%v)", got, ok)
	}
}

// TestApplyAdminSetParam_RejectsUnknownKey confirms the apply-side
// allowlist check is real, not just a fast-fail at the HTTP layer - a
// hand-crafted transaction that bypasses HandleAdminSetParam entirely
// must still be rejected here, since this is the actual consensus-
// applied security boundary (see applyAdminSetParam's own doc comment).
func TestApplyAdminSetParam_RejectsUnknownKey(t *testing.T) {
	resetApplyTestState()
	before := governanceParamsStore.Snapshot()
	t.Cleanup(func() { governanceParamsStore.Restore(before) })

	data, _ := json.Marshal(adminSetParamData{Key: "SlashBps", Value: 9999})
	applyAdminSetParam(core.Transaction{ID: "admin-param-2", Data: string(data)})

	if _, ok := governanceParamsStore.Get("SlashBps"); ok {
		t.Fatal("expected a non-allowlisted key to be silently rejected, never reaching governanceParamsStore.Set")
	}
}

func TestHandleAdminSetParam_RejectsUnknownKeyAtHTTPLayer(t *testing.T) {
	resetApplyTestState()

	env, address := adminSignedEnvelope(t, adminSetParamData{Key: "GovernanceQuorum", Value: 1})
	t.Setenv("ADMIN_WALLET_ADDRESS", address)

	body, _ := json.Marshal(env)
	req := httptest.NewRequest(http.MethodPost, "/admin/params/set", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleAdminSetParam(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a non-allowlisted key, got %d", w.Code)
	}
	if _, ok := governanceParamsStore.Get("GovernanceQuorum"); ok {
		t.Fatal("expected the param to remain unset after a rejected request")
	}
}

func TestHandleAdminSetParam_RejectsNonAdminCaller(t *testing.T) {
	resetApplyTestState()
	os.Unsetenv("ADMIN_WALLET_ADDRESS")
	os.Unsetenv("ADMIN_TOKEN")

	// A genuinely valid signature - just from nobody the network trusts
	// as admin. requireAdminAuthed must reject this outright before
	// the allowlist check is ever reached.
	env, _ := adminSignedEnvelope(t, adminSetParamData{Key: "UnbondingPeriodSeconds", Value: 100})
	body, _ := json.Marshal(env)
	req := httptest.NewRequest(http.MethodPost, "/admin/params/set", bytes.NewReader(body))
	w := httptest.NewRecorder()

	HandleAdminSetParam(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-admin caller, got %d", w.Code)
	}
	if _, ok := governanceParamsStore.Get("UnbondingPeriodSeconds"); ok {
		t.Fatal("expected the param to remain unset after a rejected request")
	}
}

// TestApplyUnstake_UnbondingPeriodFallsBackThenRespectsAdminOverride
// confirms UnbondingPeriodSeconds's read site (applyUnstake) is
// byte-identical to today's hardcoded behavior when nothing has been
// admin-set, and correctly picks up an override once one is set - the
// "testnet's own default path is provably unchanged" guarantee the
// mainnet-transition plan calls for.
func TestApplyUnstake_UnbondingPeriodFallsBackThenRespectsAdminOverride(t *testing.T) {
	resetApplyTestState()

	pub, _, _ := core.GenerateKeyPair()
	addr := core.GenerateAddress(pub)
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": 500}

	data, _ := json.Marshal(stakeData{Symbol: "CCT"})
	tx := core.Transaction{ID: "unstake-1", From: addr, AuthPublicKey: hex.EncodeToString(pub), Amount: 100, Timestamp: 1000, Data: string(data)}
	applyUnstake(tx)

	entries := stakingStore.UnbondingFor(addr)
	if len(entries) != 1 {
		t.Fatalf("expected exactly one unbonding entry, got %d", len(entries))
	}
	wantDefault := tx.Timestamp + core.UnbondingPeriodSeconds
	if entries[0].MaturesAt != wantDefault {
		t.Fatalf("expected default MaturesAt %d (no override set), got %d", wantDefault, entries[0].MaturesAt)
	}

	setGovernanceParamForTest(t, "UnbondingPeriodSeconds", 999999)
	tx2 := core.Transaction{ID: "unstake-2", From: addr, AuthPublicKey: hex.EncodeToString(pub), Amount: 100, Timestamp: 2000, Data: string(data)}
	applyUnstake(tx2)

	entries = stakingStore.UnbondingFor(addr)
	if len(entries) != 2 {
		t.Fatalf("expected two unbonding entries after the second unstake, got %d", len(entries))
	}
	wantOverride := tx2.Timestamp + 999999
	if entries[1].MaturesAt != wantOverride {
		t.Fatalf("expected overridden MaturesAt %d, got %d", wantOverride, entries[1].MaturesAt)
	}
}

// TestProcessDormantWallets_DormancyPeriodFallsBackThenRespectsAdminOverride
// mirrors the unbonding test above for DormancyPeriodSeconds's read
// site (processDormantWallets) - same "provably unchanged unless
// explicitly overridden" guarantee.
func TestProcessDormantWallets_DormancyPeriodFallsBackThenRespectsAdminOverride(t *testing.T) {
	resetApplyTestState()

	dormantAddr := "dormant-wallet"
	recipientAddr := "recipient-wallet"
	walletStore.Wallets[dormantAddr] = &core.Wallet{Address: dormantAddr, LastActivityAt: 50}
	walletStore.Wallets[recipientAddr] = &core.Wallet{Address: recipientAddr, LastActivityAt: 95}
	tokenStore.Mint(dormantAddr, 100)

	// asOf=100, LastActivityAt=50 -> 50s elapsed. Default
	// DormancyPeriodSeconds (300s) means this wallet is NOT dormant yet
	// - confirms the default path is unchanged from today's hardcoded
	// behavior with no admin override present.
	processDormantWallets(100)
	if got := tokenStore.BalanceOf(dormantAddr); got != 100 {
		t.Fatalf("expected no sweep under the default 300s dormancy period, balance changed to %d", got)
	}

	// Override to a much shorter period - the same 50s-elapsed wallet
	// is now dormant, and the recipient (5s elapsed) still qualifies to
	// receive the reclaimed balance.
	setGovernanceParamForTest(t, "DormancyPeriodSeconds", 40)
	processDormantWallets(100)

	if got := tokenStore.BalanceOf(dormantAddr); got != 0 {
		t.Fatalf("expected dormant wallet's CCT fully reclaimed under the 40s override, got %d remaining", got)
	}
	if got := tokenStore.BalanceOf(recipientAddr); got != 100 {
		t.Fatalf("expected recipient to receive the reclaimed 100 CCT, got %d", got)
	}
}

// TestApplyBridgeDeposit_AccountHoldingsCapCreditsPartiallyAndTracksExcess
// confirms the per-account TOTAL BTCB+ETHB holdings cap mechanism (see
// api/account_holdings_cap.go, applyBridgeDeposit's doc comment in
// api/apply.go): once an account's total USD-value holdings reach the
// cap, further deposits credit only the remaining headroom and the
// excess accumulates (not overwrites) in UncreditedExcessWei across
// multiple deposits, while LastCreditedWei still advances to the FULL
// observed balance so the same excess is never re-flagged.
//
// EthUsdCentsPerEth is deliberately set to weiPerEth (1e18) so USD
// cents and wei are numerically identical in this test, keeping the
// arithmetic easy to verify by hand without changing what's actually
// being exercised.
func TestApplyBridgeDeposit_AccountHoldingsCapCreditsPartiallyAndTracksExcess(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerCurrentNodeAsAuthorizedValidator(t)
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 1_000_000_000_000_000_000)
	setGovernanceParamForTest(t, "BtcUsdCentsPerBtc", 100_000_000)
	setGovernanceParamForTest(t, "AccountHoldingsCapUsdCents", 2500)

	ccAddress, _, ethbAddress := setupCustodialAccount(t, "0xdeadbeef00000000000000000000000000dead")

	// First deposit: observed balance 1500 wei (=1500 cents at this
	// rate), well under the 2500-cent cap -> credited in full, no
	// excess.
	tx := buildBridgeDepositTx("cap-dep-1", ccAddress, "1500")
	applyTxOnce(tx)

	if got := bridgedEthStore.BalanceOf(ethbAddress); got != 1500 {
		t.Fatalf("expected the full 1500 wei credited (under the account cap), got %d", got)
	}
	account, ok := ethCustodialStore.Get(ccAddress)
	if !ok {
		t.Fatal("expected custodial account to still exist")
	}
	if account.UncreditedExcessWei != "" {
		t.Fatalf("expected no excess yet, got %q", account.UncreditedExcessWei)
	}

	// Second deposit: observed balance rises to 2700 (delta 1200 over
	// the previous 1500 baseline). Current holdings are already 1500
	// cents, so headroom under the 2500 cap is only 1000 -> credit
	// 1000 more (running ETHB total 2500, exactly at the cap), excess
	// 200 tracked.
	tx2 := buildBridgeDepositTx("cap-dep-2", ccAddress, "2700")
	applyTxOnce(tx2)

	if got := bridgedEthStore.BalanceOf(ethbAddress); got != 2500 {
		t.Fatalf("expected ETHB credited up to the account cap (2500), got %d", got)
	}
	account, _ = ethCustodialStore.Get(ccAddress)
	if account.UncreditedExcessWei != "200" {
		t.Fatalf("expected UncreditedExcessWei 200, got %q", account.UncreditedExcessWei)
	}
	if account.LastCreditedWei != "2700" {
		t.Fatalf("expected LastCreditedWei to advance to the FULL observed balance 2700, got %q", account.LastCreditedWei)
	}
}

// TestApplyBridgeDeposit_CappedByExistingBtcbHoldings confirms the cap
// is genuinely a per-account TOTAL across BTCB+ETHB, not a separate
// per-symbol cap - an account that already holds BTCB value has less
// ETHB headroom available, even though this deposit is entirely an ETH
// one.
func TestApplyBridgeDeposit_CappedByExistingBtcbHoldings(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerCurrentNodeAsAuthorizedValidator(t)
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 1_000_000_000_000_000_000)
	setGovernanceParamForTest(t, "BtcUsdCentsPerBtc", 100_000_000)
	setGovernanceParamForTest(t, "AccountHoldingsCapUsdCents", 2500)

	ccAddress, pubHex, ethbAddress := setupCustodialAccount(t, "0xaaaaaaaa00000000000000000000000000aaaa")
	pub, _ := hex.DecodeString(pubHex)
	btcbAddress := core.DeriveTokenAddress(pub, core.BTCBAddressTag)

	// Pre-existing BTCB holdings worth 2000 cents (at the 1:1 test
	// rate) - already consumes most of the 2500-cent account cap
	// before any ETH deposit happens.
	bridgedBtcStore.Mint(btcbAddress, 2000)

	tx := buildBridgeDepositTx("cross-asset-dep-1", ccAddress, "1000")
	applyTxOnce(tx)

	if got := bridgedEthStore.BalanceOf(ethbAddress); got != 500 {
		t.Fatalf("expected only 500 wei credited (2500 cap - 2000 already-held BTCB value), got %d", got)
	}
	account, _ := ethCustodialStore.Get(ccAddress)
	if account.UncreditedExcessWei != "500" {
		t.Fatalf("expected UncreditedExcessWei 500 (1000 delta - 500 credited), got %q", account.UncreditedExcessWei)
	}
}

// TestApplyBridgeDeposit_NoRatesConfiguredIsUnlimited confirms that
// with neither EthUsdCentsPerEth nor BtcUsdCentsPerBtc ever admin-set,
// the account-holdings cap can't be evaluated at all and every deposit
// is credited in full - the cap mechanism must be provably opt-in, not
// silently freeze deposits the moment an admin forgets to configure a
// rate.
func TestApplyBridgeDeposit_NoRatesConfiguredIsUnlimited(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerCurrentNodeAsAuthorizedValidator(t)
	// Deliberately no override - EthUsdCentsPerEth/BtcUsdCentsPerBtc
	// both stay unset.

	ccAddress, _, ethbAddress := setupCustodialAccount(t, "0xfeedface00000000000000000000000000feed")

	tx := buildBridgeDepositTx("uncapped-dep-1", ccAddress, "999999999999999999")
	applyTxOnce(tx)

	if got := bridgedEthStore.BalanceOf(ethbAddress); got != 999999999999999999 {
		t.Fatalf("expected the full deposit credited with no cap set, got %d", got)
	}
	account, _ := ethCustodialStore.Get(ccAddress)
	if account.UncreditedExcessWei != "" {
		t.Fatalf("expected no excess tracked with no cap set, got %q", account.UncreditedExcessWei)
	}
}

// TestApplyBridgeBtcDeposit_AccountHoldingsCapCreditsPartiallyAndTracksExcess
// is the BTC-side mirror of
// TestApplyBridgeDeposit_AccountHoldingsCapCreditsPartiallyAndTracksExcess
// - same per-account TOTAL BTCB+ETHB USD-holdings cap mechanism, uint64
// sats arithmetic instead of big.Int wei. BtcUsdCentsPerBtc is set to
// satsPerBtc (1e8) so USD cents and sats are numerically identical in
// this test.
func TestApplyBridgeBtcDeposit_AccountHoldingsCapCreditsPartiallyAndTracksExcess(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerCurrentNodeAsAuthorizedValidator(t)
	setGovernanceParamForTest(t, "EthUsdCentsPerEth", 1_000_000_000_000_000_000)
	setGovernanceParamForTest(t, "BtcUsdCentsPerBtc", 100_000_000)
	setGovernanceParamForTest(t, "AccountHoldingsCapUsdCents", 25_000)

	pub, _, _ := core.GenerateKeyPair()
	ccAddress := core.GenerateAddress(pub)
	pubHex := hex.EncodeToString(pub)
	btcAddressStore.Set(&core.BtcWatchedAddress{
		CreditChainAddress:   ccAddress,
		CreditChainPublicKey: pubHex,
		BtcAddress:           "tb1qcaptest0000000000000000000000000000000",
	})
	btcbAddress := resolveBalanceAddress(ccAddress, pubHex, "BTCB")

	buildDepositTx := func(id string, newBalanceSats uint64) core.Transaction {
		data, _ := json.Marshal(btcBridgeDepositReportData{
			CreditChainAddress: ccAddress,
			NewBalanceSats:     newBalanceSats,
		})
		env := selfSignedEnvelope(data)
		return core.Transaction{
			ID:            id,
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
	}

	// First deposit: 15,000 sats observed (=15,000 cents at this rate),
	// well under the 25,000-cent cap -> credited in full, no excess.
	applyTxOnce(buildDepositTx("btc-cap-dep-1", 15_000))
	if got := bridgedBtcStore.BalanceOf(btcbAddress); got != 15_000 {
		t.Fatalf("expected the full 15000 sats credited (under the account cap), got %d", got)
	}
	watched, ok := btcAddressStore.Get(ccAddress)
	if !ok {
		t.Fatal("expected watched address to still exist")
	}
	if watched.UncreditedExcessSats != 0 {
		t.Fatalf("expected no excess yet, got %d", watched.UncreditedExcessSats)
	}

	// Second deposit: balance rises to 27,000 (delta 12,000 over the
	// 15,000 baseline). Current holdings are already 15,000 cents, so
	// headroom under the 25,000 cap is only 10,000 -> credit 10,000
	// more (running BTCB total 25,000, exactly at the cap), excess
	// 2,000 tracked.
	applyTxOnce(buildDepositTx("btc-cap-dep-2", 27_000))
	if got := bridgedBtcStore.BalanceOf(btcbAddress); got != 25_000 {
		t.Fatalf("expected BTCB credited up to the account cap (25000), got %d", got)
	}
	watched, _ = btcAddressStore.Get(ccAddress)
	if watched.UncreditedExcessSats != 2_000 {
		t.Fatalf("expected UncreditedExcessSats 2000, got %d", watched.UncreditedExcessSats)
	}
	if watched.LastCreditedSats != 27_000 {
		t.Fatalf("expected LastCreditedSats to advance to the full observed balance 27000, got %d", watched.LastCreditedSats)
	}
}
