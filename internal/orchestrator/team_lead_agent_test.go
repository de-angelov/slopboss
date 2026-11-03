package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
)

func TestCoordinationSnapshotTracksMeaningfulChanges(t *testing.T) {
	old := config.DevAgentCount
	config.DevAgentCount = 2
	t.Cleanup(func() { config.DevAgentCount = old })
	tasks := []board.Task{{ID: "A", Title: "Example", Section: "Backlog", Status: "Backlog", Body: "first"}}
	now := time.Now()
	state := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{{Intent: ControllerTaskIntent{Role: devAgent1Role, TaskID: "B", Branch: "b"}, LeaseID: "b", HeartbeatAt: now, Attempt: 1}}}
	first := coordinationAction(tasks, map[string]bool{}, state)
	state.Leases[0].HeartbeatAt = now.Add(time.Minute)
	if got := coordinationAction(tasks, map[string]bool{}, state); got.TaskKey != first.TaskKey {
		t.Fatal("heartbeat created an unnecessary wakeup")
	}
	state.Leases[0].Terminal = &ControllerTerminalResult{Outcome: string(sessionCompleted), FinishedAt: now.Add(time.Minute), Evidence: "completed"}
	completed := coordinationAction(tasks, map[string]bool{}, state)
	if completed.TaskKey == first.TaskKey {
		t.Fatal("worker result did not wake Team Lead")
	}
	tasks[0].Body = "revised"
	if got := coordinationAction(tasks, map[string]bool{}, state); got.TaskKey == completed.TaskKey {
		t.Fatal("task body edit did not wake Team Lead")
	}
}

func TestCoordinationPersistsNoOpAndWakesOnChange(t *testing.T) {
	withRetryTestState(t)
	oldStore, oldLaunch, oldWorkspace := controllerStateStore, launchControllerSession, controllerWorkspaceExists
	oldRunning, oldFinished, oldPending := running, finished, cancelPendingSince
	t.Cleanup(func() {
		controllerStateStore, launchControllerSession, controllerWorkspaceExists = oldStore, oldLaunch, oldWorkspace
		running, finished, cancelPendingSince = oldRunning, oldFinished, oldPending
	})
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	running, finished, cancelPendingSince = map[string]RunningSession{}, map[string]FinishedSession{}, map[string]time.Time{}
	controllerWorkspaceExists = func(role string) bool { return role == teamLeadRole }
	launches := 0
	var lastID, lastKey string
	launchControllerSession = func(role string, task board.Task, key string, _ []board.Task, id string) {
		if role == teamLeadRole {
			launches++
			lastID, lastKey = id, key
		}
	}
	tasks := []board.Task{{ID: "A", Title: "Example", Section: "Backlog", Status: "Backlog", Body: "first"}}
	reconcileWithMode(tasks, "agent")
	if launches != 1 {
		t.Fatalf("initial launches = %d", launches)
	}
	if err := reportSessionTerminal(lastID, sessionCompleted, teamLeadRole, lastKey); err != nil {
		t.Fatal(err)
	}
	// Reconstruct from disk with no in-memory finished sessions, as after restart.
	finished = map[string]FinishedSession{}
	reconcileWithMode(tasks, "agent")
	if launches != 1 {
		t.Fatalf("unchanged snapshot relaunched after restart: %d", launches)
	}
	tasks[0].Body = "changed"
	reconcileWithMode(tasks, "agent")
	if launches != 2 {
		t.Fatalf("changed board did not wake Team Lead: %d", launches)
	}
}

func TestCoordinationDoesNotCancelWhenSnapshotChanges(t *testing.T) {
	withRetryTestState(t)
	oldStore, oldRunning, oldFinished, oldPending := controllerStateStore, running, finished, cancelPendingSince
	t.Cleanup(func() {
		controllerStateStore, running, finished, cancelPendingSince = oldStore, oldRunning, oldFinished, oldPending
	})
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	cancelled := false
	oldTask := coordinationAction(nil, map[string]bool{}, ControllerState{})
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: oldTask.TaskKey, Role: teamLeadRole, Branch: controllerBranch(oldTask.Task, teamLeadRole)}, LeaseID: controllerLeaseID(teamLeadRole, oldTask.Task, oldTask.TaskKey), HeartbeatAt: time.Now(), Attempt: 1}
	if err := controllerStateStore.Commit(ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}); err != nil {
		t.Fatal(err)
	}
	running = map[string]RunningSession{teamLeadRole: {Role: teamLeadRole, TaskKey: oldTask.TaskKey, Cancel: func() { cancelled = true }}}
	finished = map[string]FinishedSession{}
	cancelPendingSince = map[string]time.Time{teamLeadRole: time.Now().Add(-2 * config.CancelGracePeriod)}
	reconcileWithMode([]board.Task{{ID: "NEW", Title: "New work", Section: "Backlog"}}, "agent")
	if cancelled {
		t.Fatal("snapshot change cancelled an active coordinator")
	}
}

