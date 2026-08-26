package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// buildSignedSymbolTransferTx is a generic version of
// buildSignedETHBTransferTx (api/bridge_test.go) - parameterized on
// symbol, so it covers BTCB (and any future symbol) without
// duplicating the envelope-construction boilerplate. Named distinctly
// from apply_test.go's buildSignedTransferTx (CCT-only, generates its
// own keypair) to avoid a collision.
func buildSignedSymbolTransferTx(id string, priv []byte, pub []byte, from, to, symbol string, amount uint64, ts int64) core.Transaction {
	payload := tokenTransferData{To: to, Amount: amount, Symbol: symbol}
	payloadJSON, _ := json.Marshal(payload)

	env := core.SignedEnvelope{
		Address:   from,
		PublicKey: hex.EncodeToString(pub),
		Timestamp: ts,
		Payload:   payloadJSON,
	}
	sig := core.SignMessage(priv, env.CanonicalMessage())

	return core.Transaction{
		ID:            id,
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
		Data:          string(payloadJSON),
	}
}

// TestApplyTransfer_BTCBCCTNetworkFee confirms a CCT-network BTCB send
// debits amount+BTCBCCTNetworkFeeSats entirely from the sender's own
// BTCB balance (own-currency-pays-its-own-fee, same shape as ETHB's
// CCT-network fee).
func TestApplyTransfer_BTCBCCTNetworkFee(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(pub)
	fromBtcb := resolveBalanceAddress(from, hex.EncodeToString(pub), "BTCB")

	// BTCB resolves to a derived sub-address (core.BTCBAddressTag), which
	// needs the recipient's real public key - populate walletStore the
	// same way a wallet's own activity would (see core.Wallet.PublicKeyHex's
	// doc comment), so applyTransfer can resolve `to` through the same
	// path the recipient's own balance lookups use (the bug this test
	// now guards against - see TestApplyTransfer_ETHBCCTNetworkFee).
	toPub, _, _ := core.GenerateKeyPair()
	to := core.GenerateAddress(toPub)
	walletStore.Get(to).PublicKeyHex = hex.EncodeToString(toPub)
	toBtcb := resolveBalanceAddress(to, hex.EncodeToString(toPub), "BTCB")

	bridgedBtcStore.Mint(fromBtcb, 10_000+BTCBCCTNetworkFeeSats)

	tx := buildSignedSymbolTransferTx("btcb-send-1", priv, pub, from, to, "BTCB", 10_000, 1000)
	applyTxOnce(tx)

	if got := bridgedBtcStore.BalanceOf(fromBtcb); got != 0 {
		t.Fatalf("expected sender BTCB balance 0 (10000+fee sent), got %d", got)
	}
	if got := bridgedBtcStore.BalanceOf(toBtcb); got != 10_000 {
		t.Fatalf("expected recipient BTCB balance 10000 at their resolved derived address, got %d", got)
	}
	if got := bridgedBtcStore.BalanceOf(btcbBridgeFeeAddress()); got != BTCBCCTNetworkFeeSats {
		t.Fatalf("expected the fee sink to hold %d sats, got %d", BTCBCCTNetworkFeeSats, got)
	}
}

func TestApplyTransfer_BTCBCCTNetworkFee_InsufficientBalanceRejectsWholeTransfer(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(pub)
	fromBtcb := resolveBalanceAddress(from, hex.EncodeToString(pub), "BTCB")

	const to = "recipient-btcb-address"

	// Just enough for the amount, not enough to also cover the fee.
	bridgedBtcStore.Mint(fromBtcb, 1_000)

	tx := buildSignedSymbolTransferTx("btcb-send-2", priv, pub, from, to, "BTCB", 1_000, 2000)
	applyTxOnce(tx)

	if got := bridgedBtcStore.BalanceOf(fromBtcb); got != 1_000 {
		t.Fatalf("expected BTCB balance untouched (1000) when amount+fee can't both be covered, got %d", got)
	}
	if got := bridgedBtcStore.BalanceOf(to); got != 0 {
		t.Fatalf("expected recipient to receive nothing when the fee can't be paid, got %d", got)
	}
}

