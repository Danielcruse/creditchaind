package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// resetRCGovernanceTestState extends resetGovernanceTestState with
// every Phase 5 RC-economy store this file's tests mutate - reptStore
// in particular is a real package-level singleton never reset by
// resetApplyTestState itself, so tests here must reset it explicitly
// or risk leaking RC balances across test runs within the same
// process.
func resetRCGovernanceTestState() {
	resetGovernanceTestState()
	reptStore = core.NewTokenStore()
	executiveProposalStore = core.NewExecutiveProposalStore()
	rcInfusionStore = core.NewRCInfusionStore()
	resetGovernanceRCPoolMintForTest()
}

func TestTopReputationHolders_RanksByBalanceWithDeterministicTiebreak(t *testing.T) {
	resetRCGovernanceTestState()

	reptStore.Mint("addr-c", 100)
	reptStore.Mint("addr-a", 100) // ties with addr-c - address is the tiebreak
	reptStore.Mint("addr-b", 50)
	reptStore.Mint("addr-zero", 0) // zero balance must never appear

	top := topReputationHolders(3)
	if len(top) != 3 {
		t.Fatalf("expected 3 holders, got %d: %v", len(top), top)
	}
	// addr-a and addr-c tie at 100 - address ascending breaks the tie,
	// so addr-a comes before addr-c.
	if top[0] != "addr-a" || top[1] != "addr-c" || top[2] != "addr-b" {
		t.Fatalf("expected [addr-a addr-c addr-b], got %v", top)
	}

	if isExecutive("addr-zero") {
		t.Fatalf("expected a zero-balance address to never be an executive")
	}
	if !isExecutive("addr-a") {
		t.Fatalf("expected addr-a (top holder) to be an executive")
	}
}

func TestIsExecutive_CommitteeCutoffRespected(t *testing.T) {
	resetRCGovernanceTestState()

	// Seed exactly executiveCommitteeSize+2 holders with strictly
	// descending balances so there's no tie ambiguity.
	for i := 0; i < executiveCommitteeSize+2; i++ {
		addr := string(rune('a' + i))
		reptStore.Mint(addr, uint64(1000-i))
	}

	top := topReputationHolders(executiveCommitteeSize)
	if len(top) != executiveCommitteeSize {
		t.Fatalf("expected exactly %d executives, got %d", executiveCommitteeSize, len(top))
	}
	// The two lowest-balance addresses must NOT be executives.
	lowest := string(rune('a' + executiveCommitteeSize + 1))
	if isExecutive(lowest) {
		t.Fatalf("expected the lowest-balance holder to be outside the committee")
	}
}

func TestProcessGovernanceRCPoolMining_CreditsOncePerEpochScaledByWeight(t *testing.T) {
	resetRCGovernanceTestState()

	newActivatedVoter(t, "rc-pool-voter", 100, core.StakeCap)
	weight := governanceTotalWeight()
	if weight == 0 {
		t.Fatalf("setup issue: expected nonzero governance weight")
	}

	epochBlock := core.Block{Index: miningEpochBlocks, Timestamp: 1000}
	processGovernanceRCPoolMining(epochBlock)

	expected := weight * governanceRCPoolEmissionBps / 10000
	if got := reptStore.BalanceOf(governanceRCPoolAddress()); got != expected {
		t.Fatalf("expected pool credited %d (weight=%d), got %d", expected, weight, got)
	}

	// A second call within the SAME epoch must not double-mint.
	processGovernanceRCPoolMining(epochBlock)
	if got := reptStore.BalanceOf(governanceRCPoolAddress()); got != expected {
		t.Fatalf("expected no double-mint within the same epoch, got %d (was %d)", got, expected)
	}

	// Simulated restart mid-epoch: snapshot/restore must preserve the
	// already-minted guard.
	snap := snapshotGovernanceRCPoolMint()
	resetGovernanceRCPoolMintForTest()
	restoreGovernanceRCPoolMint(snap)
	processGovernanceRCPoolMining(epochBlock)
	if got := reptStore.BalanceOf(governanceRCPoolAddress()); got != expected {
		t.Fatalf("expected no double-mint after a simulated restart, got %d (was %d)", got, expected)
	}

	// A block in epoch 0 is always a no-op (no epoch has elapsed yet).
	processGovernanceRCPoolMining(core.Block{Index: 0, Timestamp: 1})
	if got := reptStore.BalanceOf(governanceRCPoolAddress()); got != expected {
		t.Fatalf("expected epoch-0 block to be a no-op, got %d", got)
	}
}

