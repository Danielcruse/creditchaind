package api

import (
	"encoding/hex"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestBuildAddressProfile_IncludesCcc1HeldBalances covers the real gap
// this session found: ETH_CCC1/USDC_CCC1/BTC_CCC1 (the actual mainnet
// vault-backed balances) were completely absent from
// AddressProfile.HeldBalances before this - only the legacy testnet
// ETHB/BTCB/USDCTEST/YENE symbols were ever checked.
func TestBuildAddressProfile_IncludesCcc1HeldBalances(t *testing.T) {
	resetApplyTestState()

	pub, _, err := core.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	address := core.GenerateAddress(pub)
	pubHex := hex.EncodeToString(pub)

	// buildAddressProfile recovers this address's public key from its
	// own most recent confirmed transaction (see its own doc comment) -
	// seed one directly into the chain rather than routing through the
	// full apply pipeline, since only AuthAddress/AuthPublicKey matter
	// for this test.
	blockchain = &core.Blockchain{Blocks: []core.Block{
		{
			Index: 0,
			Transactions: []core.Transaction{
				{
					ID:            "seed-tx",
					AuthAddress:   address,
					AuthPublicKey: pubHex,
					From:          address,
					To:            "someone-else",
					Type:          core.TxExchange,
					Timestamp:     1000,
				},
			},
		},
	}}

	ethCcc1Addr := resolveBalanceAddress(address, pubHex, "ETH_CCC1")
	usdcCcc1Addr := resolveBalanceAddress(address, pubHex, "USDC_CCC1")
	btcCcc1Addr := resolveBalanceAddress(address, pubHex, "BTC_CCC1")

	ethCcc1Store.Mint(ethCcc1Addr, "2634000000000000") // 0.002634 ETH
	usdcCcc1Store.Mint(usdcCcc1Addr, "5000000")         // 5 USDC
	btcCcc1Store.Mint(btcCcc1Addr, 150000)              // 0.0015 BTC

	profile := buildAddressProfile(address)

	got := map[string]string{}
	for _, b := range profile.HeldBalances {
		got[b.Symbol] = b.Balance
	}

	if got["ETH_CCC1"] != "2634000000000000" {
		t.Fatalf("ETH_CCC1 held balance = %q, want \"2634000000000000\"", got["ETH_CCC1"])
	}
	if got["USDC_CCC1"] != "5000000" {
		t.Fatalf("USDC_CCC1 held balance = %q, want \"5000000\"", got["USDC_CCC1"])
	}
	if got["BTC_CCC1"] != "150000" {
		t.Fatalf("BTC_CCC1 held balance = %q, want \"150000\"", got["BTC_CCC1"])
	}
}

// TestBuildAddressProfile_OmitsZeroCcc1Balances confirms a symbol this
// address never held at all doesn't show up as a spurious zero entry -
// matches HeldBalances' existing "only nonzero" convention for every
// other symbol it already covered.
func TestBuildAddressProfile_OmitsZeroCcc1Balances(t *testing.T) {
	resetApplyTestState()

	pub, _, err := core.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	address := core.GenerateAddress(pub)
	pubHex := hex.EncodeToString(pub)

	blockchain = &core.Blockchain{Blocks: []core.Block{
		{
			Index: 0,
			Transactions: []core.Transaction{
				{ID: "seed-tx-2", AuthAddress: address, AuthPublicKey: pubHex, From: address, To: "someone-else", Type: core.TxExchange, Timestamp: 1000},
			},
		},
	}}

	profile := buildAddressProfile(address)

	for _, b := range profile.HeldBalances {
		if b.Symbol == "ETH_CCC1" || b.Symbol == "USDC_CCC1" || b.Symbol == "BTC_CCC1" {
			t.Fatalf("expected no %s entry for an address with a zero balance, got one with value %q", b.Symbol, b.Balance)
		}
	}
}
