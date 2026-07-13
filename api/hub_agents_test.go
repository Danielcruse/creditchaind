package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestAssignAgentForHub_RotatesAcrossActiveAgentsOnly verifies the
// "zoning incoming requests to active agent[s]" behavior: only active
// workers are ever assigned, and an inactive one is skipped entirely
// even though it's still a known worker of the hub.
func TestAssignAgentForHub_RotatesAcrossActiveAgentsOnly(t *testing.T) {
	const hub = "hub-agents-test-hub"
	const agentA = "hub-agents-test-agent-a"
	const agentB = "hub-agents-test-agent-b"
	const agentInactive = "hub-agents-test-agent-inactive"

	hubWorkerStore.Add(&core.HubWorker{HubAddress: hub, WorkerAddress: agentA, Active: true})
	hubWorkerStore.Add(&core.HubWorker{HubAddress: hub, WorkerAddress: agentB, Active: true})
	hubWorkerStore.Add(&core.HubWorker{HubAddress: hub, WorkerAddress: agentInactive, Active: false})

	seen := make(map[string]bool)
	for i := 0; i < 10; i++ {
		agent := assignAgentForHub(hub)
		if agent == "" {
			t.Fatalf("expected a non-empty assignment while active agents exist")
		}
		if agent == agentInactive {
			t.Fatalf("expected the inactive agent to never be assigned, got it")
		}
		seen[agent] = true
		// Each call reads notificationStore.ListFor(hub) as the rotation
		// index - advance it exactly like a real trade request would so
		// successive calls actually rotate.
		notificationStore.Add(&core.HubNotification{ID: "test-notif-" + hub + string(rune('a'+i)), HubAddress: hub})
	}

	if len(seen) != 2 {
		t.Fatalf("expected both active agents to be assigned at least once across 10 rotations, got %d distinct agents: %v", len(seen), seen)
	}
}

// TestAssignAgentForHub_NoActiveAgentsReturnsEmpty confirms a hub with
// no active workers falls back to "" (unassigned - the hub itself
// handles it, same as before this feature existed) rather than
// panicking or picking an inactive one.
func TestAssignAgentForHub_NoActiveAgentsReturnsEmpty(t *testing.T) {
	const hub = "hub-agents-test-hub-no-active"

	hubWorkerStore.Add(&core.HubWorker{HubAddress: hub, WorkerAddress: "someone", Active: false})

	if got := assignAgentForHub(hub); got != "" {
		t.Fatalf("expected empty assignment for a hub with no active agents, got %q", got)
	}
}

// TestHubAgentApplicationStore_DecideIsExactlyOnce verifies an
// application can't be approved/rejected twice, and that
// HasPendingApplication correctly reflects PENDING-only status.
func TestHubAgentApplicationStore_DecideIsExactlyOnce(t *testing.T) {
	const hub = "hub-agents-test-decide-hub"
	const applicant = "hub-agents-test-decide-applicant"

	app := &core.HubAgentApplication{
		ID:               "test-app-decide-once",
		HubAddress:       hub,
		ApplicantAddress: applicant,
		Status:           core.AgentApplicationPending,
		CreatedAt:        1000,
	}
	hubAgentApplicationStore.Add(app)

	if !hubAgentApplicationStore.HasPendingApplication(hub, applicant) {
		t.Fatalf("expected a pending application to be reported as pending")
	}

	if _, ok := hubAgentApplicationStore.Decide(app.ID, true, 2000); !ok {
		t.Fatalf("expected the first Decide call to succeed")
	}
	if app.Status != core.AgentApplicationApproved {
		t.Fatalf("expected status APPROVED, got %s", app.Status)
	}

	if hubAgentApplicationStore.HasPendingApplication(hub, applicant) {
		t.Fatalf("expected a decided application to no longer count as pending")
	}

	if _, ok := hubAgentApplicationStore.Decide(app.ID, false, 3000); ok {
		t.Fatalf("expected a second Decide call on an already-decided application to fail")
	}
	if app.Status != core.AgentApplicationApproved {
		t.Fatalf("expected status to remain APPROVED after a rejected re-decide attempt, got %s", app.Status)
	}
}
