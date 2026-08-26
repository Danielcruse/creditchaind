package api

import (
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestApplyTokenMint_NormalizesCcPrefixedDestination is a regression
// test for a real incident: minting CCT to an address typed in its
// normal "cc"-prefixed display form (the format shown everywhere else
// on the site) silently landed the balance under that 42-character
// string as a literal map key, instead of the wallet's real 40-character
// raw address - a key the recipient's own wallet (which signs with the
// raw form) could never reach. Confirmed live: the mint transaction
// showed as confirmed on-chain, but the destination wallet's balance
// stayed 0 everywhere it was actually checked.
func TestApplyTokenMint_NormalizesCcPrefixedDestination(t *testing.T) {
	resetApplyTestState()

	const rawAddress = "43df565874fc3b8415148b4a6e6f01e30cfe1eb8"
	const ccPrefixedAddress = "cc" + rawAddress

	data := tokenMintData{Address: ccPrefixedAddress, Amount: 1500, Symbol: "CCT"}
	payload, _ := json.Marshal(data)
	applyTokenMint(core.Transaction{Data: string(payload), Timestamp: 1000})

	if got := core.CctSmallestToWholeFloor(cctStore.BalanceOf(rawAddress)); got != 1500 {
		t.Fatalf("expected 1500 CCT reachable at the real raw address, got %d", got)
	}
	if got := cctStore.BalanceOf(ccPrefixedAddress); got != "0" {
		t.Fatalf("expected nothing left under the cc-prefixed key, got %s", got)
	}
}

// TestApplyTokenMint_NormalizesCcPrefixedDestinationForOtherSymbols
// confirms the same normalization applies to non-CCT mints too, not
// just the CCT-specific code path.
func TestApplyTokenMint_NormalizesCcPrefixedDestinationForOtherSymbols(t *testing.T) {
	resetApplyTestState()

	const rawAddress = "43df565874fc3b8415148b4a6e6f01e30cfe1eb8"
	const ccPrefixedAddress = "cc" + rawAddress

	data := tokenMintData{Address: ccPrefixedAddress, Amount: 42, Symbol: "REPT"}
	payload, _ := json.Marshal(data)
	applyTokenMint(core.Transaction{Data: string(payload), Timestamp: 1000})

	store, err := resolveTokenStore("REPT")
	if err != nil {
		t.Fatalf("resolveTokenStore: %v", err)
	}
	if got := store.BalanceOf(rawAddress); got != 42 {
		t.Fatalf("expected 42 REPT reachable at the real raw address, got %d", got)
	}
}
