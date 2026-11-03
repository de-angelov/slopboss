package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/coordination"
)

func TestCoordinationResultDistinguishesIdleAndFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if outcome, evidence := classifyCoordinationReport(path); outcome != sessionBlocked || !strings.Contains(evidence, "missing") {
		t.Fatalf("missing report: %s %s", outcome, evidence)
	}
	for _, tc := range []struct {
		status string
		want   SessionOutcome
	}{{"idle", sessionCompleted}, {"acted", sessionCompleted}, {"blocked", sessionBlocked}} {
		if err := coordination.WriteReport(path, coordination.Report{Status: tc.status, Summary: "specific evidence"}); err != nil {
			t.Fatal(err)
		}
		outcome, evidence := classifyCoordinationReport(path)
		if outcome != tc.want || !strings.Contains(evidence, "specific evidence") {
			t.Fatalf("%s => %s %q", tc.status, outcome, evidence)
		}
	}
}

func TestBlockedCoordinationResultPersistsAndDoesNotRelaunch(t *testing.T) {
	old := controllerStateStore
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { controllerStateStore = old })
	now := time.Now()
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: "coordinate", Role: teamLeadRole, Branch: "synthetic/lead"}, LeaseID: "lease", HeartbeatAt: now, Attempt: 1}
	if err := controllerStateStore.Commit(ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}); err != nil {
		t.Fatal(err)
	}
	if err := reportSessionTerminal("lease", sessionBlocked, teamLeadRole, "coordinate", "coordination blocked: command failed"); err != nil {
		t.Fatal(err)
	}
	state, err := controllerStateStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.Leases[0].Terminal.Evidence, "command failed") {
		t.Fatal("failure evidence not durable")
	}
	if _, ok, reason := recoveryLeaseDecision(state, teamLeadRole, "lease"); ok || !strings.Contains(reason, "review") {
		t.Fatalf("blocked report allowed launch: %v %q", ok, reason)
	}
	data, err := os.ReadFile(controllerStateStore.path)
	if err != nil || !strings.Contains(string(data), "command failed") {
		t.Fatal("failure absent from persisted state")
	}
}

func TestBlockedResultNeedsOneUseRetryApproval(t *testing.T) {
	oldWorkspaces := config.WorkspacesRoot
	config.WorkspacesRoot = t.TempDir()
	t.Cleanup(func() { config.WorkspacesRoot = oldWorkspaces })
	oldStore := controllerStateStore
	controllerStateStore = NewControllerStateStore(filepath.Join(config.WorkspacesRoot, "controller-state.json"))
	t.Cleanup(func() { controllerStateStore = oldStore })
	now := time.Now()
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: "coordinate", Role: teamLeadRole, Branch: "synthetic/lead"}, LeaseID: "lease", HeartbeatAt: now, Attempt: 1, Terminal: &ControllerTerminalResult{Outcome: string(sessionBlocked), FinishedAt: now, Evidence: "action failed"}}
	if err := controllerStateStore.Commit(ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}); err != nil {
		t.Fatal(err)
	}
	state, _ := controllerStateStore.Load()
	if _, allowed, _ := recoveryLeaseDecision(state, teamLeadRole, "different"); allowed {
		t.Fatal("changed snapshot bypassed blocked action")
	}
	if err := ApproveTerminalRetry(teamLeadRole, "problem fixed"); err != nil {
		t.Fatal(err)
	}
	state, _ = controllerStateStore.Load()
	if _, allowed, _ := recoveryLeaseDecision(state, teamLeadRole, "lease"); !allowed {
		t.Fatal("approved retry denied")
	}
	next := lease
	next.Attempt++
	next.Terminal = nil
	next.HeartbeatAt = now
	if err := commitRecoveryLease(state, next, "coordinate"); err != nil {
		t.Fatal(err)
	}
	state, _ = controllerStateStore.Load()
	if len(state.RetryApprovals) != 0 {
		t.Fatal("retry approval was not consumed")
	}
	if len(state.RetryHistory) != 1 || state.RetryHistory[0].Reason != "problem fixed" {
		t.Fatal("retry rationale was not retained")
	}
	if state.History["lease"].Terminal.Evidence != "action failed" {
		t.Fatal("original evidence lost")
	}
	if err := reportSessionTerminal("lease", sessionCompleted, teamLeadRole, "coordinate", "coordination acted: resolved"); err != nil {
		t.Fatal(err)
	}
	state, _ = controllerStateStore.Load()
	if len(state.PastResults) != 1 || state.PastResults[0].Terminal.Evidence != "action failed" {
		t.Fatal("retry overwrote prior evidence")
	}
}
