package orchestrator

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
)

func TestControllerOwnershipAndRecovery(t *testing.T) {
	old := config.WorkspacesRoot
	config.WorkspacesRoot = t.TempDir()
	t.Cleanup(func() { config.WorkspacesRoot = old })
	store := NewControllerStateStore(filepath.Join(config.WorkspacesRoot, "controller-state.json"))
	now := time.Now()
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: "TASK", Role: "Dev Agent 1", Branch: "branch"}, LeaseID: "lease", HeartbeatAt: now, Attempt: 2}
	if err := store.Commit(ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockController()
	if err != nil {
		t.Fatal(err)
	}
	if err := RecoverStoppedRole(lease.Intent.Role); err == nil {
		t.Fatal("recovery succeeded while controller owns board")
	}
	unlock()
	if err := RecoverStoppedRole(lease.Intent.Role); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Leases[0].Terminal == nil || state.Leases[0].Terminal.Outcome != string(sessionCancelled) {
		t.Fatalf("lease = %#v", state.Leases[0])
	}
	if _, allowed, _ := recoveryLeaseDecision(state, lease.Intent.Role, lease.LeaseID); !allowed {
		t.Fatal("verified stopped lease cannot resume")
	}
}

func TestExhaustedDecompositionSurvivesAuditReplacement(t *testing.T) {
	withRetryTestState(t)
	oldFinished, oldActivity := finished, lastTeamLeadActivity
	t.Cleanup(func() { finished, lastTeamLeadActivity = oldFinished, oldActivity })
	finished = map[string]FinishedSession{}
	lastTeamLeadActivity = time.Now()
	tasks := []board.Task{{ID: "WAIT", Title: "Waiting", Section: "Backlog", Status: "Backlog", Dependencies: "MISSING"}}
	task := decompositionTaskForBoard([]string{"WAIT"}, tasks, map[string]bool{})
	id := controllerLeaseID(teamLeadRole, task, task.Key)
	exhausted := ControllerWorkerLease{Intent: ControllerTaskIntent{Role: teamLeadRole, TaskID: task.Key}, LeaseID: id, Attempt: config.MaxFailedSessionRetries + 1, Terminal: &ControllerTerminalResult{Outcome: string(sessionFailed)}}
	audit := idleAuditTask(time.Now())
	active := ControllerWorkerLease{Intent: ControllerTaskIntent{Role: teamLeadRole, TaskID: audit.Key}, LeaseID: "audit", Attempt: 1}
	state := ControllerState{Leases: []ControllerWorkerLease{active}, History: map[string]ControllerWorkerLease{id: exhausted}}
	plan, ok := planTeamLeadAction(tasks, map[string]bool{}, state, map[string]desiredSession{}, map[string]bool{})
	if !ok || plan.TaskKey != audit.Key {
		t.Fatalf("running audit displaced: %#v", plan)
	}
	active.Terminal = &ControllerTerminalResult{Outcome: string(sessionCompleted)}
	state.Leases[0] = active
	if plan, ok = planTeamLeadAction(tasks, map[string]bool{}, state, map[string]desiredSession{}, map[string]bool{}); ok {
		t.Fatalf("exhausted pass restarted: %#v", plan)
	}
	if _, allowed, reason := recoveryLeaseDecision(state, teamLeadRole, id); allowed || !strings.Contains(reason, "exhausted") {
		t.Fatalf("recovery allowed=%v reason=%q", allowed, reason)
	}
}

func TestUsageAndCancellationDoNotConsumeFailureAttempts(t *testing.T) {
	for _, outcome := range []SessionOutcome{sessionCancelled, sessionUsageLimited} {
		t.Run(string(outcome), func(t *testing.T) {
			now := time.Now()
			lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: "task", Role: devAgent1Role, Branch: "branch"}, LeaseID: "id", HeartbeatAt: now, Attempt: config.MaxFailedSessionRetries + 1, Terminal: &ControllerTerminalResult{Outcome: string(outcome), FinishedAt: now, Evidence: "stopped"}}
			state := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}
			if _, allowed, reason := recoveryLeaseDecision(state, devAgent1Role, lease.LeaseID); !allowed {
				t.Fatalf("cannot resume: %s", reason)
			}
			next := state
			next.Leases = append([]ControllerWorkerLease(nil), state.Leases...)
			next.Leases[0].Terminal = nil
			if err := ValidateControllerTransition(state, next); err != nil {
				t.Fatalf("resume rejected: %v", err)
			}
		})
	}
}

