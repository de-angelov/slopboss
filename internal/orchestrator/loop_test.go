package orchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
)

type stoppedUI struct{}

func (stoppedUI) SetTasks([]board.Task) {}
func (stoppedUI) SetError(error)        {}
func (stoppedUI) Refresh()              {}
func (stoppedUI) Run() error            { return nil }
func (stoppedUI) Stop()                 {}

func TestRunLoopUsesSelectedBoardRecoveryState(t *testing.T) {
	oldStore, oldWorkspaces, oldLogs, oldLogFile := controllerStateStore, config.WorkspacesRoot, config.LogsRoot, config.LogFilePath
	oldCount, oldProvider := config.DevAgentCount, sessionProvider
	t.Cleanup(func() {
		controllerStateStore, config.WorkspacesRoot, config.LogsRoot, config.LogFilePath = oldStore, oldWorkspaces, oldLogs, oldLogFile
		config.DevAgentCount, sessionProvider = oldCount, oldProvider
	})
	config.WorkspacesRoot = filepath.Join(t.TempDir(), "workspaces")
	config.LogsRoot = filepath.Join(config.WorkspacesRoot, "logs")
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "wrong-board.json"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RunLoop(ctx, "codex", stoppedUI{}); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(config.WorkspacesRoot, "controller-state.json"); controllerStateStore.path != want {
		t.Fatalf("recovery state path = %q, want %q", controllerStateStore.path, want)
	}
}

type failedUI struct{ stopped chan struct{} }

func (u *failedUI) SetTasks([]board.Task) {}
func (u *failedUI) SetError(error)        {}
func (u *failedUI) Refresh()              {}
func (u *failedUI) Run() error            { return fmt.Errorf("screen failed to initialize") }
func (u *failedUI) Stop()                 { close(u.stopped) }

func TestRunLoopDoesNotStopUIAfterInitializationFailure(t *testing.T) {
	oldWorkspaces, oldLogs, oldLogFile, oldStore := config.WorkspacesRoot, config.LogsRoot, config.LogFilePath, controllerStateStore
	oldCount, oldProvider := config.DevAgentCount, sessionProvider
	t.Cleanup(func() {
		config.WorkspacesRoot, config.LogsRoot, config.LogFilePath, controllerStateStore = oldWorkspaces, oldLogs, oldLogFile, oldStore
		config.DevAgentCount, sessionProvider = oldCount, oldProvider
	})
	config.WorkspacesRoot = filepath.Join(t.TempDir(), "workspaces")
	config.LogsRoot = filepath.Join(config.WorkspacesRoot, "logs")
	screen := &failedUI{stopped: make(chan struct{})}
	if err := RunLoop(context.Background(), "codex", screen); err == nil {
		t.Fatal("expected screen error")
	}
	select {
	case <-screen.stopped:
		t.Fatal("Stop called after screen initialization failed")
	case <-time.After(100 * time.Millisecond):
	}
}
