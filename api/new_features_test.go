package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// buildSignedTx builds a genuinely-signed transaction of any type with
// any JSON-marshalable payload - same envelope/signature shape as
// core.SignedEnvelope, packed into the transaction's Auth* fields the
// way recordTx does. A generalized sibling of buildSignedTransferTx for
// transaction types that aren't a plain CCT transfer.
func buildSignedTx(t *testing.T, txType core.TransactionType, payload any, ts int64) (core.Transaction, string, []byte) {
	t.Helper()

	pub, priv, err := core.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	from := core.GenerateAddress(pub)

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
		ID:            "test-tx-" + string(txType) + "-" + from,
		From:          from,
		To:            "",
		Amount:        0,
		Type:          txType,
		Timestamp:     ts,
		Data:          string(payloadJSON),
		AuthAddress:   from,
		AuthPublicKey: hex.EncodeToString(pub),
		AuthTimestamp: ts,
		AuthPayload:   payloadJSON,
		AuthSignature: hex.EncodeToString(sig),
	}
	return tx, from, pub
}

// --- Token standards ---

func TestTokenStandardRegistry_CreatableExcludesSystemStandard(t *testing.T) {
	reg := core.NewTokenStandardRegistry()
	creatable := reg.Creatable()

	for _, s := range creatable {
		if s.System {
			t.Fatalf("expected no system standard in Creatable(), found %s", s.Code)
		}
		if s.Code == "C101" {
			t.Fatalf("C101 (native, reserved) must never be user-creatable")
		}
	}
	if len(creatable) != 2 {
		t.Fatalf("expected exactly 2 creatable standards (C1, C117S), got %d", len(creatable))
	}
}

func TestTokenStandardRegistry_IsCreatable(t *testing.T) {
	reg := core.NewTokenStandardRegistry()

	if reg.IsCreatable("C101") {
		t.Fatal("C101 is a system standard and must not be creatable")
	}
	if !reg.IsCreatable("C1") {
		t.Fatal("C1 should be creatable")
	}
	if !reg.IsCreatable("C117S") {
		t.Fatal("C117S should be creatable")
	}
	if reg.IsCreatable("BOGUS") {
		t.Fatal("an unknown standard code must not be creatable")
	}
}

func TestApplyTokenRegister_RejectsNonCreatableStandard(t *testing.T) {
	resetApplyTestState()

	tx, from, pubBytes := buildSignedTx(t, core.TxTokenRegister, tokenRegisterData{
		Symbol:        "BADSTD",
		Name:          "Bad Standard Token",
		InitialSupply: 100,
		Standard:      "C101", // system-reserved, must be rejected
	}, 1000)
	stakingStore.Stakes[from] = map[string]uint64{"ETHTEST": 1}
	ethTestStore.Mint(core.DeriveTokenAddress(pubBytes, core.ETHTestAddressTag), TokenCreationFeeETHTest)

	applyTokenRegister(tx)

	if _, exists := tokenRegistry.Meta["BADSTD"]; exists {
		t.Fatal("expected registration with a non-creatable standard to be rejected")
	}
}

func TestApplyTokenRegister_DefaultsEmptyStandardToC1(t *testing.T) {
	resetApplyTestState()

	tx, from, pubBytes := buildSignedTx(t, core.TxTokenRegister, tokenRegisterData{
		Symbol:        "NOSTAND",
		Name:          "No Standard Specified",
		InitialSupply: 50,
	}, 1000)
	stakingStore.Stakes[from] = map[string]uint64{"ETHTEST": 1}
	ethTestStore.Mint(core.DeriveTokenAddress(pubBytes, core.ETHTestAddressTag), TokenCreationFeeETHTest)

	applyTokenRegister(tx)

	meta, exists := tokenRegistry.Meta["NOSTAND"]
	if !exists {
		t.Fatal("expected registration to succeed")
	}
	if meta.StandardOrDefault() != "C1" {
		t.Fatalf("expected default standard C1, got %s", meta.StandardOrDefault())
	}
}

func TestApplyTokenRegister_StoresC117SPegReference(t *testing.T) {
	resetApplyTestState()

	tx, from, pubBytes := buildSignedTx(t, core.TxTokenRegister, tokenRegisterData{
		Symbol:        "STBL",
		Name:          "A Stable Token",
		InitialSupply: 1000,
		Standard:      "C117S",
		PegReference:  "USD",
	}, 1000)
	stakingStore.Stakes[from] = map[string]uint64{"ETHTEST": 1}
	ethTestStore.Mint(core.DeriveTokenAddress(pubBytes, core.ETHTestAddressTag), TokenCreationFeeETHTest)

	applyTokenRegister(tx)

	meta, exists := tokenRegistry.Meta["STBL"]
	if !exists {
		t.Fatal("expected registration to succeed")
	}
	if meta.StandardOrDefault() != "C117S" || meta.PegReference != "USD" {
		t.Fatalf("expected standard C117S / pegReference USD, got %s / %s", meta.StandardOrDefault(), meta.PegReference)
	}
}

