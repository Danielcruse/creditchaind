package api

import (
	"encoding/json"
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestApplyP2PModeratorResolve_PreCutoverUsesFlatReputationOnly is the
// grandfathering test for TrustDimensionsCutoverUnixSeconds - same
// H01FixCutoverUnixSeconds/H03FixCutoverUnixSeconds-style guarantee: a
// transaction timestamped before the cutover must apply through the
// old flat-Reputation-only path byte-for-byte, exactly as it always
// has, so a node resyncing testnet's real history reaches identical
// state to a node that processed it live. Trust must stay at its zero
// value entirely - not even seeded - since the dimensioned path never
// runs at all pre-cutover.
func TestApplyP2PModeratorResolve_PreCutoverUsesFlatReputationOnly(t *testing.T) {
	resetApplyTestState()
	p2pOfferStore = core.NewP2POfferStore()
	reputationEventStore = core.NewReputationEventStore()

	const seller, buyer, moderator = "precutover-seller", "precutover-buyer", "precutover-moderator"
	// Register directly (not walletStore.Get, which auto-vivifies via
	// NewWallet() - that now explicitly pre-seeds Trust=50, since a
	// genuinely NEW wallet should never need lazy seeding at all, see
	// trust.go) to simulate a wallet that actually predates
	// TrustDimensions existing: real Reputation, Trust still at its
	// zero value.
	walletStore.Register(&core.Wallet{Address: seller, Reputation: 50})

	buildDisputedP2POffer(t, "precutover-offer", seller, buyer, moderator, 25)

	preCutover := TrustDimensionsCutoverUnixSeconds - 3600
	tx := core.Transaction{
		ID: "precutover-resolve", From: moderator, Type: core.TxP2PModeratorResolve,
		Timestamp: preCutover, Data: `{"offerId":"precutover-offer","release":true}`,
	}
	applyP2PModeratorResolve(tx)

	sellerWallet := walletStore.Get(seller)
	if sellerWallet.Reputation != 50-ReputationDeltaP2PFraud {
		t.Fatalf("expected flat Reputation decreased by %d (from 50), got %d", ReputationDeltaP2PFraud, sellerWallet.Reputation)
	}
	if sellerWallet.Trust != (core.TrustDimensions{}) {
		t.Fatalf("expected Trust to stay entirely unseeded (zero value) pre-cutover, got %+v", sellerWallet.Trust)
	}
}

// TestApplyP2PModeratorResolve_PostCutoverUsesDisputeDimension is the
// new-behavior counterpart: a transaction timestamped after the
// cutover seeds Trust from the wallet's existing flat Reputation, then
// moves ONLY Dispute Trust (see core/credit_engine.go and this file's
// mapping - a P2P fraud finding is dispute conduct, not routine
// trading, so it must not also drag down Trading Trust), with the
// mirrored Reputation reflecting the new weighted blend rather than a
// simple 1:1 subtraction.
func TestApplyP2PModeratorResolve_PostCutoverUsesDisputeDimension(t *testing.T) {
	resetApplyTestState()
	p2pOfferStore = core.NewP2POfferStore()
	reputationEventStore = core.NewReputationEventStore()

	const seller, buyer, moderator = "postcutover-seller", "postcutover-buyer", "postcutover-moderator"
	walletStore.Get(seller).Reputation = 50

	buildDisputedP2POffer(t, "postcutover-offer", seller, buyer, moderator, 25)

	postCutover := TrustDimensionsCutoverUnixSeconds + 3600
	tx := core.Transaction{
		ID: "postcutover-resolve", From: moderator, Type: core.TxP2PModeratorResolve,
		Timestamp: postCutover, Data: `{"offerId":"postcutover-offer","release":true}`,
	}
	applyP2PModeratorResolve(tx)

	sellerWallet := walletStore.Get(seller)
	wantTrust := core.TrustDimensions{
		RepaymentTrust: 50, TradingTrust: 50, GovernanceTrust: 50,
		ContributionTrust: 50, VerificationTrust: 50, DisputeTrust: 50 - ReputationDeltaP2PFraud,
	}
	if sellerWallet.Trust != wantTrust {
		t.Fatalf("expected only DisputeTrust to move (to %d), got %+v", 50-ReputationDeltaP2PFraud, sellerWallet.Trust)
	}
	wantReputation := core.ComputeTSI(wantTrust, core.DefaultTrustWeights)
	if sellerWallet.Reputation != wantReputation {
		t.Fatalf("expected mirrored Reputation %d (ComputeTSI of the new Trust), got %d", wantReputation, sellerWallet.Reputation)
	}
	// Trading Trust specifically must be untouched - the whole point of
	// splitting Dispute out from Trading (see trust.go's mapping
	// reasoning) is that a fraud finding shouldn't also tank routine
	// marketplace standing.
	if sellerWallet.Trust.TradingTrust != 50 {
		t.Fatalf("expected TradingTrust to stay at 50 (untouched by a Dispute event), got %d", sellerWallet.Trust.TradingTrust)
	}
}

// TestTrustDimensions_LazySeedingConvergesRegardlessOfEventOrder is the
// determinism test for lazy per-wallet seeding: two independently-
// applying "nodes" (separate wallet stores here, standing in for two
// real nodes applying the same two post-cutover transactions touching
// the same wallet, in the opposite order gossip/block-application
// could plausibly deliver them) must converge on byte-identical final
// Trust/Reputation state regardless of which event happens to touch
// (and therefore seed) the wallet first.
func TestTrustDimensions_LazySeedingConvergesRegardlessOfEventOrder(t *testing.T) {
	const addr = "lazy-seed-convergence-wallet"
	postCutover := TrustDimensionsCutoverUnixSeconds + 3600

	// Register directly, not walletStore.Get - see the identical note
	// in TestApplyP2PModeratorResolve_PreCutoverUsesFlatReputationOnly
	// on why Get() would wrongly pre-seed Trust via NewWallet().

	// Node A: Trading event first, then Contribution event.
	nodeA := core.NewWalletStore()
	nodeA.Register(&core.Wallet{Address: addr, Reputation: 62}) // simulates real pre-cutover history
	walletA := nodeA.Get(addr)
	increaseTrust(walletA, core.DimensionTrading, 14, postCutover)
	increaseTrust(walletA, core.DimensionContribution, 5, postCutover)

	// Node B: same two events, opposite order.
	nodeB := core.NewWalletStore()
	nodeB.Register(&core.Wallet{Address: addr, Reputation: 62})
	walletB := nodeB.Get(addr)
	increaseTrust(walletB, core.DimensionContribution, 5, postCutover)
	increaseTrust(walletB, core.DimensionTrading, 14, postCutover)

	if walletA.Trust != walletB.Trust {
		t.Fatalf("expected identical Trust regardless of event order, got A=%+v B=%+v", walletA.Trust, walletB.Trust)
	}
	if walletA.Reputation != walletB.Reputation {
		t.Fatalf("expected identical mirrored Reputation regardless of event order, got A=%d B=%d", walletA.Reputation, walletB.Reputation)
	}

	wantTrust := core.TrustDimensions{
		RepaymentTrust: 62, TradingTrust: 76, GovernanceTrust: 62,
		ContributionTrust: 67, VerificationTrust: 62, DisputeTrust: 62,
	}
	if walletA.Trust != wantTrust {
		t.Fatalf("expected seeded-then-applied Trust %+v, got %+v", wantTrust, walletA.Trust)
	}
}

// TestMigrateWalletAssets_TransfersTrustAlongsideReputation is the
// regression test for a gap found during self-review: migrateWalletAssets
// already transferred Reputation/HighestTierReached from the old
// address to the new one and zeroed the old wallet's Reputation, but
// originally left Trust untouched entirely - silently losing a
// migrating wallet's real accumulated per-dimension history in favor
// of newWallet's own pre-seeded all-50 default, even though the flat
// Reputation number correctly carried over.
func TestMigrateWalletAssets_TransfersTrustAlongsideReputation(t *testing.T) {
	resetApplyTestState()

	const oldAddr, newAddr = "migrate-trust-old", "migrate-trust-new"
	oldWallet := walletStore.Get(oldAddr)
	oldWallet.Reputation = 73
	oldWallet.Trust = core.TrustDimensions{
		RepaymentTrust: 80, TradingTrust: 65, GovernanceTrust: 73,
		ContributionTrust: 90, VerificationTrust: 73, DisputeTrust: 55,
	}
	wantTransferred := oldWallet.Trust

	migrateWalletAssets(oldAddr, newAddr, "", "")

	newWallet := walletStore.Get(newAddr)
	if newWallet.Trust != wantTransferred {
		t.Fatalf("expected the new wallet's Trust to match the old wallet's real history %+v, got %+v", wantTransferred, newWallet.Trust)
	}
	if newWallet.Reputation != 73 {
		t.Fatalf("expected the new wallet's Reputation to still be transferred correctly (73), got %d", newWallet.Reputation)
	}

	oldWalletAfter := walletStore.Get(oldAddr)
	if oldWalletAfter.Trust != (core.TrustDimensions{}) {
		t.Fatalf("expected the old (now-frozen) wallet's Trust zeroed for consistency with its zeroed Reputation, got %+v", oldWalletAfter.Trust)
	}
	if oldWalletAfter.Reputation != 0 {
		t.Fatalf("expected the old wallet's Reputation still zeroed (0), got %d", oldWalletAfter.Reputation)
	}
}

// TestProcessProposalFinalization_GrantsGovernanceTrustOnPassedOnly is
// the regression test for the first real event feeding Governance
// Trust (see trust.go) - the proposer and voters of a proposal that
// genuinely reaches quorum and passes each gain Governance Trust
// (asymmetric: proposer 5, voter 1, mirroring
// ModeratorReputationBonus/ModeratorConfirmReputationBonus's own
// register-vs-confirm asymmetry), and a FAILED proposal grants
// nothing at all - deliberately reusing the "genuinely passed" signal
// rather than "cast a vote," which would be trivially farmable by
// voting on everything indiscriminately.
func TestProcessProposalFinalization_GrantsGovernanceTrustOnPassedOnly(t *testing.T) {
	resetGovernanceTestState()

	newActivatedVoter(t, "govtrust-proposer", 50, 0)
	newActivatedVoter(t, "govtrust-voter", 50, core.StakeCap)

	postCutover := TrustDimensionsCutoverUnixSeconds + 3600
	passing := &core.Proposal{
		ID: "prop-govtrust-pass", CreatedBy: "govtrust-proposer",
		Open: true, Quorum: 10, Deadline: postCutover,
	}
	proposalStore.Create(passing)
	proposalStore.Vote(passing.ID, "govtrust-voter", true, votingWeight("govtrust-voter"), postCutover-100)

	processProposalFinalization(postCutover)

	if passing.Status != core.ProposalStatusPassed {
		t.Fatalf("setup issue: expected proposal to pass, got %q", passing.Status)
	}

	proposerWallet := walletStore.Get("govtrust-proposer")
	if got := proposerWallet.Trust.GovernanceTrust; got != 50+GovernanceTrustDeltaProposerPassed {
		t.Fatalf("expected proposer GovernanceTrust %d, got %d", 50+GovernanceTrustDeltaProposerPassed, got)
	}
	voterWallet := walletStore.Get("govtrust-voter")
	if got := voterWallet.Trust.GovernanceTrust; got != 50+GovernanceTrustDeltaVoterPassed {
		t.Fatalf("expected voter GovernanceTrust %d, got %d", 50+GovernanceTrustDeltaVoterPassed, got)
	}
	// Other dimensions must stay untouched.
	if proposerWallet.Trust.TradingTrust != 50 || proposerWallet.Trust.RepaymentTrust != 50 {
		t.Fatalf("expected only GovernanceTrust to move for the proposer, got %+v", proposerWallet.Trust)
	}

	// A FAILED proposal (quorum never reached) grants nothing.
	newActivatedVoter(t, "govtrust-proposer-fail", 50, 0)
	failing := &core.Proposal{
		ID: "prop-govtrust-fail", CreatedBy: "govtrust-proposer-fail",
		Open: true, Quorum: 99999, Deadline: postCutover,
	}
	proposalStore.Create(failing)
	processProposalFinalization(postCutover)

	if failing.Status != core.ProposalStatusFailed {
		t.Fatalf("setup issue: expected proposal to fail, got %q", failing.Status)
	}
	// newActivatedVoter's own walletStore.Get already auto-vivifies via
	// NewWallet(), which pre-seeds Trust to all-50 (see wallet.go) -
	// that's unrelated to the failed-proposal outcome under test here,
	// so the correct check is GovernanceTrust staying at exactly that
	// baseline (untouched by the finalization path), not that Trust is
	// literally still the zero value.
	failedProposerWallet := walletStore.Get("govtrust-proposer-fail")
	if got := failedProposerWallet.Trust.GovernanceTrust; got != 50 {
		t.Fatalf("expected a FAILED proposal's proposer to gain no Governance Trust at all (still 50), got %d", got)
	}
}

// TestApplyKYCSubmit_GrantsVerificationTrustOnceOnly is the regression
// test for the first (and so far only) real event feeding Verification
// Trust - a one-time bonus on genuine KYC self-attestation, and a
// resubmission (e.g. correcting a typo'd name) must never re-grant it.
func TestApplyKYCSubmit_GrantsVerificationTrustOnceOnly(t *testing.T) {
	resetApplyTestState()

	const addr = "kyc-verification-trust-wallet"
	walletStore.Get(addr).Reputation = 50

	postCutover := TrustDimensionsCutoverUnixSeconds + 3600
	data, _ := json.Marshal(kycSubmitData{FullName: "Test User", DateOfBirth: "2000-01-01", Country: "Testland", AgreedTerms: true})
	applyKYCSubmit(core.Transaction{ID: "kyc-submit-1", From: addr, Data: string(data), Timestamp: postCutover})

	wallet := walletStore.Get(addr)
	if got := wallet.Trust.VerificationTrust; got != 50+VerificationTrustDeltaKYCSubmit {
		t.Fatalf("expected VerificationTrust %d after first submission, got %d", 50+VerificationTrustDeltaKYCSubmit, got)
	}

	// Resubmission (correcting a field) must not re-grant the bonus.
	data2, _ := json.Marshal(kycSubmitData{FullName: "Test User Corrected", DateOfBirth: "2000-01-01", Country: "Testland", AgreedTerms: true})
	applyKYCSubmit(core.Transaction{ID: "kyc-submit-2", From: addr, Data: string(data2), Timestamp: postCutover + 100})

	if got := wallet.Trust.VerificationTrust; got != 50+VerificationTrustDeltaKYCSubmit {
		t.Fatalf("expected VerificationTrust to stay at %d after a resubmission, got %d", 50+VerificationTrustDeltaKYCSubmit, got)
	}
	rec, ok := kycStore.Get(addr)
	if !ok || rec.FullName != "Test User Corrected" {
		t.Fatalf("expected the resubmission to still update the KYC record itself, got %+v (ok=%v)", rec, ok)
	}
}

// TestVotingWeight_FollowsGovernanceTrustNotOverallTSI is the
// regression test for the confirmed governance-vote-weight migration -
// now that Governance Trust has real events feeding it (proposal
// pass/vote), a wallet's vote weight should track its own governance
// conduct specifically, not get diluted or inflated by an unrelated
// dimension.
func TestVotingWeight_FollowsGovernanceTrustNotOverallTSI(t *testing.T) {
	resetGovernanceTestState()

	const addr = "voteweight-dimension-wallet"
	w := walletStore.Get(addr)
	w.VotingPower = 1
	w.Reputation = 50
	// Strong Governance Trust, but a real Dispute Trust hit drags
	// overall TSI down - vote weight must follow the former, not the
	// latter.
	w.Trust = core.TrustDimensions{
		RepaymentTrust: 50, TradingTrust: 50, GovernanceTrust: 90,
		ContributionTrust: 50, VerificationTrust: 50, DisputeTrust: 10,
	}
	overallTSI := core.ComputeTSI(w.Trust, core.DefaultTrustWeights)
	if overallTSI >= 90 {
		t.Fatalf("test setup invalid: expected overall TSI dragged below GovernanceTrust (90) by the weak Dispute dimension, got %d", overallTSI)
	}

	// base = 1 + 0*40/100 + 90*60/100 = 1 + 54 = 55
	if got := votingWeight(addr); got != 55 {
		t.Fatalf("expected vote weight 55 (derived from GovernanceTrust=90), got %d", got)
	}
}

// TestTrustDimensionValue_CreditTermsFollowRepaymentTrustNotOverallTSI
// is the regression test for the confirmed design decision: borrow
// limits and interest rates key off Repayment Trust specifically, not
// overall TSI - so a wallet with excellent repayment history but a
// weak Trading/Dispute dimension (e.g. a past marketplace fraud
// finding) still gets the credit terms its actual repayment behavior
// earns, rather than having an unrelated dimension drag its overall
// score (and therefore its credit terms) down too.
func TestTrustDimensionValue_CreditTermsFollowRepaymentTrustNotOverallTSI(t *testing.T) {
	w := &core.Wallet{Reputation: 50}
	// Excellent Repayment Trust (Genesis-tier, 95+), but a real Dispute
	// Trust hit drags overall TSI down into a lower tier.
	w.Trust = core.TrustDimensions{
		RepaymentTrust: 96, TradingTrust: 50, GovernanceTrust: 50,
		ContributionTrust: 50, VerificationTrust: 50, DisputeTrust: 20,
	}
	overallTSI := core.ComputeTSI(w.Trust, core.DefaultTrustWeights)
	if overallTSI >= 80 {
		t.Fatalf("test setup invalid: expected overall TSI dragged below Diamond (80) by the weak Dispute dimension, got %d", overallTSI)
	}

	repaymentValue := core.TrustDimensionValue(w, core.DimensionRepayment)
	if repaymentValue != 96 {
		t.Fatalf("expected TrustDimensionValue to read RepaymentTrust (96) directly, got %d", repaymentValue)
	}

	// The wallet should get Genesis-tier borrow limit/interest rate off
	// its real repayment history, not the dragged-down overall score.
	if got := core.BorrowLimit(repaymentValue); got != core.BorrowLimit(96) {
		t.Fatalf("expected Genesis-tier borrow limit from Repayment Trust 96, got limit for %d instead", got)
	}
	if gotFromOverall := core.BorrowLimit(overallTSI); gotFromOverall == core.BorrowLimit(repaymentValue) {
		t.Fatalf("test didn't actually exercise the fix - overall TSI and Repayment Trust produced the same borrow limit, tighten the test's dimension spread")
	}
}

// withRestoredDefaultTrustWeights snapshots core.DefaultTrustWeights and
// governanceParamsStore before a test that mutates either (both are
// package-level mutable state shared across the whole test binary, same
// pollution risk core.ActivatedProtocolVersion tests already guard
// against) and restores both on cleanup, so a governance-weight test
// can't leave a changed weight bleeding into an unrelated test that
// assumes core.DefaultTrustWeights.
func withRestoredDefaultTrustWeights(t *testing.T) {
	t.Helper()
	savedWeights := core.DefaultTrustWeights
	savedParams := governanceParamsStore
	t.Cleanup(func() {
		core.DefaultTrustWeights = savedWeights
		governanceParamsStore = savedParams
	})
}

// TestApplyAdminSetParam_UpdatesDefaultTrustWeights covers the admin
// fast-path half of governance-adjustable TSI weights: applyAdminSetParam
// writing a TSIWeight*Bps key must be reflected in core.DefaultTrustWeights
// immediately (via syncTrustWeightIfNeeded), not just in
// governanceParamsStore's own map - otherwise a subsequent ComputeTSI call
// would silently keep blending with the stale compiled-in default.
func TestApplyAdminSetParam_UpdatesDefaultTrustWeights(t *testing.T) {
	withRestoredDefaultTrustWeights(t)

	data, _ := json.Marshal(adminSetParamData{Key: "TSIWeightRepaymentBps", Value: 5000})
	applyAdminSetParam(core.Transaction{ID: "admin-tsi-weight", Data: string(data), Timestamp: 1000})

	if got := core.DefaultTrustWeights.RepaymentBps; got != 5000 {
		t.Fatalf("expected core.DefaultTrustWeights.RepaymentBps updated to 5000 by the admin fast path, got %d", got)
	}
	if got, ok := governanceParamsStore.Get("TSIWeightRepaymentBps"); !ok || got != 5000 {
		t.Fatalf("expected governanceParamsStore itself also updated to 5000, got %d (ok=%v)", got, ok)
	}
}

// TestFinalizeProposal_ParamChange_UpdatesDefaultTrustWeights covers the
// community-governance half of the same mechanism - a passed
// PARAM_CHANGE proposal targeting a TSIWeight*Bps key must reach
// core.DefaultTrustWeights exactly like the admin fast path does, since
// both are meant to be equally authoritative writers (see
// syncTrustWeightIfNeeded's doc comment).
func TestFinalizeProposal_ParamChange_UpdatesDefaultTrustWeights(t *testing.T) {
	withRestoredDefaultTrustWeights(t)
	resetGovernanceTestState()

	newActivatedVoter(t, "paramchange-proposer", 50, core.StakeCap)

	postCutover := TrustDimensionsCutoverUnixSeconds + 3600
	p := &core.Proposal{
		ID: "prop-tsi-weight-change", CreatedBy: "paramchange-proposer",
		Kind: core.ProposalKindParamChange, ParamKey: "TSIWeightDisputeBps", ParamValue: 2500,
		Open: true, Quorum: 1, Deadline: postCutover,
	}
	proposalStore.Create(p)
	proposalStore.Vote(p.ID, "paramchange-proposer", true, votingWeight("paramchange-proposer"), postCutover-100)

	processProposalFinalization(postCutover)

	if p.Status != core.ProposalStatusPassed {
		t.Fatalf("setup issue: expected the param-change proposal to pass, got %q", p.Status)
	}
	// The real effect is timelocked now - see
	// core.Proposal.ExecuteAfter's doc comment.
	processProposalExecution(p.ExecuteAfter)
	if got := core.DefaultTrustWeights.DisputeBps; got != 2500 {
		t.Fatalf("expected core.DefaultTrustWeights.DisputeBps updated to 2500 by the passed proposal, got %d", got)
	}
}

// TestRestoreTrustWeightsFromGovernanceParams_SurvivesRestart is the
// persistence regression test: a governance-changed TSI weight must
// still be in effect after a simulated restart (governanceParamsStore
// restored from its own persisted snapshot, then
// restoreTrustWeightsFromGovernanceParams re-syncing the core-package
// mirror), not silently revert to the compiled-in default the way it
// would if core.DefaultTrustWeights had no restore step at all.
func TestRestoreTrustWeightsFromGovernanceParams_SurvivesRestart(t *testing.T) {
	withRestoredDefaultTrustWeights(t)

	// Simulate a governance change having already happened and been
	// persisted (SaveState's snapshot round-trips governanceParamsStore
	// via Restore/RestoreSetAt before restoreTrustWeightsFromGovernanceParams
	// runs, exactly as LoadState does).
	governanceParamsStore = core.NewGovernanceParamsStore()
	governanceParamsStore.Seed("TSIWeightRepaymentBps", core.DefaultTrustWeights.RepaymentBps)
	governanceParamsStore.SetAt("TSIWeightTradingBps", 1000, 1000)

	// Simulate a fresh process: core.DefaultTrustWeights back at its
	// compiled-in default before LoadState has restored anything.
	core.DefaultTrustWeights = core.TrustWeights{
		RepaymentBps: 3000, TradingBps: 2200, GovernanceBps: 1800,
		ContributionBps: 1400, VerificationBps: 800, DisputeBps: 800,
	}

	restoreTrustWeightsFromGovernanceParams()

	if got := core.DefaultTrustWeights.TradingBps; got != 1000 {
		t.Fatalf("expected the governance-changed TradingBps (1000) to survive the simulated restart, got %d (would have silently reverted to the compiled-in default)", got)
	}
	// A key that was never governance-changed in this simulated restart
	// stays at whatever governanceParamsStore has for it (its own seeded
	// value here), not some unrelated stale value.
	if got := core.DefaultTrustWeights.RepaymentBps; got != 3000 {
		t.Fatalf("expected untouched RepaymentBps to stay at 3000, got %d", got)
	}
}
