package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const ControllerStateVersion = 1

// ControllerState is the durable snapshot owned and committed by the
// orchestrator controller. Intent is copied from the board; runtime fields are
// observations owned by the controller.
type ControllerState struct {
	Version int                     `json:"version"`
	Leases  []ControllerWorkerLease `json:"leases"`
}

// ControllerTaskIntent is the immutable board identity for one assignment.
type ControllerTaskIntent struct {
	TaskID string `json:"task_id"`
	Role   string `json:"role"`
	Branch string `json:"branch"`
}

// ControllerTerminalResult records evidence for a terminal worker outcome.
type ControllerTerminalResult struct {
	Outcome    string    `json:"outcome"`
	FinishedAt time.Time `json:"finished_at"`
	Evidence   string    `json:"evidence"`
}

// ControllerWorkerLease combines immutable board intent with controller-owned
// runtime observations. Each role may have at most one lease.
type ControllerWorkerLease struct {
	Intent      ControllerTaskIntent      `json:"intent"`
	LeaseID     string                    `json:"lease_id"`
	HeartbeatAt time.Time                 `json:"heartbeat_at"`
	Attempt     int                       `json:"attempt"`
	Terminal    *ControllerTerminalResult `json:"terminal,omitempty"`
}

// ControllerStateStore is the single-writer persistence boundary. A controller
// submits complete candidate snapshots through Commit; there are no per-worker
// mutation operations.
type ControllerStateStore struct {
	path string
}

func NewControllerStateStore(path string) *ControllerStateStore {
	return &ControllerStateStore{path: path}
}

// Load reads and validates the last committed controller snapshot.
func (s *ControllerStateStore) Load() (ControllerState, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return ControllerState{}, err
	}
	var state ControllerState
	if err := json.Unmarshal(data, &state); err != nil {
		return ControllerState{}, fmt.Errorf("decode controller state: %w", err)
	}
	if err := ValidateControllerState(state); err != nil {
		return ControllerState{}, err
	}
	return state, nil
}

// Commit validates and atomically replaces the durable snapshot. Invalid
// candidates never touch the currently committed file.
func (s *ControllerStateStore) Commit(candidate ControllerState) error {
	if err := ValidateControllerState(candidate); err != nil {
		return err
	}
	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return fmt.Errorf("encode controller state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create controller state directory: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".controller-state-*")
	if err != nil {
		return fmt.Errorf("create controller state temp file: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("write controller state: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close controller state: %w", closeErr)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("commit controller state: %w", err)
	}
	return nil
}

func ValidateControllerState(state ControllerState) error {
	if state.Version != ControllerStateVersion {
		return fmt.Errorf("unsupported controller state version %d", state.Version)
	}
	roles := make(map[string]struct{}, len(state.Leases))
	for i, lease := range state.Leases {
		if lease.Intent.TaskID == "" || lease.Intent.Role == "" || lease.Intent.Branch == "" {
			return fmt.Errorf("lease %d: missing required task, role, or branch intent", i)
		}
		if _, exists := roles[lease.Intent.Role]; exists {
			return fmt.Errorf("lease %d: duplicate role lease %q", i, lease.Intent.Role)
		}
		roles[lease.Intent.Role] = struct{}{}
		if lease.LeaseID == "" {
			return fmt.Errorf("lease %d: missing lease id", i)
		}
		if !validTimestamp(lease.HeartbeatAt) {
			return fmt.Errorf("lease %d: invalid heartbeat timestamp", i)
		}
		if lease.Attempt < 1 {
			return fmt.Errorf("lease %d: attempt must be positive", i)
		}
		if lease.Terminal != nil {
			if !validTimestamp(lease.Terminal.FinishedAt) || lease.Terminal.Outcome == "" || lease.Terminal.Evidence == "" {
				return fmt.Errorf("lease %d: terminal result requires outcome, finish timestamp, and evidence", i)
			}
			if lease.Terminal.FinishedAt.Before(lease.HeartbeatAt) {
				return fmt.Errorf("lease %d: terminal finish precedes heartbeat", i)
			}
			if lease.Terminal.Outcome == "running" || lease.Terminal.Outcome == "" {
				return fmt.Errorf("lease %d: contradictory terminal outcome %q", i, lease.Terminal.Outcome)
			}
		}
	}
	return nil
}

func validTimestamp(t time.Time) bool {
	return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999
}
