package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestHandleWallet_CctBalanceReflectsRealLedger is a regression test for
// a real bug: the cctBalance field used to read wallet.CCTBalance, which
// is dead state - only ever written by core.RewardWallet, itself called
// nowhere in the live codebase (CCT rewards moved to the real cctStore
// ledger long ago). Every wallet's cctBalance therefore always read 0,
// regardless of a real, nonzero cctStore balance backing every actual
// transfer/stake/fee. Discovered live: an admin-minted balance showed
// $0.00 on the site's own /wallet-backed displays despite the mint
// having genuinely confirmed on-chain.
func TestHandleWallet_CctBalanceReflectsRealLedger(t *testing.T) {
	resetApplyTestState()

	const address = "43df565874fc3b8415148b4a6e6f01e30cfe1eb8"
	cctStore.Mint(address, core.FormatCctSmallest(core.CctWholeToSmallest(1500)))

	req := httptest.NewRequest(http.MethodGet, "/wallet?address="+address, nil)
	w := httptest.NewRecorder()
	HandleWallet(w, req)

	var resp WalletResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.CCTBalance != 1500 {
		t.Fatalf("expected cctBalance 1500 (the real cctStore balance), got %d", resp.CCTBalance)
	}
}
