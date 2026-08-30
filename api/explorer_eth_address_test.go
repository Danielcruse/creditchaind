package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestHandleExplorerSearch_EthAddress_FindsLinkedWallet covers the real
// gap flagged in the Explorer design spec: searching by a wallet's real
// self-custodial Ethereum address (ETH_CCC1/"Ethereum-compatible"
// address) previously fell through to "not found" - addressPattern only
// ever matched a plain 40-hex CCT address, never a 0x-prefixed one. This
// confirms the new ethAddressPattern branch resolves it back to the
// same wallet's full profile (same identity, not a separate lookup),
// including the Ethereum-side balances that live at that address.
func TestHandleExplorerSearch_EthAddress_FindsLinkedWallet(t *testing.T) {
	resetApplyTestState()
	ethAddressStore = core.NewEthAddressStore()

	pub, _, err := core.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	cctAddress := core.GenerateAddress(pub)
	pubHex := hex.EncodeToString(pub)
	const ethAddr = "0xAbC123000000000000000000000000000000dEaD"
	const ethAddrLower = "0xabc123000000000000000000000000000000dead"

	blockchain = &core.Blockchain{Blocks: []core.Block{
		{
			Index: 0,
			Transactions: []core.Transaction{
				{
					ID: "seed-tx", AuthAddress: cctAddress, AuthPublicKey: pubHex,
					From: cctAddress, To: "someone-else", Type: core.TxExchange, Timestamp: 1000,
				},
			},
		},
	}}

	ethAddressStore.Set(&core.EthWatchedAddress{
		CreditChainAddress:   cctAddress,
		CreditChainPublicKey: pubHex,
		EthAddress:           ethAddrLower,
	})

	ethCcc1Store.Mint(resolveBalanceAddress(cctAddress, pubHex, "ETH_CCC1"), "1500000000000000000") // 1.5 ETH_CCC1

	// Search with the mixed-case form a real client would paste - the
	// handler must normalize before comparing, same as the registration
	// side already does.
	req := httptest.NewRequest(http.MethodGet, "/explorer/search?q="+ethAddr, nil)
	rec := httptest.NewRecorder()
	HandleExplorerSearch(rec, req)

	var resp ExplorerSearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !resp.Found || resp.Type != "address" || resp.Address == nil {
		t.Fatalf("expected a found address profile, got found=%v type=%q address=%v", resp.Found, resp.Type, resp.Address)
	}
	if resp.Address.Address != cctAddress {
		t.Fatalf("Address = %q, want the linked CCT address %q", resp.Address.Address, cctAddress)
	}
	if resp.Address.EthAddress != ethAddrLower {
		t.Fatalf("EthAddress = %q, want normalized %q", resp.Address.EthAddress, ethAddrLower)
	}
	if !resp.Address.LookedUpByEthAddress {
		t.Fatalf("expected LookedUpByEthAddress=true when searching by the Ethereum address")
	}

	found := false
	for _, b := range resp.Address.HeldBalances {
		if b.Symbol == "ETH_CCC1" && b.Balance == "1500000000000000000" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an ETH_CCC1 held balance in the profile, got %+v", resp.Address.HeldBalances)
	}
}

// TestHandleExplorerSearch_EthAddress_UnregisteredIsHonestNotFabricated
// covers the other half: a real-shaped 0x address that no wallet on
// this node has ever registered must never be fabricated a CreditChain
// identity - it should come back as a found-but-empty profile (real
// address shape, nothing known), not an error and not a made-up link.
func TestHandleExplorerSearch_EthAddress_UnregisteredIsHonestNotFabricated(t *testing.T) {
	resetApplyTestState()
	ethAddressStore = core.NewEthAddressStore()
	blockchain = &core.Blockchain{Blocks: []core.Block{{Index: 0}}}

	req := httptest.NewRequest(http.MethodGet, "/explorer/search?q=0x0000000000000000000000000000000000000000", nil)
	rec := httptest.NewRecorder()
	HandleExplorerSearch(rec, req)

	var resp ExplorerSearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !resp.Found || resp.Type != "address" || resp.Address == nil {
		t.Fatalf("expected a found-but-empty address profile, got found=%v type=%q address=%v", resp.Found, resp.Type, resp.Address)
	}
	if resp.Address.Address != "" {
		t.Fatalf("expected no linked CCT address, got %q", resp.Address.Address)
	}
	if !resp.Address.LookedUpByEthAddress {
		t.Fatalf("expected LookedUpByEthAddress=true")
	}
	if len(resp.Address.HeldBalances) != 0 {
		t.Fatalf("expected no held balances for an unregistered address, got %+v", resp.Address.HeldBalances)
	}
}
