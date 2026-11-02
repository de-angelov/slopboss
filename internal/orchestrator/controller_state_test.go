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

func TestControllerTransitionRejectsMutationBeforePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewControllerStateStore(path)
	valid := validControllerState()
	if err := store.Commit(valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Leases = append([]ControllerWorkerLease(nil), valid.Leases...)
	invalid.Leases[0].Intent.TaskID = "LOOP-99"
	if err := store.Commit(invalid); err == nil {
		t.Fatal("Commit() accepted mutation of an existing lease identity")
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Leases[0].Intent != valid.Leases[0].Intent || *got.Leases[0].Terminal != *valid.Leases[0].Terminal {
		t.Fatalf("invalid transition changed durable state: got %#v", got)
	}
}

func TestControllerTransitionAtomicallyReplacesCompleteState(t *testing.T) {
	store := NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	initial := validControllerState()
	initial.Leases[0].Terminal = nil
	if err := store.Commit(initial); err != nil {
		t.Fatal(err)
	}
	next := initial
	next.Leases = append([]ControllerWorkerLease(nil), initial.Leases...)
	next.Leases[0].HeartbeatAt = initial.Leases[0].HeartbeatAt.Add(2 * time.Minute)
	next.Leases[0].Terminal = &ControllerTerminalResult{Outcome: "failed", FinishedAt: initial.Leases[0].HeartbeatAt.Add(3 * time.Minute), Evidence: "worker reported failure"}
	next.Leases = append(next.Leases, ControllerWorkerLease{
		Intent:  ControllerTaskIntent{TaskID: "LOOP-02", Role: "dev-agent-2", Branch: "agent/2/task"},
		LeaseID: "lease-456", HeartbeatAt: initial.Leases[0].HeartbeatAt, Attempt: 1,
	})
	if err := store.Commit(next); err != nil {
		t.Fatalf("Commit() valid transition: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Leases) != 2 || got.Leases[0].HeartbeatAt != next.Leases[0].HeartbeatAt ||
		got.Leases[0].Terminal == nil || *got.Leases[0].Terminal != *next.Leases[0].Terminal ||
		got.Leases[1] != next.Leases[1] {
		t.Fatalf("Commit() did not replace the complete candidate snapshot: got %#v, want %#v", got, next)
	}
}

func TestControllerStaleSnapshotsRejectIndependentRoleUpdates(t *testing.T) {
	store := NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	initial := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{
		{Intent: ControllerTaskIntent{TaskID: "DOG-10", Role: "team-lead", Branch: "agent/tl/task"}, LeaseID: "team-lead-lease", HeartbeatAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Attempt: 1},
		{Intent: ControllerTaskIntent{TaskID: "DOG-11", Role: "dev-agent-3", Branch: "agent/3/task"}, LeaseID: "dev-agent-3-lease", HeartbeatAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Attempt: 1},
	}}
	if err := store.Commit(initial); err != nil {
		t.Fatal(err)
	}

	// Two independent handlers load the same durable snapshot, then update
	// different roles. Commit serializes writes but cannot refresh stale input.
	teamLeadSnapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	devAgentSnapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	teamLeadSnapshot.Leases[0].HeartbeatAt = initial.Leases[0].HeartbeatAt.Add(time.Minute)
	devAgentSnapshot.Leases[1].HeartbeatAt = initial.Leases[1].HeartbeatAt.Add(2 * time.Minute)

	if err := store.Commit(teamLeadSnapshot); err != nil {
		t.Fatalf("Team Lead update: %v", err)
	}
	if err := store.Commit(devAgentSnapshot); err == nil || !strings.Contains(err.Error(), "regresses heartbeat for role \"team-lead\"") {
		t.Fatalf("stale Dev Agent update error = %v, want Team Lead heartbeat regression", err)
	}

	persisted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.Leases[0].HeartbeatAt.Equal(teamLeadSnapshot.Leases[0].HeartbeatAt) ||
		!persisted.Leases[1].HeartbeatAt.Equal(initial.Leases[1].HeartbeatAt) {
		t.Fatalf("persisted state after stale rejection = %#v", persisted)
	}

	// Retrying from the newly committed snapshot preserves both role updates.
	persisted.Leases[1].HeartbeatAt = initial.Leases[1].HeartbeatAt.Add(2 * time.Minute)
	if err := store.Commit(persisted); err != nil {
		t.Fatalf("Dev Agent retry from fresh snapshot: %v", err)
	}
	recovered, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.Leases[0].HeartbeatAt.Equal(teamLeadSnapshot.Leases[0].HeartbeatAt) ||
		!recovered.Leases[1].HeartbeatAt.Equal(initial.Leases[1].HeartbeatAt.Add(2*time.Minute)) {
		t.Fatalf("persisted state after refreshed retry = %#v", recovered)
	}
}

func TestControllerTransitionPersistsNextRetryAttempt(t *testing.T) {
	store := NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	terminal := validControllerState()
	terminal.Leases[0].Attempt = 3
	terminal.Leases[0].Terminal.Outcome = "failed"
	if err := store.Commit(terminal); err != nil {
		t.Fatal(err)
	}

	retry := terminal
	retry.Leases = append([]ControllerWorkerLease(nil), terminal.Leases...)
	retry.Leases[0].Attempt++
	retry.Leases[0].HeartbeatAt = terminal.Leases[0].Terminal.FinishedAt.Add(time.Second)
	retry.Leases[0].Terminal = nil
	if err := store.Commit(retry); err != nil {
		t.Fatalf("Commit() legal retry attempt: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Leases[0].LeaseID != terminal.Leases[0].LeaseID || got.Leases[0].Intent != terminal.Leases[0].Intent ||
		got.Leases[0].Attempt != terminal.Leases[0].Attempt+1 || got.Leases[0].Terminal != nil {
		t.Fatalf("retry state was not persisted: got %#v", got.Leases[0])
	}
}
