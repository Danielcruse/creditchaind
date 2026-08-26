package api

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// buildSignedStakeTx mirrors buildSignedTransferTx's shape (see
// apply_test.go) for a STAKE_ETH transaction - builds a genuinely
// signed envelope so applyTxOnce's PublicKeyHex-capture path (see
// core.Wallet.PublicKeyHex's doc comment) runs exactly like it would
// for a real client request.
func buildSignedStakeTx(t *testing.T, symbol string, amount uint64, ts int64) (core.Transaction, string, []byte) {
	t.Helper()

	pub, priv, _ := core.GenerateKeyPair()
	from := core.GenerateAddress(pub)

	payload := stakeData{Symbol: symbol, Amount: amount}
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
		ID:            "test-stake-" + from,
		From:          from,
		Amount:        amount,
		Type:          core.TxStakeETH,
		Data:          string(payloadJSON),
		Timestamp:     ts,
		AuthAddress:   from,
		AuthPublicKey: hex.EncodeToString(pub),
		AuthTimestamp: ts,
		AuthPayload:   payloadJSON,
		AuthSignature: hex.EncodeToString(sig),
	}
	return tx, from, pub
}

// TestApplyStake_ETHB_NoRewardNoOETH is the deliberately-updated
// replacement for what used to be TestApplyStake_ETHB_MintsOETH -
// staking is now a pure locking mechanism (Phase 3 of the mainnet
// content/theme plan): staking ETHB still moves the real balance into
// the stake ledger, but mints neither a CCT reward nor an oETH
// receipt anymore. See audit/KNOWN_GAPS.md's "Staking reward removal"
// entry for the full rationale.
func TestApplyStake_ETHB_NoRewardNoOETH(t *testing.T) {
	resetApplyTestState()

	tx, from, pub := buildSignedStakeTx(t, "ETHB", 500, 1000)
	// applyStake debits the staker's own ETHB balance (resolved via
	// resolveBalanceAddress, which falls back to the pubkey-derived
	// sub-address with no custodial account present in this test).
	ethbAddr := core.DeriveTokenAddress(pub, core.ETHBAddressTag)
	bridgedEthStore.Mint(ethbAddr, 500)

	applyTxOnce(tx)

	if got := bridgedEthStore.BalanceOf(ethbAddr); got != 0 {
		t.Fatalf("expected ETHB balance fully staked (0 remaining), got %d", got)
	}
	if got := stakingStore.StakeOf(from, "ETHB"); got != 500 {
		t.Fatalf("expected 500 ETHB staked, got %d", got)
	}
	if got := walletStore.Get(from).OETHBalance; got != 0 {
		t.Fatalf("expected no oETH minted for staking ETHB (staking is a pure locking mechanism now), got %d", got)
	}
}

func TestApplyStake_LegacySymbolsRejected(t *testing.T) {
	resetApplyTestState()

	for _, symbol := range []string{"ETHTEST", "USDCTEST"} {
		tx, from, pub := buildSignedStakeTx(t, symbol, 500, 1000)
		var addr string
		if symbol == "ETHTEST" {
			addr = core.DeriveTokenAddress(pub, core.ETHTestAddressTag)
			ethTestStore.Mint(addr, 500)
		} else {
			addr = core.DeriveTokenAddress(pub, core.USDCTestAddressTag)
			usdcTestStore.Mint(addr, 500)
		}

		applyTxOnce(tx)

		if got := stakingStore.StakeOf(from, symbol); got != 0 {
			t.Fatalf("%s: expected staking to be rejected post-migration, got %d staked", symbol, got)
		}
		var bal uint64
		if symbol == "ETHTEST" {
			bal = ethTestStore.BalanceOf(addr)
		} else {
			bal = usdcTestStore.BalanceOf(addr)
		}
		if bal != 500 {
			t.Fatalf("%s: balance should be untouched (rejected before any transfer), got %d", symbol, bal)
		}
	}
}

