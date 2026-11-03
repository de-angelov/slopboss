package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/de-angelov/slopboss/internal/config"
)

// lockController serializes controllers and manual recovery for one board.
func lockController() (func(), error) {
	if err := os.MkdirAll(config.WorkspacesRoot, 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(config.WorkspacesRoot, "controller.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another controller or recovery operation is using this board: %w", err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

// RecoverStoppedRole records operator-confirmed termination of an unresolved lease.
// The caller must verify that the worker and its descendants have stopped.
func RecoverStoppedRole(role string) error {
	unlock, err := lockController()
	if err != nil {
		return err
	}
	defer unlock()
	store := NewControllerStateStore(filepath.Join(config.WorkspacesRoot, "controller-state.json"))
	state, err := store.Load()
	if err != nil {
		return err
	}
	for i := range state.Leases {
		lease := &state.Leases[i]
		if lease.Intent.Role != role {
			continue
		}
		if lease.Terminal != nil {
			return fmt.Errorf("role %q already has a terminal result", role)
		}
		now := time.Now()
		lease.HeartbeatAt = now
		lease.Terminal = &ControllerTerminalResult{Outcome: string(sessionCancelled), FinishedAt: now, Evidence: "operator verified worker stopped"}
		if state.History == nil {
			state.History = make(map[string]ControllerWorkerLease)
		}
		state.History[lease.LeaseID] = *lease
		return store.Commit(state)
	}
	return fmt.Errorf("no lease for role %q", role)
}

// TerminalWorkerLease returns evidence for a finished worker assignment.
func TerminalWorkerLease(role, taskID, branch string) (ControllerWorkerLease, error) {
	store := NewControllerStateStore(filepath.Join(config.WorkspacesRoot, "controller-state.json"))
	state, err := store.Load()
	if err != nil {
		return ControllerWorkerLease{}, err
	}
	lease, found := findControllerLease(state, role)
	if !found || lease.Intent.TaskID != taskID || lease.Intent.Branch != branch {
		return ControllerWorkerLease{}, fmt.Errorf("no matching worker lease for %s on %q", taskID, role)
	}
	if lease.Terminal == nil {
		return ControllerWorkerLease{}, fmt.Errorf("worker %s has no terminal evidence; verify it stopped and recover its lease first", role)
	}
	return lease, nil
}
