package api

import (
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// resetPoSRTestState extends resetApplyTestState with the
// validator-specific package vars it doesn't already reset (they're
// declared in api/validators.go, not touched by every other test).
func resetPoSRTestState() {
	resetApplyTestState()
	validatorStore = core.NewValidatorStore()
	delegationStore = core.NewDelegationStore()
	blockchain = nil
}

func TestApplyValidatorRegister_RequiresCCTStakeNotETHTEST(t *testing.T) {
	resetPoSRTestState()

	const addr = "posr-test-ethtest-only"
	w := walletStore.Get(addr)
	w.Reputation = 50
	validatorApplicationStore.Apps[addr] = &core.ValidatorApplication{Address: addr, Status: core.ValidatorApplicationApproved}
	stakingStore.Stakes[addr] = map[string]uint64{"ETHTEST": 5000}
	tokenStore.Mint(addr, ValidatorRegisterFeeCCT)

	tx := core.Transaction{
		ID:   "posr-test-register-ethtest-only",
		From: addr,
		Type: core.TxValidatorRegister,
		Data: `{"nodeId":"node-1","nodePublicKey":"abc"}`,
	}
	applyValidatorRegister(tx)

	if _, ok := validatorStore.Get(addr); ok {
		t.Fatalf("expected registration to fail for a wallet with ETHTEST stake but zero CCT stake")
	}

	const addr2 = "posr-test-cct-staked"
	w2 := walletStore.Get(addr2)
	w2.Reputation = 50
	validatorApplicationStore.Apps[addr2] = &core.ValidatorApplication{Address: addr2, Status: core.ValidatorApplicationApproved}
	stakingStore.Stakes[addr2] = map[string]uint64{"CCT": core.MinValidatorStake}
	tokenStore.Mint(addr2, ValidatorRegisterFeeCCT)

	tx2 := core.Transaction{
		ID:   "posr-test-register-cct",
		From: addr2,
		Type: core.TxValidatorRegister,
		Data: `{"nodeId":"node-1","nodePublicKey":"abc"}`,
	}
	applyValidatorRegister(tx2)

	if _, ok := validatorStore.Get(addr2); !ok {
		t.Fatalf("expected registration to succeed for a wallet with sufficient CCT stake and zero ETHTEST")
	}
}

func TestApplyValidatorRegister_RejectsPermanentlyBannedAddress(t *testing.T) {
	resetPoSRTestState()

	const addr = "posr-test-banned"
	w := walletStore.Get(addr)
	w.Reputation = 50
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": core.MinValidatorStake}
	tokenStore.Mint(addr, ValidatorRegisterFeeCCT)

	status := validatorStore.Register(addr, "node-1", "abc")
	status.PermanentlyBanned = true

	tx := core.Transaction{
		ID:   "posr-test-register-banned",
		From: addr,
		Type: core.TxValidatorRegister,
		Data: `{"nodeId":"node-1","nodePublicKey":"abc"}`,
	}
	applyValidatorRegister(tx)

	if tokenStore.BalanceOf(addr) != ValidatorRegisterFeeCCT {
		t.Fatalf("expected the registration fee to be untouched for a banned address (rejected before the fee transfer), got balance %d", tokenStore.BalanceOf(addr))
	}
}

func TestStakingStoreSlash_ClampsAtAvailableBalance(t *testing.T) {
	resetPoSRTestState()

	const addr = "posr-test-slash-clamp"
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": 30}

	seized := stakingStore.Slash(addr, "CCT", 1000)
	if seized != 30 {
		t.Fatalf("expected Slash to clamp at the actually-staked amount (30), got %d", seized)
	}
	if got := stakingStore.Stakes[addr]["CCT"]; got != 0 {
		t.Fatalf("expected remaining stake 0 after full seizure, got %d", got)
	}

	seized2 := stakingStore.Slash(addr, "CCT", 5)
	if seized2 != 0 {
		t.Fatalf("expected a second slash against an already-zero balance to seize 0, got %d", seized2)
	}
}

func TestApplySlashReport_MissedBlock_SlashesStakeAndBansAfterThreshold(t *testing.T) {
	resetPoSRTestState()

	const addr = "posr-test-slash-integration"
	w := walletStore.Get(addr)
	w.Reputation = 100
	stakingStore.Stakes[addr] = map[string]uint64{"CCT": 10000}
	tokenStore.Mint(stakeEscrowAddress("CCT"), 10000)
	initialSupply := tokenStore.Token.TotalSupply

	validatorStore.Register(addr, "node-1", "abc")

	report := func(roundHash string) {
		data, _ := json.Marshal(slashReportData{
			Reason:           "missed_block",
			ValidatorAddress: addr,
			RoundHash:        roundHash,
		})
		applySlashReport(core.Transaction{ID: "posr-slash-" + roundHash, Data: string(data)})
	}

	report("round-1")

	expectedSlash := uint64(10000) * core.MissedBlockStakeSlashBps / 10000
	if expectedSlash == 0 {
		t.Fatalf("test setup issue: expected a nonzero slash amount at 10000 CCT staked")
	}
	if got := stakingStore.Stakes[addr]["CCT"]; got != 10000-expectedSlash {
		t.Fatalf("expected stake reduced by %d after one missed-block slash, got %d remaining", expectedSlash, got)
	}
	if got := tokenStore.Token.TotalSupply; got != initialSupply-expectedSlash {
		t.Fatalf("expected TotalSupply to drop by the burned amount %d, got delta %d", expectedSlash, initialSupply-got)
	}
	if status, _ := validatorStore.Get(addr); status.PermanentlyBanned {
		t.Fatalf("did not expect a ban after a single missed-block strike")
	}

	// Cross the ban threshold (6 missed-block strikes total, including
	// the one already applied above).
	for i := 2; i <= int(core.PermanentBanSlashCountThreshold); i++ {
		report("round-" + string(rune('0'+i)))
	}

	status, ok := validatorStore.Get(addr)
	if !ok {
		t.Fatalf("expected validator status to still exist")
	}
	if status.SlashCount < core.PermanentBanSlashCountThreshold {
		t.Fatalf("expected SlashCount >= %d after %d strikes, got %d", core.PermanentBanSlashCountThreshold, core.PermanentBanSlashCountThreshold, status.SlashCount)
	}
	if !status.PermanentlyBanned {
		t.Fatalf("expected PermanentlyBanned once SlashCount crosses the threshold")
	}
}

func TestBlockRewardForTimestamp_ReturnsCorrectEraTier(t *testing.T) {
	resetPoSRTestState()

	genesisTs := int64(1_700_000_000)
	blockchain = &core.Blockchain{Blocks: []core.Block{{Index: 0, Timestamp: genesisTs}}}

	cases := []struct {
		name      string
		elapsed   int64
		wantCCT   uint64
	}{
		{"year 1, at genesis", 0, 10},
		{"year 1, near end", secondsPerYear - 1, 10},
		{"year 2 boundary", secondsPerYear, 8},
		{"year 3 boundary", secondsPerYear * 2, 6},
		{"year 4 boundary", secondsPerYear * 3, 5},
		{"year 5 boundary", secondsPerYear * 4, 3},
		{"year 10, held at floor", secondsPerYear * 9, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := blockRewardForTimestamp(genesisTs + c.elapsed)
			if got != c.wantCCT {
				t.Fatalf("expected %d CCT, got %d", c.wantCCT, got)
			}
		})
	}
}

