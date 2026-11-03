package orchestrator

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/provider"
)

type shutdownProvider struct{}

func (shutdownProvider) Name() string               { return "shutdown-test" }
func (shutdownProvider) DefaultModel(string) string { return "" }
func (shutdownProvider) Command(ctx context.Context, _ string, _ int) *exec.Cmd {
	return exec.CommandContext(ctx, "sleep", "60")
}
func (shutdownProvider) InteractiveCommand(ctx context.Context, _, _ string) *exec.Cmd {
	return exec.CommandContext(ctx, "sleep", "60")
}
func (shutdownProvider) ExperimentCommand(ctx context.Context, _ provider.ExperimentSpec) *exec.Cmd {
	return exec.CommandContext(ctx, "sleep", "60")
}
func (shutdownProvider) NewMonitor() provider.Monitor { return shutdownMonitor{} }

type shutdownMonitor struct{}

func (shutdownMonitor) Write(p []byte) (int, error)        { return io.Discard.Write(p) }
func (shutdownMonitor) Breakdown() provider.TokenBreakdown { return provider.TokenBreakdown{} }
func (shutdownMonitor) UsageLimited() bool                 { return false }
func (shutdownMonitor) FinalMessage() string               { return "" }

func TestShutdownPersistsCancellationBeforeDrain(t *testing.T) {
	withRetryTestState(t)
	oldStore, oldProvider, oldRoot := controllerStateStore, sessionProvider, config.WorkspacesRoot
	root := t.TempDir()
	controllerStateStore = NewControllerStateStore(filepath.Join(root, "state.json"))
	sessionProvider = shutdownProvider{}
	config.WorkspacesRoot = root
	t.Cleanup(func() {
		controllerStateStore = oldStore
		sessionProvider = oldProvider
		config.WorkspacesRoot = oldRoot
	})
	if err := os.MkdirAll(config.DevAgentWorkspace(1), 0755); err != nil {
		t.Fatal(err)
	}
	task := board.Task{ID: "task", Title: "test", Key: "key"}
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: task.ID, Role: devAgent1Role, Branch: "branch"}, LeaseID: "lease", HeartbeatAt: time.Now(), Attempt: 1}
	if err := commitControllerState(ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	running = map[string]RunningSession{}
	mu.Unlock()
	startSession(devAgent1Role, task, task.Key, nil, lease.LeaseID)
	shutdownSessions()
	state, err := loadControllerState()
	if err != nil {
		t.Fatal(err)
	}
	result := state.Leases[0].Terminal
	if result == nil || result.Outcome != string(sessionCancelled) {
		t.Fatalf("shutdown terminal = %#v, want cancelled", result)
	}
	_, launch, _ := recoveryLeaseDecision(state, devAgent1Role, "other-lease")
	if !launch {
		t.Fatal("cancelled lease prevents subsequent task")
	}
}

func TestConcurrentTerminalReportsPreserveBothLeases(t *testing.T) {
	oldStore := controllerStateStore
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { controllerStateStore = oldStore })
	now := time.Now()
	state := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{
		{Intent: ControllerTaskIntent{TaskID: "one", Role: devAgent1Role, Branch: "one"}, LeaseID: "one", HeartbeatAt: now, Attempt: 1},
		{Intent: ControllerTaskIntent{TaskID: "two", Role: devAgent2Role, Branch: "two"}, LeaseID: "two", HeartbeatAt: now, Attempt: 1},
	}}
	if err := commitControllerState(state); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errors := make(chan error, 2)
	for _, entry := range []struct{ id, role string }{{"one", devAgent1Role}, {"two", devAgent2Role}} {
		go func(id, role string) { <-start; errors <- reportSessionTerminal(id, sessionCancelled, role, id) }(entry.id, entry.role)
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	saved, err := loadControllerState()
	if err != nil {
		t.Fatal(err)
	}
	for _, lease := range saved.Leases {
		if lease.Terminal == nil || lease.Terminal.Outcome != string(sessionCancelled) {
			t.Fatalf("terminal lost: %#v", lease)
		}
	}
}

func TestReconcileCommitsMultipleNewLeases(t *testing.T) {
	withRetryTestState(t)
	oldStore, oldLaunch, oldWorkspace, oldCount := controllerStateStore, launchControllerSession, controllerWorkspaceExists, config.DevAgentCount
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	config.DevAgentCount = 2
	controllerWorkspaceExists = func(role string) bool { return role == devAgent1Role || role == devAgent2Role }
	launches := map[string]int{}
	launchControllerSession = func(role string, _ board.Task, _ string, _ []board.Task, _ string) { launches[role]++ }
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
		{ID: "one", Key: "one", Title: "one", Section: config.DevAgentSection(1), Owner: devAgent1Role, Status: "In Progress", Branch: "one"},
		{ID: "two", Key: "two", Title: "two", Section: config.DevAgentSection(2), Owner: devAgent2Role, Status: "In Progress", Branch: "two"},
	}
	reconcile(tasks)
	reconcile(tasks)
	for _, role := range []string{devAgent1Role, devAgent2Role} {
		if launches[role] != 1 {
			t.Fatalf("%s launches = %d, want 1", role, launches[role])
		}
	}
	saved, err := loadControllerState()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Leases) != 2 {
		t.Fatalf("persisted leases = %#v", saved.Leases)
	}
}

func TestRunningSessionPersistsProcessIdentity(t *testing.T) {
	withRetryTestState(t)
	oldStore, oldProvider, oldRoot := controllerStateStore, sessionProvider, config.WorkspacesRoot
	config.WorkspacesRoot = t.TempDir()
	controllerStateStore = NewControllerStateStore(filepath.Join(config.WorkspacesRoot, "controller-state.json"))
	sessionProvider = shutdownProvider{}
	t.Cleanup(func() { controllerStateStore, sessionProvider, config.WorkspacesRoot = oldStore, oldProvider, oldRoot })
	if err := os.MkdirAll(config.DevAgentWorkspace(1), 0755); err != nil {
		t.Fatal(err)
	}
	task := board.Task{ID: "task", Title: "test", Key: "key"}
	now := time.Now()
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: task.ID, Role: devAgent1Role, Branch: "branch"}, LeaseID: "lease", HeartbeatAt: now, Attempt: 1}
	if err := commitControllerState(ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	running = map[string]RunningSession{}
	mu.Unlock()
	startSession(devAgent1Role, task, task.Key, nil, lease.LeaseID)
	defer shutdownSessions()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, err := loadControllerState()
		if err == nil && state.Leases[0].Process != nil && state.Leases[0].Process.PID > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("running worker process was not recorded")
}
