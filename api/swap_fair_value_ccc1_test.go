package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestHandleFairValueSwap_RejectsVaultBackedSymbols is the regression
// test for a real fund-safety fix: ETH_CCC1/BTC_CCC1 must never be
// swappable through this endpoint, since fairValueMint has no check
// against real vault reserves - allowing it would let circulating
// CCC1 supply exceed what's actually locked (see this file's own doc
// comment on fairValueSwapSymbols for the full reasoning). Covers
// every direction: CCC1 as FromSymbol, CCC1 as ToSymbol, and CCC1 on
// both sides at once.
func TestHandleFairValueSwap_RejectsVaultBackedSymbols(t *testing.T) {
	resetApplyTestState()

	pub, priv, _ := core.GenerateKeyPair()

	cases := []struct {
		name       string
		fromSymbol string
		toSymbol   string
	}{
		{"ETH_CCC1 as from", "ETH_CCC1", "YENE"},
		{"BTC_CCC1 as from", "BTC_CCC1", "YENE"},
		{"ETH_CCC1 as to", "YENE", "ETH_CCC1"},
		{"BTC_CCC1 as to", "YENE", "BTC_CCC1"},
		{"CCC1 on both sides", "ETH_CCC1", "BTC_CCC1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := buildSignedEnvelopeJSON(t, pub, priv, fairValueSwapRequest{
				FromSymbol: tc.fromSymbol, ToSymbol: tc.toSymbol, FromAmount: 1000,
			})
			req := httptest.NewRequest(http.MethodPost, "/swap/fair-value", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			HandleFairValueSwap(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 rejecting a vault-backed symbol, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestHandleFairValueSwap_LegacyMirrorSymbolsStillAllowed proves the
// fix didn't overreach - BTCB/ETHB/YENE (which never claimed real 1:1
// backing) must still work exactly as before.
func TestHandleFairValueSwap_LegacyMirrorSymbolsStillAllowed(t *testing.T) {
	resetApplyTestState()

	if !fairValueSwapSymbols["BTCB"] || !fairValueSwapSymbols["ETHB"] || !fairValueSwapSymbols["YENE"] {
		t.Fatalf("expected BTCB/ETHB/YENE to remain swappable, got %+v", fairValueSwapSymbols)
	}
	if fairValueSwapSymbols["ETH_CCC1"] || fairValueSwapSymbols["BTC_CCC1"] {
		t.Fatalf("expected ETH_CCC1/BTC_CCC1 to be excluded, got %+v", fairValueSwapSymbols)
	}
}