// --- YEN-e claim ---

func TestYeneClaimAmount_DeterministicAndInRange(t *testing.T) {
	a1 := yeneClaimAmount("some-fixed-tx-id")
	a2 := yeneClaimAmount("some-fixed-tx-id")
	if a1 != a2 {
		t.Fatalf("expected the same tx ID to always derive the same amount, got %d and %d", a1, a2)
	}
	if a1 < YeneClaimMinAmount || a1 > YeneClaimMaxAmount {
		t.Fatalf("expected amount in [%d,%d], got %d", YeneClaimMinAmount, YeneClaimMaxAmount, a1)
	}

	b := yeneClaimAmount("a-different-tx-id")
	if a1 == b {
		t.Skip("different IDs happened to hash to the same amount - statistically rare, not a failure on its own")
	}
}

func TestYeneClaimStore_CooldownGating(t *testing.T) {
	s := core.NewYeneClaimStore()
	addr := "some-address"

	if !s.CanClaim(addr, 1000, YeneClaimCooldownSeconds) {
		t.Fatal("expected a never-claimed address to be able to claim")
	}

	s.RecordClaim(addr, 1000)

	if s.CanClaim(addr, 1000+1, YeneClaimCooldownSeconds) {
		t.Fatal("expected claim to be blocked immediately after claiming")
	}
	if s.CanClaim(addr, 1000+YeneClaimCooldownSeconds-1, YeneClaimCooldownSeconds) {
		t.Fatal("expected claim to still be blocked just before the cooldown elapses")
	}
	if !s.CanClaim(addr, 1000+YeneClaimCooldownSeconds, YeneClaimCooldownSeconds) {
		t.Fatal("expected claim to be allowed once the cooldown has fully elapsed")
	}
}

func TestApplyYeneClaim_MintsIntoYenSubAddress(t *testing.T) {
	resetApplyTestState()

	tx, from, pubBytes := buildSignedTx(t, core.TxYeneClaim, struct{}{}, 1000)
	tx.From = from
	tx.AuthAddress = from

	applyYeneClaim(tx)

	yenAddr := core.DeriveTokenAddress(pubBytes, core.YenAddressTag)
	balance := yenStore.BalanceOf(yenAddr)
	expected := yeneClaimAmount(tx.ID)
	if balance != expected {
		t.Fatalf("expected YEN-e balance %d (deterministic from tx ID), got %d", expected, balance)
	}
}

func TestApplyYeneClaim_RespectsCooldownOnReplay(t *testing.T) {
	resetApplyTestState()

	tx, from, pubBytes := buildSignedTx(t, core.TxYeneClaim, struct{}{}, 1000)
	tx.From = from
	tx.AuthAddress = from

	applyYeneClaim(tx)
	firstBalance := yenStore.BalanceOf(core.DeriveTokenAddress(pubBytes, core.YenAddressTag))

	// A second claim transaction from the same address, timestamped
	// before the cooldown elapses, must not mint again - mirrors what
	// applyFaucetClaim already guards against for the faucet.
	tx2, _, _ := buildSignedTx(t, core.TxYeneClaim, struct{}{}, 1000+1)
	tx2.From = from
	tx2.AuthAddress = from
	tx2.AuthPublicKey = tx.AuthPublicKey

	applyYeneClaim(tx2)

	secondBalance := yenStore.BalanceOf(core.DeriveTokenAddress(pubBytes, core.YenAddressTag))
	if secondBalance != firstBalance {
		t.Fatalf("expected a claim within the cooldown window to be a no-op, balance changed from %d to %d", firstBalance, secondBalance)
	}
}

func TestVerifyTxAuth_YeneClaimBindsToFrom(t *testing.T) {
	resetApplyTestState()

	tx, from, _ := buildSignedTx(t, core.TxYeneClaim, struct{}{}, 1000)
	tx.From = from
	tx.AuthAddress = from

	if !verifyTxAuth(tx) {
		t.Fatal("expected a genuinely self-signed YEN-e claim to verify")
	}

	tx.From = "someone-else-entirely"
	if verifyTxAuth(tx) {
		t.Fatal("expected verification to fail once From no longer matches the signer's own address")
	}
}

// --- CCT ETH-equivalent price ---

func TestCctPriceInETH_FallsBackWithoutOrderBookHistory(t *testing.T) {
	priceHistoryStore = core.NewPriceHistoryStore()

	rate, source := cctPriceInETH()
	if source != "fallback" {
		t.Fatalf("expected fallback source with no recorded trades, got %s", source)
	}
	if rate != DefaultCCTPerETH {
		t.Fatalf("expected fallback rate %v, got %v", DefaultCCTPerETH, rate)
	}
}

