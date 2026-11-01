package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validControllerState() ControllerState {
	heartbeat := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return ControllerState{
		Version: ControllerStateVersion,
		Leases: []ControllerWorkerLease{{
			Intent:      ControllerTaskIntent{TaskID: "LOOP-01", Role: "dev-agent-1", Branch: "agent/1/persist-controller-state-model"},
			LeaseID:     "lease-123",
			HeartbeatAt: heartbeat,
			Attempt:     2,
			Terminal:    &ControllerTerminalResult{Outcome: "completed", FinishedAt: heartbeat.Add(time.Minute), Evidence: "commit abc123"},
		}},
	}
}

func TestControllerStateRoundTrip(t *testing.T) {
	store := NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	want := validControllerState()
	if err := store.Commit(want); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Version != want.Version || len(got.Leases) != 1 {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
	gotLease, wantLease := got.Leases[0], want.Leases[0]
	if gotLease.Intent != wantLease.Intent || gotLease.LeaseID != wantLease.LeaseID ||
		!gotLease.HeartbeatAt.Equal(wantLease.HeartbeatAt) || gotLease.Attempt != wantLease.Attempt ||
		gotLease.Terminal == nil || *gotLease.Terminal != *wantLease.Terminal {
		t.Fatalf("lease evidence did not round-trip: got %#v, want %#v", gotLease, wantLease)
	}
}

func TestControllerStateRejectsInvalidCandidatesAndPreservesCommit(t *testing.T) {
	store := NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	valid := validControllerState()
	if err := store.Commit(valid); err != nil {
		t.Fatalf("initial Commit() error = %v", err)
	}

	duplicate := valid
	duplicate.Leases = append(append([]ControllerWorkerLease(nil), valid.Leases...), valid.Leases[0])
	missingIntent := valid
	missingIntent.Leases = append([]ControllerWorkerLease(nil), valid.Leases...)
	missingIntent.Leases[0].Intent.TaskID = ""
	badHeartbeat := valid
	badHeartbeat.Leases = append([]ControllerWorkerLease(nil), valid.Leases...)
	badHeartbeat.Leases[0].HeartbeatAt = time.Time{}
	badTerminalTime := valid
	badTerminalTime.Leases = append([]ControllerWorkerLease(nil), valid.Leases...)
	badTerminalTime.Leases[0].Terminal.FinishedAt = time.Time{}
	contradictory := valid
	contradictory.Leases = append([]ControllerWorkerLease(nil), valid.Leases...)
	contradictory.Leases[0].Terminal.Outcome = "running"
	unknownVersion := valid
	unknownVersion.Version++

	for name, candidate := range map[string]ControllerState{
		"unknown version":               unknownVersion,
		"duplicate role":                duplicate,
		"missing intent":                missingIntent,
		"invalid heartbeat":             badHeartbeat,
		"invalid terminal timestamp":    badTerminalTime,
		"contradictory terminal result": contradictory,
	} {
		t.Run(name, func(t *testing.T) {
			if err := store.Commit(candidate); err == nil {
				t.Fatal("Commit() accepted invalid candidate")
			}
			got, err := store.Load()
			if err != nil {
				t.Fatalf("Load() after rejected candidate: %v", err)
			}
			if got.Leases[0].LeaseID != valid.Leases[0].LeaseID {
				t.Fatalf("rejected candidate replaced durable state: %#v", got)
			}
		})
	}
}

func TestControllerStateLoadRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"version":999,"leases":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewControllerStateStore(path).Load(); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Load() error = %v, want unsupported version", err)
	}
}