func TestLegacyStakeRedemption_FreeStakeRedeemed(t *testing.T) {
	resetApplyTestState()
	resetLegacyStakeMigrationSweepForTest()

	pub, _, _ := core.GenerateKeyPair()
	addr := core.GenerateAddress(pub)

	// Seed a legacy ETHTEST stake directly (as if staked before the
	// migration), plus the matching oETH receipt and PublicKeyHex
	// (normally captured by applyTxOnce - set directly here since this
	// test seeds state without a real transaction).
	stakingStore.StakeETH(addr, "ETHTEST", 1000)
	walletStore.Get(addr).OETHBalance = 1000
	walletStore.Get(addr).PublicKeyHex = hex.EncodeToString(pub)
	ethTestStore.Mint(stakeEscrowAddress("ETHTEST"), 1000)

	processLegacyStakeMigrationSweep(core.Block{Index: 1, Timestamp: 5000})

	if got := stakingStore.StakeOf(addr, "ETHTEST"); got != 0 {
		t.Fatalf("expected legacy ETHTEST stake fully redeemed, got %d remaining", got)
	}
	if got := walletStore.Get(addr).OETHBalance; got != 0 {
		t.Fatalf("expected oETH receipt burned on redemption, got %d remaining", got)
	}
	yenAddr := core.DeriveTokenAddress(pub, core.YenAddressTag)
	if got := yenStore.BalanceOf(yenAddr); got == 0 {
		t.Fatalf("expected a nonzero YEN-e payout, got 0")
	}
}

func TestLegacyStakeRedemption_LockedCollateralSkipped(t *testing.T) {
	resetApplyTestState()
	resetLegacyStakeMigrationSweepForTest()

	pub, _, _ := core.GenerateKeyPair()
	addr := core.GenerateAddress(pub)

	// 1000 staked/oETH total, but 600 of it is reserved as an open
	// loan's collateral - only the free 400 should redeem.
	stakingStore.StakeETH(addr, "ETHTEST", 1000)
	walletStore.Get(addr).OETHBalance = 400
	walletStore.Get(addr).PublicKeyHex = hex.EncodeToString(pub)
	ethTestStore.Mint(stakeEscrowAddress("ETHTEST"), 1000)
	loanStore.Loans["loan1"] = &core.Loan{
		ID:               "loan1",
		Borrower:         addr,
		Lender:           "some-lender",
		Status:           core.LoanFunded,
		CollateralSymbol: "OETH",
		CollateralAmount: 600,
	}

	processLegacyStakeMigrationSweep(core.Block{Index: 1, Timestamp: 5000})

	if got := stakingStore.StakeOf(addr, "ETHTEST"); got != 600 {
		t.Fatalf("expected only the free 400 redeemed (600 still staked, backing the loan), got %d staked", got)
	}
	if got := walletStore.Get(addr).OETHBalance; got != 0 {
		t.Fatalf("expected the free 400 oETH burned on redemption, got %d remaining", got)
	}
}

func TestLegacyStakeRedemption_IdempotentAcrossRestart(t *testing.T) {
	resetApplyTestState()
	resetLegacyStakeMigrationSweepForTest()

	pub, _, _ := core.GenerateKeyPair()
	addr := core.GenerateAddress(pub)
	stakingStore.StakeETH(addr, "ETHTEST", 1000)
	walletStore.Get(addr).OETHBalance = 1000
	walletStore.Get(addr).PublicKeyHex = hex.EncodeToString(pub)
	ethTestStore.Mint(stakeEscrowAddress("ETHTEST"), 1000)

	processLegacyStakeMigrationSweep(core.Block{Index: 1, Timestamp: 5000})

	// Simulate a restart: snapshot then restore the sweep state.
	snap := snapshotLegacyStakeMigrationSweep()
	resetLegacyStakeMigrationSweepForTest()
	restoreLegacyStakeMigrationSweep(snap)

	yenAddr := core.DeriveTokenAddress(pub, core.YenAddressTag)
	balAfterFirstRun := yenStore.BalanceOf(yenAddr)

	// A second sweep call post-"restart" must not re-redeem (already
	// zero stake, and the sweep itself should already be marked done).
	processLegacyStakeMigrationSweep(core.Block{Index: 2, Timestamp: 6000})

	if got := yenStore.BalanceOf(yenAddr); got != balAfterFirstRun {
		t.Fatalf("expected no double-mint across a simulated restart: before=%d after=%d", balAfterFirstRun, got)
	}
}

func TestReduceStakeDirect_BoundsCheck(t *testing.T) {
	s := core.NewStakingStore()
	s.StakeETH("addr1", "ETHTEST", 100)

	if s.ReduceStakeDirect("addr1", "ETHTEST", 150) {
		t.Fatalf("expected reducing more than staked to fail")
	}
	if !s.ReduceStakeDirect("addr1", "ETHTEST", 100) {
		t.Fatalf("expected reducing exactly the staked amount to succeed")
	}
	if got := s.StakeOf("addr1", "ETHTEST"); got != 0 {
		t.Fatalf("expected 0 remaining after full reduction, got %d", got)
	}
}
