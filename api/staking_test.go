package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleStaking_ReportsDelegatedOutAndAvailableStake confirms the
// staking endpoint surfaces both the raw delegated-out figure and the
// derived available-stake figure a wallet's own display needs to show
// "you've delegated X, leaving Y available" rather than just a flat
// staked total that doesn't account for delegation at all.
func TestHandleStaking_ReportsDelegatedOutAndAvailableStake(t *testing.T) {
	resetModeratorDelegationTestState()

	const address = "staking-display-addr"
	stakingStore.Stakes[address] = map[string]uint64{"CCT": 3020}
	delegationStore.Delegate("some-validator", address, 3000)

	req := httptest.NewRequest(http.MethodGet, "/staking?address="+address, nil)
	w := httptest.NewRecorder()
	HandleStaking(w, req)

	var resp StakingResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.DelegatedOutCCT != 3000 {
		t.Fatalf("expected delegatedOutCCT 3000, got %d", resp.DelegatedOutCCT)
	}
	if resp.AvailableStakedCCT != 20 {
		t.Fatalf("expected availableStakedCCT 20, got %d", resp.AvailableStakedCCT)
	}

	var cctPosition *StakePosition
	for i := range resp.Positions {
		if resp.Positions[i].Symbol == "CCT" {
			cctPosition = &resp.Positions[i]
		}
	}
	if cctPosition == nil || cctPosition.Staked != 3020 {
		t.Fatalf("expected the raw CCT position to stay at the full staked 3020 (unaffected by delegation), got %+v", cctPosition)
	}
}
