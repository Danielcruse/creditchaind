package api

import (
	"encoding/hex"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestApplyHubTradeRequest_FractionalRateScalesCorrectly is the
// regression test for the "can't set 1 CCT = 0.000975 ETH" bug: before
// core.HubRateScale existed, HubRate.SellRate/BuyRate were plain
// unscaled uint64s, so any fractional price truncated to 0 both in the
// frontend's parseInt and conceptually on the backend. This confirms a
// scaled rate (975, representing the real price 0.000975 once divided
// by core.HubRateScale) produces the correct fiat amount for an
// OFFAPP hub trade.
func TestApplyHubTradeRequest_FractionalRateScalesCorrectly(t *testing.T) {
	resetHubOperatorTestState()

	hubPub, hubPriv, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	companyStore.Apply(&core.CompanyApplication{
		Address:   hubAddr,
		PublicKey: hex.EncodeToString(hubPub),
		Status:    core.CompanyVerified,
	})

	// Real-world price: 1 CCT = 0.000975 <fiat> -> stored scaled as 975
	// (975 / core.HubRateScale == 0.000975).
	rateTx := signedHubTxAs(t, hubPub, hubPriv, core.TxHubRateSet, hubRateSetData{
		Hub: "", Symbol: "CCT", FiatSymbol: "USD", SellRate: 975, Active: true,
	}, 1000)
	applyTxOnce(rateTx)

	rate, ok := hubRateStore.Get(hubAddr, "CCT", "", "USD")
	if !ok || rate.SellRate != 975 {
		t.Fatalf("expected the fractional rate to be stored as scaled 975, got %+v", rate)
	}

	// Hub needs CCT inventory to sell.
	cctStore.Mint(hubAddr, cctW(1_000_000))

	buyerPub, buyerPriv, _ := core.GenerateKeyPair()
	buyerAddr := core.GenerateAddress(buyerPub)

	tradeTx := signedHubTxAs(t, buyerPub, buyerPriv, core.TxHubTradeRequest, hubTradeRequestData{
		Hub: hubAddr, Symbol: "CCT", Amount: 1_000_000, FiatSymbol: "USD", Direction: "BUY_FROM_HUB",
	}, 2000)
	applyTxOnce(tradeTx)

	offer, ok := p2pOfferStore.Get(tradeTx.ID)
	if !ok {
		t.Fatalf("expected the trade to create a P2POffer")
	}
	// 1,000,000 CCT * 975 / core.HubRateScale(1,000,000) == 975.
	wantFiat := uint64(975)
	if offer.WantAmount != wantFiat {
		t.Fatalf("expected fiat amount %d (0.000975 * 1,000,000), got %d", wantFiat, offer.WantAmount)
	}
	if offer.AcceptedBy != buyerAddr {
		t.Fatalf("expected buyer %s to be AcceptedBy, got %s", buyerAddr, offer.AcceptedBy)
	}

	escrow := p2pOfferEscrowAddress(tradeTx.ID)
	if got := cctStore.BalanceOf(escrow); got != cctW(1_000_000) {
		t.Fatalf("expected 1,000,000 CCT escrowed from the hub's sale, got %s", got)
	}
}