func TestBlockRewardForTimestamp_NoBlockchainFallsBackToBaseline(t *testing.T) {
	resetPoSRTestState()
	if got := blockRewardForTimestamp(123); got != ProduceBlockReward {
		t.Fatalf("expected fallback to ProduceBlockReward when blockchain is unset, got %d", got)
	}
}

func TestMintCCTCapped_ClampsAtSupplyCap(t *testing.T) {
	resetPoSRTestState()

	tokenStore.Token.TotalSupply = MaxTotalCCTSupply - 5

	minted := mintCCTCapped("posr-test-cap-recipient", 100)
	if minted != 5 {
		t.Fatalf("expected mint to clamp to the remaining headroom (5), got %d", minted)
	}
	if got := tokenStore.Token.TotalSupply; got != MaxTotalCCTSupply {
		t.Fatalf("expected TotalSupply to land exactly at the cap, got %d", got)
	}

	mintedAtCap := mintCCTCapped("posr-test-cap-recipient", 50)
	if mintedAtCap != 0 {
		t.Fatalf("expected a mint attempt once already at the cap to mint 0, got %d", mintedAtCap)
	}
}

func TestApplyGenesisPoolAllocation_MintsOnceAndSumsCorrectly(t *testing.T) {
	resetPoSRTestState()

	const foundationAddr = "posr-test-foundation"
	treasuryStore.SetFoundationAddress(foundationAddr)

	applyGenesisPoolAllocation(core.Transaction{ID: "posr-genesis-alloc-1", Data: "{}"})

	total := tokenStore.BalanceOf(genesisPoolAddress(genesisPoolValidatorReward)) +
		tokenStore.BalanceOf(foundationAddr) +
		tokenStore.BalanceOf(genesisPoolAddress(genesisPoolEcosystemDev)) +
		tokenStore.BalanceOf(genesisPoolAddress(genesisPoolGovernanceTreasury)) +
		tokenStore.BalanceOf(genesisPoolAddress(genesisPoolCommunityIncentives))

	const wantTotal = genesisPoolValidatorRewardAmount + genesisPoolFoundationTreasuryAmount +
		genesisPoolEcosystemDevAmount + genesisPoolGovernanceTreasuryAmount + genesisPoolCommunityIncentivesAmount
	if total != wantTotal {
		t.Fatalf("expected genesis pools to sum to %d, got %d", wantTotal, total)
	}
	if !treasuryStore.Treasury.GenesisPoolsAllocated {
		t.Fatalf("expected GenesisPoolsAllocated to be set true")
	}

	// Second attempt must be a no-op.
	applyGenesisPoolAllocation(core.Transaction{ID: "posr-genesis-alloc-2", Data: "{}"})
	totalAfterSecondAttempt := tokenStore.BalanceOf(genesisPoolAddress(genesisPoolValidatorReward)) +
		tokenStore.BalanceOf(foundationAddr) +
		tokenStore.BalanceOf(genesisPoolAddress(genesisPoolEcosystemDev)) +
		tokenStore.BalanceOf(genesisPoolAddress(genesisPoolGovernanceTreasury)) +
		tokenStore.BalanceOf(genesisPoolAddress(genesisPoolCommunityIncentives))
	if totalAfterSecondAttempt != wantTotal {
		t.Fatalf("expected a second allocation attempt to be a no-op, total changed from %d to %d", wantTotal, totalAfterSecondAttempt)
	}
}

