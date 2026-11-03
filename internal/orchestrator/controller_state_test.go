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

// This models two launch paths that both load the same snapshot before either
// writes. The store rejects the stale second candidate without losing the
// first lease; rebuilding it from the latest snapshot allows the added role.
func TestControllerStaleLaunchSnapshotRejectsThenRecovers(t *testing.T) {
	store := NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	base := ControllerState{Version: ControllerStateVersion}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	lease := func(task, role string) ControllerWorkerLease {
		return ControllerWorkerLease{
			Intent:  ControllerTaskIntent{TaskID: task, Role: role, Branch: "branch/" + task},
			LeaseID: "lease-" + task, HeartbeatAt: now, Attempt: 1,
		}
	}
	teamLeadCandidate := base
	teamLeadCandidate.Leases = []ControllerWorkerLease{lease("lead-task", teamLeadRole)}
	devCandidate := base
	devCandidate.Leases = []ControllerWorkerLease{lease("dev-task", "dev-agent-3")}

	if err := store.Commit(teamLeadCandidate); err != nil {
		t.Fatalf("commit Team Lead lease: %v", err)
	}
	if err := store.Commit(devCandidate); err == nil || !strings.Contains(err.Error(), "removes role lease") {
		t.Fatalf("stale Dev Agent 3 snapshot error = %v, want removed-role rejection", err)
	}

	persisted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Leases) != 1 || persisted.Leases[0].Intent.Role != teamLeadRole {
		t.Fatalf("rejected candidate changed persisted state: %#v", persisted.Leases)
	}

	// A launch retry that reloads and extends the committed snapshot succeeds.
	devCandidate = persisted
	devCandidate.Leases = append(devCandidate.Leases, lease("dev-task", "dev-agent-3"))
	if err := store.Commit(devCandidate); err != nil {
		t.Fatalf("commit reloaded Dev Agent 3 lease: %v", err)
	}
	persisted, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Leases) != 2 {
		t.Fatalf("recovered state has %d leases, want 2: %#v", len(persisted.Leases), persisted.Leases)
	}
}

func TestControllerTransitionAllowsNewScheduleForSameTask(t *testing.T) {
	old := validControllerState()
	next := old
	next.Leases = append([]ControllerWorkerLease(nil), old.Leases...)
	next.Leases[0].LeaseID = "new-schedule-key"
	next.Leases[0].Attempt = 1
	next.Leases[0].Terminal = nil
	if err := ValidateControllerTransition(old, next); err != nil {
		t.Fatalf("terminal assignment could not be replaced: %v", err)
	}
	old.Leases = append([]ControllerWorkerLease(nil), old.Leases...)
	old.Leases[0].Terminal = nil
	if err := ValidateControllerTransition(old, next); err == nil {
		t.Fatal("replaced an active assignment")
	}
}
