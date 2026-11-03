package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/logx"
)

const retryRequestsFile = "retry-requests.json"

type liveRetryRequest struct {
	LeaseID    string    `json:"lease_id"`
	Reason     string    `json:"reason"`
	ApprovedAt time.Time `json:"approved_at"`
}

// FindControllerLeaseByTarget finds an active or historical lease by role name or task ID.
func FindControllerLeaseByTarget(state ControllerState, target string) (ControllerWorkerLease, bool) {
	target = strings.TrimSpace(target)
	for _, lease := range state.Leases {
		if strings.EqualFold(lease.Intent.Role, target) || strings.EqualFold(lease.Intent.TaskID, target) {
			return lease, true
		}
	}
	for _, lease := range state.History {
		if strings.EqualFold(lease.Intent.Role, target) || strings.EqualFold(lease.Intent.TaskID, target) {
			return lease, true
		}
	}
	return ControllerWorkerLease{}, false
}

func queueLiveRetryApproval(leaseID, reason string) error {
	path := filepath.Join(config.WorkspacesRoot, retryRequestsFile)
	var requests []liveRetryRequest
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &requests)
	}
	requests = append(requests, liveRetryRequest{
		LeaseID:    leaseID,
		Reason:     reason,
		ApprovedAt: time.Now(),
	})
	data, err := json.MarshalIndent(requests, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func consumePendingRetryRequestsLocked(store *ControllerStateStore, durable *ControllerState) {
	path := filepath.Join(config.WorkspacesRoot, retryRequestsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var requests []liveRetryRequest
	if err := json.Unmarshal(data, &requests); err != nil || len(requests) == 0 {
		_ = os.Remove(path)
		return
	}
	if durable.RetryApprovals == nil {
		durable.RetryApprovals = make(map[string]string)
	}
	for _, req := range requests {
		durable.RetryApprovals[req.LeaseID] = req.Reason
		durable.RetryHistory = append(durable.RetryHistory, ControllerRetryApproval{
			LeaseID:    req.LeaseID,
			Reason:     req.Reason,
			ApprovedAt: req.ApprovedAt,
		})
		logx.Event("applied live retry approval for lease %s: %s", req.LeaseID, req.Reason)
	}
	if err := store.Commit(*durable); err == nil {
		_ = os.Remove(path)
	}
}

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
func RecoverStoppedRole(target string) error {
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
		if !strings.EqualFold(lease.Intent.Role, target) && !strings.EqualFold(lease.Intent.TaskID, target) {
			continue
		}
		if lease.Terminal != nil {
			return fmt.Errorf("lease for %q already has a terminal result", target)
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
	return fmt.Errorf("no lease found for %q", target)
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

// ApproveTerminalRetry records a one-use operator override without erasing the
// original terminal evidence. If the controller is running, it queues the approval for the next tick.
func ApproveTerminalRetry(target, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "operator retry approved"
	}
	if len(reason) > 2000 {
		return fmt.Errorf("retry requires a reason of 1 to 2000 characters")
	}

	store := NewControllerStateStore(filepath.Join(config.WorkspacesRoot, "controller-state.json"))
	state, err := store.Load()
	if err != nil {
		return err
	}
	lease, found := FindControllerLeaseByTarget(state, target)
	if !found {
		return fmt.Errorf("no lease found for %q", target)
	}
	if lease.Terminal == nil {
		return fmt.Errorf("role %q has no terminal result; resolve the worker lease first", target)
	}
	if lease.Terminal.Outcome != string(sessionBlocked) && lease.Terminal.Outcome != string(sessionFailed) && lease.Terminal.Outcome != string(sessionUnknown) {
		return fmt.Errorf("role %q has no blocked or failed result to retry", target)
	}

	unlock, err := lockController()
	if err != nil {
		// Controller is running! Queue live retry request.
		if err := queueLiveRetryApproval(lease.LeaseID, reason); err != nil {
			return fmt.Errorf("failed to queue live retry: %w", err)
		}
		logx.Event("queued live retry approval for %s (lease %s)", target, lease.LeaseID)
		return nil
	}
	defer unlock()

	if state.RetryApprovals == nil {
		state.RetryApprovals = make(map[string]string)
	}
	if state.RetryApprovals[lease.LeaseID] != reason {
		state.RetryHistory = append(state.RetryHistory, ControllerRetryApproval{LeaseID: lease.LeaseID, Reason: reason, ApprovedAt: time.Now()})
	}
	state.RetryApprovals[lease.LeaseID] = reason
	return store.Commit(state)
}
