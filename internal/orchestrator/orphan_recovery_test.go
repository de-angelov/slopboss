package orchestrator

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
)

func TestReconcilePrioritizesReadyOwnerOfBlockedChain(t *testing.T) {
	withRetryTestState(t)
	oldStore, oldLaunch, oldWorkspace, oldCount := controllerStateStore, launchControllerSession, controllerWorkspaceExists, config.DevAgentCount
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	config.DevAgentCount = 2
	controllerWorkspaceExists = func(role string) bool { return role == teamLeadRole }
	var launched []board.Task
	launchControllerSession = func(_ string, task board.Task, _ string, _ []board.Task, _ string) { launched = append(launched, task) }
	mu.Lock()
	running = map[string]RunningSession{}
	finished = map[string]FinishedSession{}
	cancelPendingSince = map[string]time.Time{}
	lastTeamLeadActivity = time.Now()
	mu.Unlock()
	t.Cleanup(func() {
		controllerStateStore = oldStore
		launchControllerSession = oldLaunch
		controllerWorkspaceExists = oldWorkspace
		config.DevAgentCount = oldCount
	})
	tasks := []board.Task{
		{ID: "DOG-04", Section: "Blocked Tasks", Status: "Blocked", Category: "AFK", Dependencies: "DOG-15"},
		{ID: "DOG-15", Section: "Blocked Tasks", Status: "Blocked", Category: "AFK", Dependencies: "DOG-16"},
		{ID: "DOG-16", Section: "Backlog", Status: "Backlog", Category: "AFK", Title: "repair archive", Key: "DOG-16"},
	}
	reconcile(tasks)
	reconcile(tasks)
	if len(launched) != 1 || launched[0].ID != "DOG-16" {
		t.Fatalf("launches=%#v, want one Team Lead pass on DOG-16", launched)
	}
}

func TestReconcileAllowsReadyWorkAfterNoProgressDecomposition(t *testing.T) {
	withRetryTestState(t)
	oldStore, oldLaunch, oldWorkspace, oldCount := controllerStateStore, launchControllerSession, controllerWorkspaceExists, config.DevAgentCount
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	config.DevAgentCount = 2
	controllerWorkspaceExists = func(role string) bool { return role == teamLeadRole }
	var launched []board.Task
	launchControllerSession = func(_ string, task board.Task, _ string, _ []board.Task, _ string) { launched = append(launched, task) }
	mu.Lock()
	running = map[string]RunningSession{}
	finished = map[string]FinishedSession{}
	cancelPendingSince = map[string]time.Time{}
	lastTeamLeadActivity = time.Now()
	mu.Unlock()
	t.Cleanup(func() {
		controllerStateStore = oldStore
		launchControllerSession = oldLaunch
		controllerWorkspaceExists = oldWorkspace
		config.DevAgentCount = oldCount
	})
	tasks := []board.Task{
		{ID: "ORPHAN", Section: "Blocked Tasks", Status: "Blocked", Category: "AFK", Title: "unowned", Dependencies: "MISSING"},
		{ID: "READY", Section: "Backlog", Status: "Backlog", Category: "AFK", Title: "ready", Key: "READY"},
	}
	reconcile(tasks)
	if len(launched) != 1 || launched[0].Section != teamLeadDecomposeSection {
		t.Fatalf("first launch=%#v, want decomposition", launched)
	}
	leaseID := controllerLeaseID(teamLeadRole, launched[0], launched[0].Key)
	if err := reportSessionTerminal(leaseID, sessionFailed, teamLeadRole, launched[0].Key); err != nil {
		t.Fatal(err)
	}
	reconcile(tasks)
	reconcile(tasks)
	if len(launched) != 2 || launched[1].ID != "READY" {
		t.Fatalf("launches=%#v, want one decomposition followed by ready work", launched)
	}
}
