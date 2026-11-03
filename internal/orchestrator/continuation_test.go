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

func TestCompletedWorkerCanContinueExactlyOnce(t *testing.T) {
	withRetryTestState(t)
	oldRoot, oldCount := config.RepoRoot, config.DevAgentCount
	config.SetRoot(t.TempDir())
	config.DevAgentCount = 1
	t.Cleanup(func() { config.SetRoot(oldRoot); config.DevAgentCount = oldCount })
	for path, data := range map[string]string{config.BacklogFile: "# BACKLOG\n\n## Backlog\n", config.TasksFile: "# TASKS\n\n## Dev Agent 1 In Progress\n\n### Fix\nTask ID: A\nCategory: AFK\nOwner: Dev Agent 1\nBranch: agent/1/a\nStatus: In Progress\n\nScope: work\n", config.ArchiveFile: "# ARCHIVE\n\n## Done\n"} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldStore, oldLaunch, oldWorkspace := controllerStateStore, launchControllerSession, controllerWorkspaceExists
	oldRunning, oldFinished, oldPending := running, finished, cancelPendingSince
	t.Cleanup(func() {
		controllerStateStore, launchControllerSession, controllerWorkspaceExists = oldStore, oldLaunch, oldWorkspace
		running, finished, cancelPendingSince = oldRunning, oldFinished, oldPending
	})
	controllerStateStore = NewControllerStateStore(filepath.Join(config.WorkspacesRoot, "controller-state.json"))
	running, finished, cancelPendingSince = map[string]RunningSession{}, map[string]FinishedSession{}, map[string]time.Time{}
	tasks, err := board.ReadBoardTasks()
	if err != nil {
		t.Fatal(err)
	}
	task := tasks[0]
	now := time.Now()
	oldID := controllerLeaseID(devAgent1Role, task, task.Key)
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: task.ID, Role: devAgent1Role, Branch: task.Branch}, LeaseID: oldID, HeartbeatAt: now, Attempt: 1, Terminal: &ControllerTerminalResult{Outcome: string(sessionCompleted), FinishedAt: now, Evidence: "worker exited"}}
	if err := controllerStateStore.Commit(ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}); err != nil {
		t.Fatal(err)
	}
	launches := 0
	var newID, newKey string
	controllerWorkspaceExists = func(role string) bool { return role == devAgent1Role }
	launchControllerSession = func(role string, task board.Task, key string, _ []board.Task, id string) {
		launches++
		newID, newKey = id, key
		if !strings.Contains(task.Body, "rebase onto main") {
			t.Error("handoff instructions absent from worker task")
		}
	}
	reconcileWithMode(tasks, "agent")
	if launches != 0 {
		t.Fatal("completed worker relaunched without a request")
	}
	if _, err := board.RequestContinuation(task.ID, task.Owner, task.Branch, oldID, "rebase onto main"); err != nil {
		t.Fatal(err)
	}
	tasks, err = board.ReadBoardTasks()
	if err != nil {
		t.Fatal(err)
	}
	reconcileWithMode(tasks, "agent")
	if launches != 1 || newID == oldID {
		t.Fatalf("continuation launches=%d id=%s", launches, newID)
	}
	if err := reportSessionTerminal(newID, sessionCompleted, devAgent1Role, newKey); err != nil {
		t.Fatal(err)
	}
	finished = map[string]FinishedSession{}
	reconcileWithMode(tasks, "agent")
	if launches != 1 {
		t.Fatalf("same request repeated after completion: %d", launches)
	}
}

func TestContinuationRequiresMatchingTerminalEvidence(t *testing.T) {
	task := board.Task{ID: "A", Branch: "branch", Key: "key", ContinueAfter: strings.Repeat("a", 64)}
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: "A", Role: devAgent1Role, Branch: "branch"}, LeaseID: task.ContinueAfter, Attempt: 1, HeartbeatAt: time.Now()}
	state := ControllerState{Leases: []ControllerWorkerLease{lease}}
	if got := continuationTaskKey(task, devAgent1Role, state); got != task.Key {
		t.Fatal("accepted unresolved worker")
	}
	lease.Terminal = &ControllerTerminalResult{Outcome: string(sessionCompleted), FinishedAt: time.Now(), Evidence: "done"}
	lease.Intent.TaskID = "other"
	state.Leases[0] = lease
	if got := continuationTaskKey(task, devAgent1Role, state); got != task.Key {
		t.Fatal("accepted evidence for different task")
	}
}

func TestCompletionFollowUpTaskInstructsResume(t *testing.T) {
	mu.Lock()
	oldFinished := finished
	finished = map[string]FinishedSession{
		devAgent1Role: {
			Outcome: sessionCompleted,
			Task:    "A",
			TaskKey: "key-a",
		},
	}
	mu.Unlock()
	defer func() {
		mu.Lock()
		finished = oldFinished
		mu.Unlock()
	}()

	task, ok := completionFollowUpTask(ControllerState{})
	if !ok {
		t.Fatal("expected completion follow up task")
	}
	if !strings.Contains(task.Body, "board resume TASK-ID") {
		t.Errorf("expected follow up body to instruct board resume, got:\n%s", task.Body)
	}
}