func TestPlannerDoesNotCancelActiveTeamLeadWhenBoardChanges(t *testing.T) {
	withRetryTestState(t)
	oldStore, oldRunning, oldFinished, oldPending := controllerStateStore, running, finished, cancelPendingSince
	t.Cleanup(func() {
		controllerStateStore, running, finished, cancelPendingSince = oldStore, oldRunning, oldFinished, oldPending
	})
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	task := board.Task{ID: "OLD", Title: "Old work", Key: "old"}
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: task.ID, Role: teamLeadRole, Branch: controllerBranch(task, teamLeadRole)}, LeaseID: controllerLeaseID(teamLeadRole, task, task.Key), HeartbeatAt: time.Now(), Attempt: 1}
	if err := controllerStateStore.Commit(ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}); err != nil {
		t.Fatal(err)
	}
	cancelled := false
	running = map[string]RunningSession{teamLeadRole: {Role: teamLeadRole, TaskKey: task.Key, Cancel: func() { cancelled = true }}}
	finished = map[string]FinishedSession{}
	cancelPendingSince = map[string]time.Time{teamLeadRole: time.Now().Add(-2 * config.CancelGracePeriod)}
	reconcileWithMode([]board.Task{{ID: "NEW", Title: "New work", Section: "Backlog", Status: "Backlog"}}, "planner")
	if cancelled {
		t.Fatal("board change cancelled an active planner Team Lead")
	}
}

func TestCoordinationUnresolvedLeaseDoesNotRelaunchAfterRestart(t *testing.T) {
	withRetryTestState(t)
	oldStore, oldRunning, oldFinished, oldPending, oldLaunch, oldWorkspace := controllerStateStore, running, finished, cancelPendingSince, launchControllerSession, controllerWorkspaceExists
	t.Cleanup(func() {
		controllerStateStore, running, finished, cancelPendingSince, launchControllerSession, controllerWorkspaceExists = oldStore, oldRunning, oldFinished, oldPending, oldLaunch, oldWorkspace
	})
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	running, finished, cancelPendingSince = map[string]RunningSession{}, map[string]FinishedSession{}, map[string]time.Time{}
	action := coordinationAction(nil, board.CompletedSet(), ControllerState{})
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: action.TaskKey, Role: teamLeadRole, Branch: controllerBranch(action.Task, teamLeadRole)}, LeaseID: controllerLeaseID(teamLeadRole, action.Task, action.TaskKey), HeartbeatAt: time.Now(), Attempt: 1}
	if err := controllerStateStore.Commit(ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}); err != nil {
		t.Fatal(err)
	}
	launches := 0
	controllerWorkspaceExists = func(string) bool { return true }
	launchControllerSession = func(string, board.Task, string, []board.Task, string) { launches++ }
	reconcileWithMode(nil, "agent")
	if launches != 0 {
		t.Fatalf("unresolved lease relaunched %d times", launches)
	}
	if Snapshot().RecoveryByRole[teamLeadRole] == "" {
		t.Fatal("unresolved lease was hidden from dashboard")
	}
}

func TestCoordinationUsesExactControllerExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	action := coordinationAction(nil, nil, ControllerState{})
	if !strings.Contains(action.Task.Body, shellQuote(executable)+" --dir "+shellQuote(config.RepoRoot)) {
		t.Fatalf("coordination prompt omitted exact executable: %s", action.Task.Body)
	}
	if got := shellQuote("/tmp/a'b"); got != "'/tmp/a'\\''b'" {
		t.Fatalf("shellQuote = %q", got)
	}
}