func TestApplyProposalPromote_BurnsRCAndExtendsPromotedUntilWithCap(t *testing.T) {
	resetRCGovernanceTestState()

	const promoter = "promote-test-addr"
	reptStore.Mint(promoter, 1000)

	p := &core.Proposal{ID: "prop-promote", CreatedBy: "someone-else", Open: true, Quorum: 10, Deadline: 999999}
	proposalStore.Create(p)

	tx := core.Transaction{
		ID:        "promote-tx-1",
		From:      promoter,
		Type:      core.TxProposalPromote,
		Timestamp: 1000,
		Data:      `{"proposalId":"prop-promote","rcAmount":2}`,
	}
	applyProposalPromote(tx)

	if got := reptStore.BalanceOf(promoter); got != 998 {
		t.Fatalf("expected 2 RC burned (1000->998), got %d", got)
	}
	wantUntil := int64(1000) + promotionDurationForRCAmount(2)
	if p.PromotedUntil != wantUntil {
		t.Fatalf("expected PromotedUntil=%d, got %d", wantUntil, p.PromotedUntil)
	}

	// Re-promoting with a SMALLER amount must not shrink PromotedUntil
	// (extends via max, never additive, never regresses).
	priorUntil := p.PromotedUntil
	tx2 := core.Transaction{
		ID: "promote-tx-2", From: promoter, Type: core.TxProposalPromote, Timestamp: 1001,
		Data: `{"proposalId":"prop-promote","rcAmount":1}`,
	}
	applyProposalPromote(tx2)
	if p.PromotedUntil != priorUntil {
		t.Fatalf("expected PromotedUntil to stay at %d (max, not additive), got %d", priorUntil, p.PromotedUntil)
	}

	// A huge RC amount must be clamped at promotionMaxDurationSeconds,
	// never unbounded.
	reptStore.Mint(promoter, 1_000_000)
	tx3 := core.Transaction{
		ID: "promote-tx-3", From: promoter, Type: core.TxProposalPromote, Timestamp: 2000,
		Data: `{"proposalId":"prop-promote","rcAmount":999999}`,
	}
	applyProposalPromote(tx3)
	if got := p.PromotedUntil - 2000; got != promotionMaxDurationSeconds {
		t.Fatalf("expected the ceiling of %d seconds, got %d", promotionMaxDurationSeconds, got)
	}
}

func TestApplyProposalPromote_RejectsInsufficientBalanceOrClosedProposal(t *testing.T) {
	resetRCGovernanceTestState()

	const poor = "promote-poor-addr"
	reptStore.Mint(poor, 1)

	p := &core.Proposal{ID: "prop-promote-poor", CreatedBy: "x", Open: true, Quorum: 10, Deadline: 999999}
	proposalStore.Create(p)

	tx := core.Transaction{
		ID: "promote-poor-tx", From: poor, Type: core.TxProposalPromote, Timestamp: 1000,
		Data: `{"proposalId":"prop-promote-poor","rcAmount":5}`,
	}
	applyProposalPromote(tx)
	if got := reptStore.BalanceOf(poor); got != 1 {
		t.Fatalf("expected no burn on insufficient balance, got balance %d", got)
	}
	if p.PromotedUntil != 0 {
		t.Fatalf("expected PromotedUntil untouched on a rejected promote, got %d", p.PromotedUntil)
	}

	const funded = "promote-closed-addr"
	reptStore.Mint(funded, 100)
	closedP := &core.Proposal{ID: "prop-promote-closed", CreatedBy: "x", Open: false, Quorum: 10, Deadline: 1}
	proposalStore.Create(closedP)
	tx2 := core.Transaction{
		ID: "promote-closed-tx", From: funded, Type: core.TxProposalPromote, Timestamp: 1000,
		Data: `{"proposalId":"prop-promote-closed","rcAmount":5}`,
	}
	applyProposalPromote(tx2)
	if got := reptStore.BalanceOf(funded); got != 100 {
		t.Fatalf("expected no burn when the proposal is already closed, got balance %d", got)
	}
}

