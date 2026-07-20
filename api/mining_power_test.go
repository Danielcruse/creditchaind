package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiningPowerScore_WeightsAllSevenFactors(t *testing.T) {
	resetApplyTestState()
	const addr = "mining-power-test-wallet"

	// Max out every real factor and confirm the composite score lands
	// at (or very near) 100 - if any factor's weight were dropped from
	// the sum, this would come in noticeably short of 100.
	asOf := int64(1000)
	walletStore.Get(addr).Reputation = 100 // Trust Score factor
	walletStore.Get(addr).CreatedAt = asOf - int64(accountAgeNormalizationDays)*2*24*60*60

	creditEngine.Profiles.Get(addr).RecordRepayment(1) // 100% repayment, no defaults

	stakingStore.Stakes[addr] = map[string]uint64{"CCT": stakedCCTNormalizationCap}

	for i := 0; i < governanceParticipationCap; i++ {
		proposalStore.Voted["prop-"+string(rune('a'+i))] = map[string]bool{addr: true}
	}

	for i := uint64(0); i < successfulLendingNormalizationCount; i++ {
		creditEngine.Profiles.Get(addr).RecordFunding(1)
	}

	validatorStore.Register(addr, "node-1", "pubkey")

	score := miningPowerScore(addr, asOf)

	if score < 95 {
		t.Fatalf("expected a maxed-out wallet's Mining Power score near 100, got %d", score)
	}
	if score > 100 {
		t.Fatalf("expected Mining Power score capped at 100, got %d", score)
	}
}

func TestMiningPowerScore_ZeroForBrandNewWallet(t *testing.T) {
	resetApplyTestState()
	const addr = "mining-power-fresh-wallet"

	// A brand-new wallet auto-vivifies with Reputation 50 (see
	// core.WalletStore.Get) and no history anywhere - the score should
	// be well below max but not necessarily zero (repayment factor
	// defaults to a neutral 50 with no history).
	score := miningPowerScore(addr, 1000)

	if score == 0 {
		t.Fatalf("expected a nonzero score from Trust Score alone (Reputation defaults to 50), got 0")
	}
	if score > 50 {
		t.Fatalf("expected a fresh wallet's score well under the max, got %d", score)
	}
}

func TestHandleMiningPower_NetworkContributionsAlwaysZeroWeight(t *testing.T) {
	resetApplyTestState()
	const addr = "mining-power-http-test-wallet"
	walletStore.Get(addr).Reputation = 70

	req := httptest.NewRequest(http.MethodGet, "/mining-power?address="+addr, nil)
	rec := httptest.NewRecorder()

	HandleMiningPower(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp miningPowerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.NetworkContribWeight != 0 {
		t.Fatalf("expected NetworkContribWeight 0 (no real backing data), got %d", resp.NetworkContribWeight)
	}
	if resp.NetworkContribStatus != "not yet active" {
		t.Fatalf("expected NetworkContribStatus 'not yet active', got %q", resp.NetworkContribStatus)
	}
	if resp.TrustScoreFactor != 70 {
		t.Fatalf("expected TrustScoreFactor 70, got %d", resp.TrustScoreFactor)
	}
}
