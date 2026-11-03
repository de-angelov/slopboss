package orchestrator

import (
	"context"
	"path/filepath"
	"testing"

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