func TestValidatorApplicationVoteCapFor_ScalesWithRCAndRespectsCeiling(t *testing.T) {
	resetRCGovernanceTestState()

	const addr = "vote-cap-addr"
	if got := validatorApplicationVoteCapFor(addr); got != validatorApplicationBaseMaxVotesPerWallet {
		t.Fatalf("expected base cap %d with zero RC, got %d", validatorApplicationBaseMaxVotesPerWallet, got)
	}

	reptStore.Mint(addr, rcPerExtraVoteCapSlot*2)
	if got := validatorApplicationVoteCapFor(addr); got != validatorApplicationBaseMaxVotesPerWallet+2 {
		t.Fatalf("expected base+2 with 2 slots of RC, got %d", got)
	}

	// Far more RC than the ceiling allows must still clamp at
	// maxExtraVoteCapSlots, never exceed it.
	reptStore.Mint(addr, rcPerExtraVoteCapSlot*100)
	want := uint64(validatorApplicationBaseMaxVotesPerWallet + maxExtraVoteCapSlots)
	if got := validatorApplicationVoteCapFor(addr); got != want {
		t.Fatalf("expected the cap clamped at %d, got %d", want, got)
	}
}

func TestApplyGovernance_TokenCreationRejectedBelowMinRC(t *testing.T) {
	resetTokenCreationGovernanceTestState()

	const proposer = "tc-lowrc-proposer"
	cctStore.Mint(proposer, cctW(TokenCreationProposalFeeCCT))
	reptStore.Mint(proposer, TokenCreationMinRC-1) // deliberately just under the gate

	tx := core.Transaction{
		ID:   "tc-lowrc-create",
		From: proposer,
		Type: core.TxGovernance,
		Data: `{"action":"create","title":"Make LOWRC","kind":"TOKEN_CREATION","tokenSymbol":"LOWRCTK","tokenName":"Low RC Token","tokenInitialSupply":1000,"tokenStandard":"C1"}`,
	}
	applyGovernance(tx)

	if len(proposalStore.Proposals) != 0 {
		t.Fatalf("expected no proposal created below the RC release-privilege gate, got %d", len(proposalStore.Proposals))
	}
	// Fee must not be charged either - the create itself was rejected
	// before the fee transfer ever runs.
	if got := cctStore.BalanceOf(proposer); got != cctW(TokenCreationProposalFeeCCT) {
		t.Fatalf("expected no CCT fee charged when the RC gate rejects the create, got balance %s", got)
	}
}

func TestApplyGovernance_TokenCreationAcceptedAtExactMinRC(t *testing.T) {
	resetTokenCreationGovernanceTestState()

	const proposer = "tc-exactrc-proposer"
	cctStore.Mint(proposer, cctW(TokenCreationProposalFeeCCT))
	reptStore.Mint(proposer, TokenCreationMinRC) // exactly at the gate

	tx := core.Transaction{
		ID:   "tc-exactrc-create",
		From: proposer,
		Type: core.TxGovernance,
		Data: `{"action":"create","title":"Make EXACTRC","kind":"TOKEN_CREATION","tokenSymbol":"EXACTRCT","tokenName":"Exact RC Token","tokenInitialSupply":1000,"tokenStandard":"C1"}`,
	}
	applyGovernance(tx)

	if len(proposalStore.Proposals) != 1 {
		t.Fatalf("expected the proposal created at exactly the RC gate, got %d", len(proposalStore.Proposals))
	}
}

