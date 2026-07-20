package api

import "sync"

// missedBlockReporters accumulates, per missed-block event, the set
// of distinct reporting nodes that have independently corroborated
// it - closing the single-reporter-trust gap disclosed in
// audit/KNOWN_GAPS.md: unlike a double-sign report (which carries a
// cryptographic fraud proof independently re-verified from the
// embedded evidence at apply time, see verifyDoubleSignEvidence), a
// missed block is an absence - nothing can cryptographically prove a
// validator DIDN'T produce, so a single node's unverified claim was,
// until this, enough to slash reputation/stake network-wide. This
// requires real corroboration from multiple independently-running
// nodes before the actual penalty (core.ApplyMissedBlock/
// RecordMiss/RecordSlash/slashStake/banIfSlashThresholdCrossed, in
// applySlashReport) ever fires - see requiredMissedBlockConfirmations
// for the M-of-N threshold.
var missedBlockReporters = struct {
	mu        sync.Mutex
	reporters map[string]map[string]bool // dedupKey -> set of reporter node addresses
}{reporters: make(map[string]map[string]bool)}

// recordMissedBlockReporter adds reporter to dedupKey's corroborating
// set (a no-op if this exact reporter already corroborated this
// exact event - a node only ever submits one report per miss it
// personally detects, but gossip/resubmission could otherwise inflate
// the count) and returns the resulting corroboration count.
func recordMissedBlockReporter(dedupKey, reporter string) int {
	missedBlockReporters.mu.Lock()
	defer missedBlockReporters.mu.Unlock()

	set, ok := missedBlockReporters.reporters[dedupKey]
	if !ok {
		set = make(map[string]bool)
		missedBlockReporters.reporters[dedupKey] = set
	}
	set[reporter] = true
	return len(set)
}

// clearMissedBlockReporters drops a resolved event's corroboration
// set once its penalty has actually been applied, keeping this map
// bounded to only currently-unresolved events instead of growing
// forever across the network's lifetime.
func clearMissedBlockReporters(dedupKey string) {
	missedBlockReporters.mu.Lock()
	defer missedBlockReporters.mu.Unlock()
	delete(missedBlockReporters.reporters, dedupKey)
}

// requiredMissedBlockConfirmations is the M in "M-of-N": a strict
// majority (more than half) of n, floored at 1. The floor is a
// deliberate, disclosed degradation, not a bypass - a genuinely
// single-node network has no one else to corroborate with, and
// refusing to ever slash a missed block in that case would defeat
// the mechanism entirely for a small/bootstrap deployment. It only
// ever requires just 1 confirmation when there is, verifiably, only
// 1 known node (see distinctActiveValidatorNodeCount) - as the
// network grows, so does the bar.
func requiredMissedBlockConfirmations(n int) int {
	if n <= 1 {
		return 1
	}
	m := n/2 + 1
	if m > n {
		m = n
	}
	return m
}

// distinctActiveValidatorNodeCount is this codebase's only available
// deterministic proxy for "how many independently-operated nodes
// exist in this network" - deterministic because it's derived purely
// from already-applied, consensus-agreed validatorStore state (every
// node computes the identical value from the same transaction
// history), which matters here since the M-of-N threshold itself
// must never depend on any one node's own local, non-replicated view
// (e.g. its live peer count) or different nodes could disagree on
// whether a given event has crossed the threshold.
//
// Disclosed limitation: a node with no validator currently delegated
// to it is invisible to this count even though it may also be
// running the consensus loop and independently reporting misses - so
// this undercounts the true number of watching nodes rather than
// overcounts, which is the safer direction for a threshold (harder to
// reach, not easier to game). A real node registry independent of
// validator delegation would be a separate, larger feature - see
// audit/KNOWN_GAPS.md.
func distinctActiveValidatorNodeCount() int {
	nodes := make(map[string]bool)
	for _, status := range validatorStore.List() {
		if status.Active && status.AuthorizedNodeID != "" {
			nodes[status.AuthorizedNodeID] = true
		}
	}
	if len(nodes) == 0 {
		return 1
	}
	return len(nodes)
}

// missedBlockReporterSnapshot/snapshotMissedBlockReporters/
// restoreMissedBlockReporters persist in-flight (not-yet-resolved)
// corroboration state across a restart - same rationale as
// snapshotSlashDedup/restoreSlashDedup in apply.go. Without this, a
// node that restarts after already reporting a miss (its own local
// consensus-loop cycle only reports once per detected miss and
// won't automatically re-report after coming back up) would silently
// drop its own corroboration vote, which could stall an event just
// below threshold indefinitely if enough nodes restart in sequence.
type missedBlockReporterSnapshot struct {
	Key       string   `json:"key"`
	Reporters []string `json:"reporters"`
}

func snapshotMissedBlockReporters() []missedBlockReporterSnapshot {
	missedBlockReporters.mu.Lock()
	defer missedBlockReporters.mu.Unlock()

	out := make([]missedBlockReporterSnapshot, 0, len(missedBlockReporters.reporters))
	for key, set := range missedBlockReporters.reporters {
		reporters := make([]string, 0, len(set))
		for r := range set {
			reporters = append(reporters, r)
		}
		out = append(out, missedBlockReporterSnapshot{Key: key, Reporters: reporters})
	}
	return out
}

func restoreMissedBlockReporters(snaps []missedBlockReporterSnapshot) {
	missedBlockReporters.mu.Lock()
	defer missedBlockReporters.mu.Unlock()

	for _, snap := range snaps {
		set := make(map[string]bool, len(snap.Reporters))
		for _, r := range snap.Reporters {
			set[r] = true
		}
		missedBlockReporters.reporters[snap.Key] = set
	}
}

// resetMissedBlockReportersForTest clears all in-flight corroboration
// state - test-only, mirrors this file's other package-level maps
// needing a reset hook for test isolation (see resetApplyTestState).
func resetMissedBlockReportersForTest() {
	missedBlockReporters.mu.Lock()
	defer missedBlockReporters.mu.Unlock()
	missedBlockReporters.reporters = make(map[string]map[string]bool)
}
