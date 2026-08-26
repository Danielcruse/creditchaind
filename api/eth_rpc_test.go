package api

import (
	"bytes"
	"log"
	"math/big"
	"strings"
	"testing"
)

// captureLog redirects the standard logger to a buffer for the
// duration of the test and restores it afterward - used here to
// distinguish which of InitEthRPC's several early-return branches
// actually fired, since more than one of them leaves ethRPCClient nil
// (the only externally-observable side effect otherwise).
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })
	return &buf
}

// resetEthRPCTestState resets the package-level ETH RPC state InitEthRPC
// mutates - both ethRPCClient and expectedEthChainID are shared across
// every test in this package, so a prior test's override must not leak
// into this one.
func resetEthRPCTestState(t *testing.T) {
	t.Helper()
	ethRPCClient = nil
	expectedEthChainID = sepoliaChainID
	t.Cleanup(func() {
		ethRPCClient = nil
		expectedEthChainID = sepoliaChainID
	})
}

// TestInitEthRPC_NeverGatesOnKMSRegardlessOfChain confirms InitEthRPC
// itself no longer has any KMS opinion at all - that safety gate moved
// to HandleCreateEthCustodialAccount (see eth_custodial_test.go), since
// this shared RPC connection is also used by the fully self-custodial
// flow (eth_selfcustody.go), which never touches ethSigner/KMS and
// carries none of the custody risk the gate exists to prevent. Proven
// here by confirming a non-Sepolia chain ID with the KMS flag OFF still
// fails only on the missing RPC URL - the exact same failure mode as
// the Sepolia-default case below - never on a KMS-related refusal.
func TestInitEthRPC_NeverGatesOnKMSRegardlessOfChain(t *testing.T) {
	resetEthRPCTestState(t)
	t.Setenv("ETH_EXPECTED_CHAIN_ID", "1") // real Ethereum mainnet
	t.Setenv("ETH_CUSTODIAL_KMS_ENABLED", "")
	t.Setenv("ETH_RPC_URL", "")
	t.Setenv("SEPOLIA_RPC_URL", "")

	buf := captureLog(t)
	InitEthRPC()

	if ethRPCClient != nil {
		t.Fatal("expected still-disabled with no RPC URL configured")
	}
	if got := big.NewInt(1); expectedEthChainID.Cmp(got) != 0 {
		t.Fatalf("expected expectedEthChainID parsed to 1, got %s", expectedEthChainID)
	}
	if strings.Contains(buf.String(), "KMS") {
		t.Fatalf("expected InitEthRPC to have zero KMS opinion regardless of chain ID, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "ETH_RPC_URL/SEPOLIA_RPC_URL not set") {
		t.Fatalf("expected the request to fail only on the missing RPC URL, got: %s", buf.String())
	}
}

// TestInitEthRPC_SepoliaDefault_FailsOnlyOnMissingURL confirms today's
// exact unmodified default behavior: with ETH_EXPECTED_CHAIN_ID unset
// (defaults to Sepolia), InitEthRPC fails only on the missing RPC URL -
// a testnet deployment's existing config is unaffected by this file no
// longer containing any KMS gate.
func TestInitEthRPC_SepoliaDefault_FailsOnlyOnMissingURL(t *testing.T) {
	resetEthRPCTestState(t)
	t.Setenv("ETH_EXPECTED_CHAIN_ID", "")
	t.Setenv("ETH_CUSTODIAL_KMS_ENABLED", "")
	t.Setenv("ETH_RPC_URL", "")
	t.Setenv("SEPOLIA_RPC_URL", "")

	buf := captureLog(t)
	InitEthRPC()

	if ethRPCClient != nil {
		t.Fatal("expected still-disabled with no RPC URL configured")
	}
	if !strings.Contains(buf.String(), "ETH_RPC_URL/SEPOLIA_RPC_URL not set") {
		t.Fatalf("expected the request to fail on the missing RPC URL, got: %s", buf.String())
	}
}