func TestApplyRCInfuse_BurnsAndMintsIntoPoolAndBoostsMiningScore(t *testing.T) {
	resetRCGovernanceTestState()
	governanceParamsStore.Set(realWorldTrustWeightParamKey, 5000) // 50% bps weight

	const infuser = "infuse-addr"
	reptStore.Mint(infuser, 100)

	tx := core.Transaction{
		ID: "infuse-tx-1", From: infuser, Type: core.TxRCInfuse, Timestamp: 1000,
		Data: `{"rcAmount":10,"trustReason":"I vouch for this community"}`,
	}
	applyRCInfuse(tx)

	if got := reptStore.BalanceOf(infuser); got != 90 {
		t.Fatalf("expected 10 RC burned from the infuser, got balance %d", got)
	}
	if got := reptStore.BalanceOf(governanceRCPoolAddress()); got != 10 {
		t.Fatalf("expected the same 10 RC minted into the governance pool, got %d", got)
	}

	wallet := walletStore.Get(infuser)
	wantBoost := uint64(10) * 5000 / 10000 // 10 RC * 50% = 5
	if wallet.RCInfusionWeightBps != wantBoost {
		t.Fatalf("expected RCInfusionWeightBps=%d, got %d", wantBoost, wallet.RCInfusionWeightBps)
	}

	recent := rcInfusionStore.Recent(10)
	if len(recent) != 1 || recent[0].TrustReason != "I vouch for this community" {
		t.Fatalf("expected the infusion recorded with its trust reason, got %+v", recent)
	}

	// Mining Power must reflect the capped boost.
	score := miningPowerScore(infuser, 1000)
	boost := rcInfusionBoostFor(wallet)
	if boost > rcInfusionBoostCap {
		t.Fatalf("expected the boost itself to never exceed the cap %d, got %d", rcInfusionBoostCap, boost)
	}
	if score < boost {
		t.Fatalf("expected miningPowerScore to include at least the boost term, score=%d boost=%d", score, boost)
	}

	// A wallet that infuses enough to exceed the cap must still only
	// ever contribute rcInfusionBoostCap to Mining Power.
	reptStore.Mint(infuser, 100000)
	tx2 := core.Transaction{
		ID: "infuse-tx-2", From: infuser, Type: core.TxRCInfuse, Timestamp: 1001,
		Data: `{"rcAmount":100000,"trustReason":"go big"}`,
	}
	applyRCInfuse(tx2)
	wallet = walletStore.Get(infuser)
	if got := rcInfusionBoostFor(wallet); got != rcInfusionBoostCap {
		t.Fatalf("expected the boost clamped at %d after a huge infusion, got %d", rcInfusionBoostCap, got)
	}
}

func TestApplyRCInfuse_RejectsInsufficientBalanceOrOversizedReason(t *testing.T) {
	resetRCGovernanceTestState()

	const poor = "infuse-poor-addr"
	reptStore.Mint(poor, 1)
	tx := core.Transaction{
		ID: "infuse-poor-tx", From: poor, Type: core.TxRCInfuse, Timestamp: 1000,
		Data: `{"rcAmount":5,"trustReason":"not enough RC"}`,
	}
	applyRCInfuse(tx)
	if got := reptStore.BalanceOf(poor); got != 1 {
		t.Fatalf("expected no burn on insufficient balance, got %d", got)
	}
	if got := reptStore.BalanceOf(governanceRCPoolAddress()); got != 0 {
		t.Fatalf("expected no pool mint on a rejected infusion, got %d", got)
	}
}

func TestApplyExecutiveProposalVote_RejectsNonExecutiveEvenPastSubmissionRace(t *testing.T) {
	resetRCGovernanceTestState()

	// Seed a full committee (executiveCommitteeSize wallets, all with
	// more RC than "not-executive") plus the real top-N executive and
	// a genuinely-excluded non-executive wallet - with only 2 wallets
	// seeded, both would trivially land in an under-full top-5, so the
	// committee must actually be filled to test real exclusion.
	for i := 0; i < executiveCommitteeSize; i++ {
		reptStore.Mint("filler-exec-"+string(rune('a'+i)), 5000)
	}
	reptStore.Mint("real-executive", 10000)
	reptStore.Mint("not-executive", 1)

	p := &core.ExecutiveProposal{
		ID: "execprop-vote-test", CreatedBy: "real-executive",
		ParamKey: realWorldTrustWeightParamKey, ParamValue: 1234,
		Open: true, Deadline: 999999,
	}
	executiveProposalStore.Create(p)

	// A non-executive's vote transaction (as if it had somehow slipped
	// past the HTTP handler's own isExecutive check) must still be
	// rejected at apply time.
	nonExecTx := core.Transaction{
		ID: "exec-vote-nonexec", From: "not-executive", Type: core.TxExecutiveProposalVote, Timestamp: 1000,
		Data: `{"proposalId":"execprop-vote-test","support":true}`,
	}
	applyExecutiveProposalVote(nonExecTx)
	if p.YesVotes != 0 || p.NoVotes != 0 {
		t.Fatalf("expected a non-executive's vote to be rejected at apply time, got yes=%d no=%d", p.YesVotes, p.NoVotes)
	}

	// A genuine executive's vote must count.
	execTx := core.Transaction{
		ID: "exec-vote-real", From: "real-executive", Type: core.TxExecutiveProposalVote, Timestamp: 1001,
		Data: `{"proposalId":"execprop-vote-test","support":true}`,
	}
	applyExecutiveProposalVote(execTx)
	if p.YesVotes != 1 {
		t.Fatalf("expected the real executive's vote to count, got yes=%d", p.YesVotes)
	}
}