func TestCctPriceInETH_DerivesFromLatestOrderBookTrade(t *testing.T) {
	priceHistoryStore = core.NewPriceHistoryStore()
	// 500 CCT per 1 ETHTEST, scaled by priceScale (1e6) - i.e.
	// last.Price = 500_000_000 means 1 ETHTEST = 500 CCT, so 1 CCT =
	// 1/500 ETHTEST (treated as ETH).
	priceHistoryStore.RecordPrice("ETHTEST", 500_000_000, 1000)

	rate, source := cctPriceInETH()
	if source != "orderbook" {
		t.Fatalf("expected orderbook source once a trade is recorded, got %s", source)
	}
	want := 1.0 / 500.0
	if rate < want-1e-9 || rate > want+1e-9 {
		t.Fatalf("expected rate ~%v, got %v", want, rate)
	}
}

// --- Multi-currency price oracle ---

func TestHandleSupportedCurrencies_ReturnsFixedList(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/currencies", nil)
	rr := httptest.NewRecorder()
	HandleSupportedCurrencies(rr, req)

	var currencies []string
	if err := json.Unmarshal(rr.Body.Bytes(), &currencies); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(currencies) == 0 {
		t.Fatal("expected at least one supported currency")
	}
	found := false
	for _, c := range currencies {
		if c == "USD" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected USD to be among the supported currencies")
	}
}

func TestHandleExternalPrice_UnsupportedCurrencyRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/price/external?currency=ZZZ", nil)
	rr := httptest.NewRecorder()
	HandleExternalPrice(rr, req)
	if rr.Code != 400 {
		t.Fatalf("expected 400 for an unsupported currency, got %d", rr.Code)
	}
}

func TestHandleExternalPrice_KnownCurrencyReturnsCachedRate(t *testing.T) {
	priceOracleMu.Lock()
	priceOracleCache = priceOracleData{
		Rates:     map[string]currencyRates{"EUR": {Eth: 2500.5, Btc: 55000.25, Usdc: 0.92}},
		UpdatedAt: 12345,
	}
	priceOracleMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/price/external?currency=eur", nil)
	rr := httptest.NewRecorder()
	HandleExternalPrice(rr, req)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp currencyPriceResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Currency != "EUR" || resp.Eth != 2500.5 || resp.Btc != 55000.25 || resp.Usdc != 0.92 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestHandleExternalPrice_NoCurrencyReturnsLegacyUsdShape(t *testing.T) {
	priceOracleMu.Lock()
	priceOracleCache = priceOracleData{
		Rates:     map[string]currencyRates{"USD": {Eth: 3000, Btc: 60000, Usdc: 1.0}},
		UpdatedAt: 99999,
	}
	priceOracleMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/price/external", nil)
	rr := httptest.NewRecorder()
	HandleExternalPrice(rr, req)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp legacyUsdPriceResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.EthUsd != 3000 || resp.BtcUsd != 60000 || resp.UsdcUsd != 1.0 {
		t.Fatalf("unexpected legacy-shape response: %+v", resp)
	}
}

// --- USDC/ETH address unification ---

func TestResolveBalanceAddress_USDCTESTUsesRealCustodialEthAddressWhenPresent(t *testing.T) {
	resetApplyTestState()

	pub, _, _ := core.GenerateKeyPair()
	cctAddress := core.GenerateAddress(pub)
	pubHex := hex.EncodeToString(pub)

	ethCustodialStore.Create(&core.EthCustodialAccount{
		CreditChainAddress:   cctAddress,
		CreditChainPublicKey: pubHex,
		EthAddress:           "0xRealCustodialEthAddress",
		KeyRef:               "test-key-ref",
	})

	got := resolveBalanceAddress(cctAddress, pubHex, "USDCTEST")
	if got != "0xRealCustodialEthAddress" {
		t.Fatalf("expected USDCTEST to resolve to the real custodial ETH address, got %s", got)
	}

	// ETHB must resolve to the identical address - this is the literal
	// "USDC uses the same address as ETH" claim being tested.
	gotEthb := resolveBalanceAddress(cctAddress, pubHex, "ETHB")
	if gotEthb != got {
		t.Fatalf("expected USDCTEST and ETHB to resolve to the same address once a custodial account exists, got %s vs %s", got, gotEthb)
	}
}

func TestResolveBalanceAddress_USDCTESTFallsBackToDerivedHashWithoutCustodialAccount(t *testing.T) {
	resetApplyTestState()

	pub, _, _ := core.GenerateKeyPair()
	cctAddress := core.GenerateAddress(pub)
	pubHex := hex.EncodeToString(pub)

	got := resolveBalanceAddress(cctAddress, pubHex, "USDCTEST")
	want := core.DeriveTokenAddress(pub, core.USDCTestAddressTag)
	if got != want {
		t.Fatalf("expected fallback to the derived-hash address %s, got %s", want, got)
	}
}
