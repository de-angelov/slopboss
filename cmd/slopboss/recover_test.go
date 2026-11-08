package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/orchestrator"
)

func TestRecoverRetrySucceedsWhileControllerLockHeld(t *testing.T) {
	old := config.WorkspacesRoot
	config.WorkspacesRoot = t.TempDir()
	t.Cleanup(func() { config.WorkspacesRoot = old })
	store := orchestrator.NewControllerStateStore(filepath.Join(config.WorkspacesRoot, "controller-state.json"))
	lease := orchestrator.ControllerWorkerLease{Intent: orchestrator.ControllerTaskIntent{TaskID: "TASK-1", Role: "Dev Agent 1", Branch: "agent/1/task-1"}, LeaseID: "lease-1", Attempt: 1, HeartbeatAt: time.Now(), Terminal: &orchestrator.ControllerTerminalResult{Outcome: "failed", FinishedAt: time.Now(), Evidence: "failed"}}
	if err := store.Commit(orchestrator.ControllerState{Version: orchestrator.ControllerStateVersion, Leases: []orchestrator.ControllerWorkerLease{lease}}); err != nil {
		t.Fatal(err)
	}

	// Acquire the same OS lock used by the active daemon.
	lock, err := os.OpenFile(filepath.Join(config.WorkspacesRoot, "controller.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	recoverRetry, recoverConfirmed, recoverReason = true, false, ""
	t.Cleanup(func() { recoverRetry, recoverConfirmed, recoverReason = false, false, "" })
	if err := recoverCmd.RunE(recoverCmd, []string{"TASK-1"}); err != nil {
		t.Fatalf("recover --retry while controller is running: %v", err)
	}
}