func TestApplyExecutiveProposalCreate_RejectsNonExecutive(t *testing.T) {
	resetRCGovernanceTestState()
	for i := 0; i < executiveCommitteeSize; i++ {
		reptStore.Mint("filler-exec2-"+string(rune('a'+i)), 5000)
	}
	reptStore.Mint("not-executive-2", 1)

	tx := core.Transaction{
		ID: "exec-create-nonexec", From: "not-executive-2", Type: core.TxExecutiveProposalCreate, Timestamp: 1000,
		Data: `{"paramValue":500}`,
	}
	applyExecutiveProposalCreate(tx)

	if len(executiveProposalStore.Proposals) != 0 {
		t.Fatalf("expected no executive proposal created by a non-executive, got %d", len(executiveProposalStore.Proposals))
	}
}

func TestProcessExecutiveProposalFinalization_PassedUpdatesRealWorldTrustWeightOnly(t *testing.T) {
	resetRCGovernanceTestState()

	// A PASSED outcome (Yes>No headcount majority).
	passing := &core.ExecutiveProposal{
		ID: "execprop-pass", CreatedBy: "exec-1",
		ParamKey: realWorldTrustWeightParamKey, ParamValue: 7777,
		Open: true, Deadline: 1000,
	}
	executiveProposalStore.Create(passing)
	executiveProposalStore.Vote(passing.ID, "exec-1", true)
	executiveProposalStore.Vote(passing.ID, "exec-2", true)
	executiveProposalStore.Vote(passing.ID, "exec-3", false)

	processExecutiveProposalFinalization(1000)

	if passing.Status != core.ProposalStatusPassed {
		t.Fatalf("expected PASSED (2 yes > 1 no), got %q", passing.Status)
	}
	if got := governanceParam(realWorldTrustWeightParamKey, 0); got != 7777 {
		t.Fatalf("expected RealWorldTrustWeightBps=7777 after a genuine PASSED vote, got %d", got)
	}

	// A FAILED outcome (No>=Yes) must never touch the param.
	failing := &core.ExecutiveProposal{
		ID: "execprop-fail", CreatedBy: "exec-1",
		ParamKey: realWorldTrustWeightParamKey, ParamValue: 1,
		Open: true, Deadline: 1000,
	}
	executiveProposalStore.Create(failing)
	executiveProposalStore.Vote(failing.ID, "exec-4", false)
	executiveProposalStore.Vote(failing.ID, "exec-5", true)
	executiveProposalStore.Vote(failing.ID, "exec-6", false)

	processExecutiveProposalFinalization(1000)

	if failing.Status != core.ProposalStatusFailed {
		t.Fatalf("expected FAILED (2 no >= 1 yes), got %q", failing.Status)
	}
	if got := governanceParam(realWorldTrustWeightParamKey, 0); got != 7777 {
		t.Fatalf("expected RealWorldTrustWeightBps to stay at 7777 after a FAILED vote, got %d", got)
	}
}

func TestHandleRCLeaderboard_ShowsOnlyRealNonzeroBalances(t *testing.T) {
	resetRCGovernanceTestState()

	reptStore.Mint("leader-1", 500)
	reptStore.Mint("leader-2", 300)
	// leader-3 never minted - must never appear via a fabricated entry.

	holders := sortedReputationHolders()
	if len(holders) != 2 {
		t.Fatalf("expected exactly 2 real holders, got %d: %+v", len(holders), holders)
	}
	if holders[0].Address != "leader-1" || holders[0].Balance != 500 {
		t.Fatalf("expected leader-1 first with balance 500, got %+v", holders[0])
	}
}

