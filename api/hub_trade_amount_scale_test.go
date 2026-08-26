package api

import (
	"encoding/hex"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

func TestHubTradeAmountDecimals_KnownSymbols(t *testing.T) {
	cases := map[string]uint64{
		"ETH_CCC1":  18,
		"USDC_CCC1": 6,
		"BTC_CCC1":  8,
		"CCT":       0,
		"REPT":      0,
		"BTCB":      0, // pre-existing testnet mirror token - deliberately untouched, see its own test
	}
	for symbol, want := range cases {
		if got := hubTradeAmountDecimals(symbol); got != want {
			t.Errorf("hubTradeAmountDecimals(%q) = %d, want %d", symbol, got, want)
		}
	}
}

// TestApplyHubTradeRequest_EthCcc1FractionalWeiAmount_OffApp is the
// exact scenario this fix exists for: a trader buying a genuinely
// fractional amount of ETH_CCC1 (0.0000567 ETH) from a hub quoting an
// OFFAPP (off-app currency) rate per WHOLE ETH - before this fix, the
// fiat-amount math never divided by ETH_CCC1's 18 decimals, so this
// would have computed a wildly wrong (or, with the old frontend
// integer-only entry, entirely unreachable) fiat amount.
func TestApplyHubTradeRequest_EthCcc1FractionalWeiAmount_OffApp(t *testing.T) {
	resetHubOperatorTestState()

	hubPub, hubPriv, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	hubPubHex := hex.EncodeToString(hubPub)
	companyStore.Apply(&core.CompanyApplication{Address: hubAddr, PublicKey: hubPubHex, Status: core.CompanyVerified})

	// 1 whole ETH_CCC1 = 3,500,000 NGN, scaled by core.HubRateScale.
	rateTx := signedHubTxAs(t, hubPub, hubPriv, core.TxHubRateSet, hubRateSetData{
		Hub: "", Symbol: "ETH_CCC1", FiatSymbol: "NGN", SellRate: 3_500_000 * core.HubRateScale, Active: true,
	}, 1000)
	applyTxOnce(rateTx)

	store, err := resolveTokenStore("ETH_CCC1")
	if err != nil {
		t.Fatalf("resolveTokenStore: %v", err)
	}
	hubSide := resolveBalanceAddress(hubAddr, hubPubHex, "ETH_CCC1")
	store.Mint(hubSide, 1_000_000_000_000_000_000) // 1 whole ETH of inventory

	buyerPub, buyerPriv, _ := core.GenerateKeyPair()
	buyerAddr := core.GenerateAddress(buyerPub)

	const amountWei = uint64(56_700_000_000_000) // 0.0000567 ETH
	tradeTx := signedHubTxAs(t, buyerPub, buyerPriv, core.TxHubTradeRequest, hubTradeRequestData{
		Hub: hubAddr, Symbol: "ETH_CCC1", Amount: amountWei, FiatSymbol: "NGN", Direction: "BUY_FROM_HUB",
	}, 2000)
	applyTxOnce(tradeTx)

	offer, ok := p2pOfferStore.Get(tradeTx.ID)
	if !ok {
		t.Fatalf("expected the trade to create a P2POffer")
	}
	if offer.OfferAmount != amountWei {
		t.Fatalf("expected escrowed OfferAmount to be the exact wei amount %d, got %d", amountWei, offer.OfferAmount)
	}
	// 0.0000567 ETH * 3,500,000 NGN/ETH = 198.45 NGN, floored to 198
	// (OFFAPP fiat is a plain whole-currency-unit display value).
	if want := uint64(198); offer.WantAmount != want {
		t.Fatalf("expected fiat amount %d NGN (0.0000567 * 3,500,000), got %d", want, offer.WantAmount)
	}

	escrow := p2pOfferEscrowAddress(tradeTx.ID)
	if got := store.BalanceOf(escrow); got != amountWei {
		t.Fatalf("expected exactly %d wei escrowed, got %d", amountWei, got)
	}
	buyerSide := resolveBalanceAddress(buyerAddr, hex.EncodeToString(buyerPub), "ETH_CCC1")
	if got := store.BalanceOf(buyerSide); got != 0 {
		t.Fatalf("expected the buyer's own balance untouched at 0 until settlement, got %d", got)
	}
}

// TestApplyHubTradeRequest_EthCcc1VsUsdcCcc1_OnChainSettlesBothLegsToScale
// covers the ONCHAIN path, where BOTH legs are real tokens with their
// own decimals (18 for ETH_CCC1, 6 for USDC_CCC1) - both must be
// converted correctly, not just the symbol side.
func TestApplyHubTradeRequest_EthCcc1VsUsdcCcc1_OnChainSettlesBothLegsToScale(t *testing.T) {
	resetHubOperatorTestState()

	hubPub, hubPriv, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	hubPubHex := hex.EncodeToString(hubPub)
	companyStore.Apply(&core.CompanyApplication{Address: hubAddr, PublicKey: hubPubHex, Status: core.CompanyVerified})

	// 1 whole ETH_CCC1 = 2,000 whole USDC_CCC1, scaled by core.HubRateScale.
	rateTx := signedHubTxAs(t, hubPub, hubPriv, core.TxHubRateSet, hubRateSetData{
		Hub: "", Symbol: "ETH_CCC1", FiatSymbol: "USDC_CCC1", Mode: "ONCHAIN", SellRate: 2_000 * core.HubRateScale, Active: true,
	}, 1000)
	applyTxOnce(rateTx)

	ethStore, _ := resolveTokenStore("ETH_CCC1")
	usdcStore, _ := resolveTokenStore("USDC_CCC1")

	hubEthSide := resolveBalanceAddress(hubAddr, hubPubHex, "ETH_CCC1")
	ethStore.Mint(hubEthSide, 1_000_000_000_000_000_000) // 1 whole ETH of inventory

	buyerPub, buyerPriv, _ := core.GenerateKeyPair()
	buyerAddr := core.GenerateAddress(buyerPub)
	buyerUsdcSide := resolveBalanceAddress(buyerAddr, hex.EncodeToString(buyerPub), "USDC_CCC1")
	usdcStore.Mint(buyerUsdcSide, 1_000_000) // exactly 1.0 USDC_CCC1 (6 decimals)

	const amountWei = uint64(500_000_000_000_000) // 0.0005 ETH
	tradeTx := signedHubTxAs(t, buyerPub, buyerPriv, core.TxHubTradeRequest, hubTradeRequestData{
		Hub: hubAddr, Symbol: "ETH_CCC1", Amount: amountWei, FiatSymbol: "USDC_CCC1", Direction: "BUY_FROM_HUB",
	}, 2000)
	applyTxOnce(tradeTx)

	// The buyer's received ETH_CCC1 leg is also skimmed 0.3% on receipt
	// (see settleHubTradeOnChain's skimPlatformFee(symbolStore, ...,
	// userSymbolSide, data.Amount) call) - 500,000,000,000,000 wei minus
	// 0.3% = 498,500,000,000,000. This is the platform fee, an existing
	// mechanism unrelated to this fix - the point under test is that the
	// PRE-fee amount the buyer was credited was the exact wei figure
	// (unscaled/unrounded), not that no fee applies at all.
	buyerEthSide := resolveBalanceAddress(buyerAddr, hex.EncodeToString(buyerPub), "ETH_CCC1")
	wantEthAfterFee := amountWei - amountWei*3/1000
	if got := ethStore.BalanceOf(buyerEthSide); got != wantEthAfterFee {
		t.Fatalf("expected buyer to receive %d wei of ETH_CCC1 (amount minus 0.3%% platform fee), got %d", wantEthAfterFee, got)
	}
	// 0.0005 ETH * 2,000 USDC/ETH = 1.0 USDC = 1,000,000 smallest units,
	// entirely spent (the platform fee on this leg is skimmed from the
	// HUB's received USDC, not the buyer's payment - see
	// skimPlatformFee(counterStore, ..., hubCounterSide, counterAmount)).
	if got := usdcStore.BalanceOf(buyerUsdcSide); got != 0 {
		t.Fatalf("expected buyer's USDC_CCC1 fully spent (1,000,000 -> 0), got %d left", got)
	}
}