// TestApplyBridgeBtcDeposit_CreditsCorrectlyAndDedups mirrors
// TestApplyBridgeDeposit_CreditsCorrectly/_DedupPreventsDoubleCredit
// (api/bridge_test.go) for the BTCB deposit-report path.
func TestApplyBridgeBtcDeposit_CreditsCorrectlyAndDedups(t *testing.T) {
	resetApplyTestState()
	allowDepositsForTest(t)
	registerCurrentNodeAsAuthorizedValidator(t)

	pub, _, _ := core.GenerateKeyPair()
	ccAddress := core.GenerateAddress(pub)
	pubHex := hex.EncodeToString(pub)

	btcAddressStore.Set(&core.BtcWatchedAddress{
		CreditChainAddress:   ccAddress,
		CreditChainPublicKey: pubHex,
		BtcAddress:           "tb1qtest0000000000000000000000000000000000",
	})
	btcbAddress := resolveBalanceAddress(ccAddress, pubHex, "BTCB")

	data, _ := json.Marshal(btcBridgeDepositReportData{
		CreditChainAddress: ccAddress,
		NewBalanceSats:     50_000,
	})
	env := selfSignedEnvelope(data)
	tx := core.Transaction{
		ID:            "btc-deposit-1",
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

	if got := bridgedBtcStore.BalanceOf(btcbAddress); got != 50_000 {
		t.Fatalf("expected BTCB balance 50000 after first deposit report, got %d", got)
	}
	watched, _ := btcAddressStore.Get(ccAddress)
	if watched.LastCreditedSats != 50_000 {
		t.Fatalf("expected LastCreditedSats updated to 50000, got %d", watched.LastCreditedSats)
	}

	// Replaying the identical report must not double-credit.
	tx2 := tx
	tx2.ID = "btc-deposit-1-replay"
	applyTxOnce(tx2)

	if got := bridgedBtcStore.BalanceOf(btcbAddress); got != 50_000 {
		t.Fatalf("expected BTCB balance to stay 50000 after a replayed report, got %d", got)
	}
}

// TestApplyHubTradeRequest_OnChain_BTCB confirms hub trading of BTCB
// needs zero new code - resolveTokenStore("BTCB") already resolves
// (B.1), so the existing generic settleHubTradeOnChain path (already
// proven for CCT-vs-native-token pairs) works unchanged for a
// BTCB-vs-CCT ONCHAIN rate too. Mirrors
// TestApplyHubTradeRequest_FractionalRateScalesCorrectly's setup, just
// with Mode:"ONCHAIN" and Symbol:"BTCB" instead of an OFFAPP CCT rate.
func TestApplyHubTradeRequest_OnChain_BTCB(t *testing.T) {
	resetHubOperatorTestState()

	hubPub, hubPriv, _ := core.GenerateKeyPair()
	hubAddr := core.GenerateAddress(hubPub)
	hubPubHex := hex.EncodeToString(hubPub)
	companyStore.Apply(&core.CompanyApplication{
		Address:   hubAddr,
		PublicKey: hubPubHex,
		Status:    core.CompanyVerified,
	})

	// 1 BTCB (sats) = 30 CCT, scaled by core.HubRateScale.
	rateTx := signedHubTxAs(t, hubPub, hubPriv, core.TxHubRateSet, hubRateSetData{
		Hub: "", Symbol: "BTCB", FiatSymbol: "CCT", Mode: "ONCHAIN", SellRate: 30 * core.HubRateScale, Active: true,
	}, 1000)
	applyTxOnce(rateTx)

	rate, ok := hubRateStore.Get(hubAddr, "BTCB", "", "CCT")
	if !ok || !rate.IsOnChain() {
		t.Fatalf("expected an ONCHAIN BTCB/CCT rate to be stored, got %+v", rate)
	}

	hubBtcbAddr := resolveBalanceAddress(hubAddr, hubPubHex, "BTCB")
	bridgedBtcStore.Mint(hubBtcbAddr, 1_000)

	buyerPub, buyerPriv, _ := core.GenerateKeyPair()
	buyerAddr := core.GenerateAddress(buyerPub)
	cctStore.Mint(buyerAddr, cctW(1_000_000))

	tradeTx := signedHubTxAs(t, buyerPub, buyerPriv, core.TxHubTradeRequest, hubTradeRequestData{
		Hub: hubAddr, Symbol: "BTCB", Amount: 100, FiatSymbol: "CCT", Direction: "BUY_FROM_HUB",
	}, 2000)
	applyTxOnce(tradeTx)

	buyerBtcbAddr := resolveBalanceAddress(buyerAddr, hex.EncodeToString(buyerPub), "BTCB")
	if got := bridgedBtcStore.BalanceOf(buyerBtcbAddr); got != 100 {
		t.Fatalf("expected buyer to receive 100 sats of BTCB, got %d", got)
	}
	// 100 sats * 30*HubRateScale / HubRateScale == 3000 CCT, minus the
	// 0.3% marketplace platform fee (see api/apply.go's
	// splitPlatformFee) skimmed on receipt - 9 CCT, leaving 2991. The
	// buyer's own BTCB leg carries no equivalent deduction here: 0.3%
	// of 100 sats floors to 0 under integer math, so nothing was small
	// enough to skim on that side.
	if got := cctStore.BalanceOf(hubAddr); got != cctW(2_991) {
		t.Fatalf("expected hub to receive 2991 CCT (3000 - 0.3%% platform fee), got %s", got)
	}

	offer, ok := p2pOfferStore.Get(tradeTx.ID)
	if !ok || offer.PaymentMode != core.PaymentModeOnChain {
		t.Fatalf("expected an ONCHAIN P2POffer trade-history record, got %+v", offer)
	}
}