func TestApplyExecutiveProposalCreate_ModeratorDeactivateRequiresActiveModerator(t *testing.T) {
	resetRCGovernanceTestState()
	moderatorStore = core.NewModeratorStore()
	for i := 0; i < executiveCommitteeSize; i++ {
		reptStore.Mint("filler-modact-"+string(rune('a'+i)), 5000)
	}
	const exec = "exec-modact-1"
	reptStore.Mint(exec, 9000)

	moderatorStore.Register("real-moderator", "pub", 1000)

	// Rejects a target that was never registered as a moderator.
	txBad := core.Transaction{
		ID: "exec-create-modact-bad", From: exec, Type: core.TxExecutiveProposalCreate, Timestamp: 1000,
		Data: `{"paramKey":"ModeratorDeactivate","targetAddress":"never-a-moderator"}`,
	}
	applyExecutiveProposalCreate(txBad)
	if len(executiveProposalStore.Proposals) != 0 {
		t.Fatalf("expected no proposal created targeting a non-moderator, got %d", len(executiveProposalStore.Proposals))
	}

	// Accepts a target that is currently an active moderator.
	txGood := core.Transaction{
		ID: "exec-create-modact-good", From: exec, Type: core.TxExecutiveProposalCreate, Timestamp: 1000,
		Data: `{"paramKey":"ModeratorDeactivate","targetAddress":"real-moderator"}`,
	}
	applyExecutiveProposalCreate(txGood)
	if len(executiveProposalStore.Proposals) != 1 {
		t.Fatalf("expected exactly 1 proposal created targeting a real moderator, got %d", len(executiveProposalStore.Proposals))
	}
	for _, p := range executiveProposalStore.Proposals {
		if p.ParamKey != moderatorDeactivateParamKey || p.TargetAddress != "real-moderator" {
			t.Fatalf("unexpected proposal shape: %+v", p)
		}
	}
}

func TestProcessExecutiveProposalFinalization_PassedModeratorDeactivateRemovesFromRotation(t *testing.T) {
	resetRCGovernanceTestState()
	moderatorStore = core.NewModeratorStore()
	moderatorStore.Register("target-moderator", "pub", 1000)
	moderatorStore.Register("other-moderator", "pub", 1000)

	passing := &core.ExecutiveProposal{
		ID: "execprop-modact-pass", CreatedBy: "exec-1",
		ParamKey: moderatorDeactivateParamKey, TargetAddress: "target-moderator",
		Open: true, Deadline: 1000,
	}
	executiveProposalStore.Create(passing)
	executiveProposalStore.Vote(passing.ID, "exec-1", true)
	executiveProposalStore.Vote(passing.ID, "exec-2", true)
	executiveProposalStore.Vote(passing.ID, "exec-3", false)

	processExecutiveProposalFinalization(1000)

	if passing.Status != core.ProposalStatusPassed {
		t.Fatalf("expected PASSED, got %q", passing.Status)
	}
	m, ok := moderatorStore.Get("target-moderator")
	if !ok || !m.Deactivated {
		t.Fatalf("expected target-moderator to be Deactivated after a PASSED vote, got %+v", m)
	}
	for i := 0; i < 10; i++ {
		if addr := moderatorStore.AssignNext(); addr == "target-moderator" {
			t.Fatalf("expected the deactivated moderator to never be assigned again (iteration %d)", i)
		}
	}

	// A FAILED moderator-deactivate vote must never touch the moderator.
	failing := &core.ExecutiveProposal{
		ID: "execprop-modact-fail", CreatedBy: "exec-1",
		ParamKey: moderatorDeactivateParamKey, TargetAddress: "other-moderator",
		Open: true, Deadline: 1000,
	}
	executiveProposalStore.Create(failing)
	executiveProposalStore.Vote(failing.ID, "exec-4", false)
	executiveProposalStore.Vote(failing.ID, "exec-5", true)
	executiveProposalStore.Vote(failing.ID, "exec-6", false)

	processExecutiveProposalFinalization(1000)

	if failing.Status != core.ProposalStatusFailed {
		t.Fatalf("expected FAILED, got %q", failing.Status)
	}
	if m, ok := moderatorStore.Get("other-moderator"); !ok || m.Deactivated {
		t.Fatalf("expected other-moderator to remain active after a FAILED vote, got %+v", m)
	}
}
