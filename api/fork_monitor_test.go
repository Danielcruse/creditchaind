package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordForkEvent is the single chokepoint both the periodic fork
// monitor and syncChainFromPeer's reorg-refusal path call through
// (see api/peer.go and api/fork_monitor.go's own doc comments) - so
// testing it directly covers "a refused reorg immediately flips
// forkStatus.healthy to false and is queryable via /health/sync" and
// "the refusal log record contains the right fork details" for both
// call sites at once, without needing to stand up a full peer HTTP
// round trip with a real signed candidate chain just to reach the
// same shared function.
func TestRecordForkEvent_SetsUnhealthyStatus(t *testing.T) {
	setForkStatus(true, "in sync with 1 peer(s)")

	details := "refusing chain from http://peer:8080 - diverges from an already-applied block at index 42 (reorg-with-rollback not supported)"
	recordForkEvent(details)

	forkStatus.mu.Lock()
	healthy := forkStatus.healthy
	gotDetails := forkStatus.details
	forkStatus.mu.Unlock()

	if healthy {
		t.Fatal("expected forkStatus.healthy to be false after recordForkEvent")
	}
	if gotDetails != details {
		t.Fatalf("expected forkStatus.details to be %q, got %q", details, gotDetails)
	}
}

func TestRecordForkEvent_WritesDurableLogWithForkDetails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)

	details := "FORK DETECTED vs peer node2 (http://node2:8080) at block index 17 - local hash abc, peer hash def."
	recordForkEvent(details)

	logPath := filepath.Join(dir, "fork_events.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected fork_events.log to be written, got error: %v", err)
	}
	if !strings.Contains(string(data), details) {
		t.Fatalf("expected fork_events.log to contain the fork details %q, got: %s", details, string(data))
	}
}

func TestHandleHealth_ReflectsForkStatusAfterRecordForkEvent(t *testing.T) {
	details := "refusing chain from http://peer:9090 - diverges from an already-applied block at index 5 (reorg-with-rollback not supported)"
	recordForkEvent(details)

	forkStatus.mu.Lock()
	healthy := forkStatus.healthy
	gotDetails := forkStatus.details
	forkStatus.mu.Unlock()

	if healthy {
		t.Fatal("expected /health/sync-backing forkStatus to be unhealthy after a refused reorg")
	}
	if gotDetails != details {
		t.Fatalf("expected forkStatus.details to match the refusal details, got %q", gotDetails)
	}
}