func TestPlannerKeepsRunningIdleAuditOnHealthyBoard(t *testing.T) {
	withRetryTestState(t)
	oldFinished, oldActivity := finished, lastTeamLeadActivity
	t.Cleanup(func() { finished, lastTeamLeadActivity = oldFinished, oldActivity })
	finished = map[string]FinishedSession{}
	lastTeamLeadActivity = time.Now()
	audit := idleAuditTask(time.Now())
	state := ControllerState{Leases: []ControllerWorkerLease{{Intent: ControllerTaskIntent{Role: teamLeadRole, TaskID: audit.Key}, LeaseID: "audit", Attempt: 1}}}
	plan, ok := planTeamLeadAction(nil, map[string]bool{}, state, map[string]desiredSession{}, map[string]bool{})
	if !ok || plan.TaskKey != audit.Key {
		t.Fatalf("running healthy-board audit displaced: %#v", plan)
	}
}

func TestApproveTerminalRetryByTaskIDAndLiveQueue(t *testing.T) {
	old := config.WorkspacesRoot
	config.WorkspacesRoot = t.TempDir()
	t.Cleanup(func() { config.WorkspacesRoot = old })

	store := NewControllerStateStore(filepath.Join(config.WorkspacesRoot, "controller-state.json"))
	now := time.Now()
	lease := ControllerWorkerLease{
		Intent:      ControllerTaskIntent{TaskID: "TASK-078", Role: "Dev Agent 2", Branch: "agent/2/task-078"},
		LeaseID:     "lease-078",
		HeartbeatAt: now,
		Attempt:     config.MaxFailedSessionRetries + 1,
		Terminal:    &ControllerTerminalResult{Outcome: string(sessionFailed), FinishedAt: now, Evidence: "quota exceeded"},
	}
	if err := store.Commit(ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}); err != nil {
		t.Fatal(err)
	}

	// 1. Test offline retry approval using Task ID
	if err := ApproveTerminalRetry("TASK-078", "lifted"); err != nil {
		t.Fatalf("ApproveTerminalRetry by task ID failed: %v", err)
	}

	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.RetryApprovals["lease-078"] != "lifted" {
		t.Fatalf("expected retry approval for lease-078, got: %v", state.RetryApprovals)
	}

	// 2. Test live retry approval when controller lock is held
	unlock, err := lockController()
	if err != nil {
		t.Fatal(err)
	}

	// While controller is locked, ApproveTerminalRetry should queue the request
	if err := ApproveTerminalRetry("TASK-078", "second retry"); err != nil {
		t.Fatalf("ApproveTerminalRetry while locked failed: %v", err)
	}

	// Simulate controller consuming pending requests in its reconcile tick
	consumePendingRetryRequestsLocked(store, &state)

	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.RetryApprovals["lease-078"] != "second retry" {
		t.Fatalf("expected updated live retry approval, got: %v", state.RetryApprovals)
	}

	unlock()
}

func TestIsQuotaOrRateLimit(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"429 Too Many Requests", true},
		{"insufficient_quota: you have exceeded your current quota", true},
		{"rate limit exceeded. Try again in 20s", true},
		{"RESOURCE_EXHAUSTED: quota reached", true},
		{"syntax error in main.go:42", false},
		{"exit status 1: test failed", false},
	}
	for _, tc := range cases {
		if got := isQuotaOrRateLimit(tc.msg); got != tc.want {
			t.Errorf("isQuotaOrRateLimit(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}