func TestApplyAdminTransfer_MovesExistingBalanceWithoutSenderSignature(t *testing.T) {
	resetPoSRTestState()

	const from = "posr-test-unreachable-validator"
	const to = "posr-test-new-validator-wallet"
	tokenStore.Mint(from, 5000)

	data, _ := json.Marshal(adminTransferData{From: from, To: to, Amount: 1000, Symbol: "CCT"})
	applyAdminTransfer(core.Transaction{ID: "posr-admin-transfer-1", Data: string(data)})

	if got := tokenStore.BalanceOf(from); got != 4000 {
		t.Fatalf("expected sender balance reduced to 4000, got %d", got)
	}
	if got := tokenStore.BalanceOf(to); got != 1000 {
		t.Fatalf("expected recipient balance 1000, got %d", got)
	}
}

func TestApplyAdminTransfer_InsufficientBalanceIsNoOp(t *testing.T) {
	resetPoSRTestState()

	const from = "posr-test-poor-sender"
	const to = "posr-test-recipient"
	tokenStore.Mint(from, 10)

	data, _ := json.Marshal(adminTransferData{From: from, To: to, Amount: 1000, Symbol: "CCT"})
	applyAdminTransfer(core.Transaction{ID: "posr-admin-transfer-2", Data: string(data)})

	if got := tokenStore.BalanceOf(from); got != 10 {
		t.Fatalf("expected insufficient-balance transfer to be a no-op, sender balance changed to %d", got)
	}
	if got := tokenStore.BalanceOf(to); got != 0 {
		t.Fatalf("expected recipient to receive nothing, got %d", got)
	}
}
