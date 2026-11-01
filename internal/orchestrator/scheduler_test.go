package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
)

func TestCanRetryFailedSessionAfterDelayWithoutSideEffects(t *testing.T) {
	withRetryTestState(t)
	now := time.Now()
	session := FinishedSession{
		Role:       devAgent1Role,
		TaskKey:    "task-1",
		Outcome:    sessionFailed,
		FinishedAt: now.Add(-config.FailedSessionRetryDelay),
	}

	mu.Lock()
	defer mu.Unlock()

	canRetry, consumed := canRetryFailedSessionLocked(devAgent1Role, session, now)
	if !canRetry {
		t.Fatal("expected failed session to be eligible for retry after cooldown")
	}
	if consumed != 0 {
		t.Fatalf("consumed retries = %d, want 0", consumed)
	}

	// The eligibility check must not have incremented the counter.
	key := failedSessionRetryKey(devAgent1Role, session.TaskKey)
	if failedSessionRetryCounts[key] != 0 {
		t.Fatalf("retry count = %d, want 0 (check must be side-effect free)", failedSessionRetryCounts[key])
	}

	// Recording a retry is the only thing that consumes the budget.
	if got := recordFailedSessionRetryLocked(devAgent1Role, session.TaskKey); got != 1 {
		t.Fatalf("recordFailedSessionRetryLocked = %d, want 1", got)
	}
	if failedSessionRetryCounts[key] != 1 {
		t.Fatalf("retry count = %d, want 1", failedSessionRetryCounts[key])
	}
}

func TestRecoveryPolicyRecognizesTransientAndAmbiguousResults(t *testing.T) {
	base := ControllerWorkerLease{LeaseID: "lease", Attempt: 1, HeartbeatAt: time.Now()}
	for _, outcome := range []SessionOutcome{sessionFailed, sessionCancelled, sessionUsageLimited} {
		lease := base
		lease.Terminal = &ControllerTerminalResult{Outcome: string(outcome), FinishedAt: time.Now(), Evidence: "worker reported"}
		if !retryableDurableResult(lease) {
			t.Errorf("%q should be retryable", outcome)
		}
	}
	for _, outcome := range []SessionOutcome{sessionUnknown, sessionCompleted} {
		lease := base
		lease.Terminal = &ControllerTerminalResult{Outcome: string(outcome), FinishedAt: time.Now(), Evidence: "worker reported"}
		if retryableDurableResult(lease) {
			t.Errorf("%q should require review or remain complete", outcome)
		}
	}
}

func TestExpiredLeaseIsRetryableAndSameLiveLeaseIsNot(t *testing.T) {
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{Role: devAgent1Role}, LeaseID: "id", HeartbeatAt: time.Now().Add(-11 * time.Minute), Attempt: 1}
	state := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}
	if _, launch := reconcileLeaseDecision(state, devAgent1Role, "id"); !launch {
		t.Fatal("expired lease should be recovered")
	}
	lease.HeartbeatAt = time.Now()
	state.Leases[0] = lease
	if _, launch := reconcileLeaseDecision(state, devAgent1Role, "id"); launch {
		t.Fatal("live lease should suppress duplicate launch")
	}
}

func TestRecoveryRetryCountComesFromDurableAttempt(t *testing.T) {
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{Role: devAgent1Role}, LeaseID: "id", Attempt: config.MaxFailedSessionRetries + 1, HeartbeatAt: time.Now(), Terminal: &ControllerTerminalResult{Outcome: string(sessionFailed), FinishedAt: time.Now(), Evidence: "transient"}}
	state := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}
	got, retry := reconcileLeaseDecision(state, devAgent1Role, "id")
	if retry || got.Attempt != config.MaxFailedSessionRetries+1 {
		t.Fatalf("durable exhaustion decision = %#v, launch %v", got, retry)
	}
}

func TestRecoveryDecisionSnapshotExposesExhaustionAndAmbiguousEvidence(t *testing.T) {
	withRetryTestState(t)
	for _, tc := range []struct {
		name, taskKey, outcome, evidence string
		attempt                          int
		want                             string
	}{
		{"exhaustion", "spent-task", string(sessionFailed), "transient: worker exited", config.MaxFailedSessionRetries + 1, "Blocked: retry attempts exhausted"},
		{"ambiguity", "unclear-task", string(sessionUnknown), "provider returned an unclassified result", 1, "Blocked: ambiguous worker result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leaseID := "lease-" + tc.taskKey
			now := time.Now()
			lease := ControllerWorkerLease{Intent: ControllerTaskIntent{Role: devAgent1Role}, LeaseID: leaseID, Attempt: tc.attempt, HeartbeatAt: now, Terminal: &ControllerTerminalResult{Outcome: tc.outcome, FinishedAt: now, Evidence: tc.evidence}}
			state := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}
			_, launch, reason := recoveryLeaseDecision(state, devAgent1Role, leaseID)
			if launch || !strings.Contains(reason, tc.want) {
				t.Fatalf("decision = launch %v, reason %q", launch, reason)
			}
			recordRecoveryDecision(tc.taskKey, reason)
			got := Snapshot().Recovery[tc.taskKey]
			if !strings.Contains(got, "Blocked") || !strings.Contains(got, "Team Lead") || !strings.Contains(got, tc.evidence) {
				t.Fatalf("snapshot decision %q did not retain required decision/evidence", got)
			}
		})
	}
}

func TestBlockedLeaseDoesNotSuppressDistinctReadyRole(t *testing.T) {
	blockedID := "blocked"
	now := time.Now()
	blocked := ControllerWorkerLease{Intent: ControllerTaskIntent{Role: devAgent1Role}, LeaseID: blockedID, Attempt: config.MaxFailedSessionRetries + 1, HeartbeatAt: now, Terminal: &ControllerTerminalResult{Outcome: string(sessionFailed), FinishedAt: now, Evidence: "exhausted"}}
	state := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{blocked}}
	_, launchBlocked, reason := recoveryLeaseDecision(state, devAgent1Role, blockedID)
	if launchBlocked || reason == "" {
		t.Fatalf("blocked role decision = launch %v, reason %q", launchBlocked, reason)
	}
	_, launchReady, readyReason := recoveryLeaseDecision(state, devAgent2Role, "new-ready-task")
	if !launchReady || readyReason != "" {
		t.Fatalf("independent ready role decision = launch %v, reason %q", launchReady, readyReason)
	}
}

func TestSuccessfulRecoveryLeaseClearsStaleDecision(t *testing.T) {
	withRetryTestState(t)
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "controller.json"))
	taskKey := "recover-me"
	recordRecoveryDecision(taskKey, "Blocked: retry attempts exhausted; Team Lead review required")
	now := time.Now()
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: "task", Role: devAgent1Role, Branch: "branch"}, LeaseID: "new-lease", HeartbeatAt: now, Attempt: 2}
	if err := commitRecoveryLease(ControllerState{Version: ControllerStateVersion}, lease, taskKey); err != nil {
		t.Fatal(err)
	}
	if got := Snapshot().Recovery[taskKey]; got != "" {
		t.Fatalf("stale recovery decision remains after durable lease: %q", got)
	}
	loaded, err := loadControllerState()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Leases) != 1 || loaded.Leases[0].LeaseID != lease.LeaseID {
		t.Fatalf("recovery lease not durably recorded: %#v", loaded.Leases)
	}
}

func TestCanRetryFailedSessionBeforeDelay(t *testing.T) {
	withRetryTestState(t)
	now := time.Now()
	session := FinishedSession{
		Role:       devAgent1Role,
		TaskKey:    "task-1",
		Outcome:    sessionFailed,
		FinishedAt: now.Add(-config.FailedSessionRetryDelay + time.Second),
	}

	mu.Lock()
	defer mu.Unlock()

	canRetry, _ := canRetryFailedSessionLocked(devAgent1Role, session, now)
	if canRetry {
		t.Fatal("expected failed session to wait for cooldown")
	}
}

func TestCanRetryStopsAtLimit(t *testing.T) {
	withRetryTestState(t)
	now := time.Now()
	session := FinishedSession{
		Role:       devAgent1Role,
		TaskKey:    "task-1",
		Outcome:    sessionFailed,
		FinishedAt: now.Add(-config.FailedSessionRetryDelay),
	}
	key := failedSessionRetryKey(devAgent1Role, session.TaskKey)
	failedSessionRetryCounts[key] = config.MaxFailedSessionRetries

	mu.Lock()
	defer mu.Unlock()

	canRetry, consumed := canRetryFailedSessionLocked(devAgent1Role, session, now)
	if canRetry {
		t.Fatal("expected failed session retry limit to be enforced")
	}
	if consumed != config.MaxFailedSessionRetries {
		t.Fatalf("consumed retries = %d, want %d", consumed, config.MaxFailedSessionRetries)
	}
}

func TestCanRetryIgnoresCompletedSession(t *testing.T) {
	withRetryTestState(t)
	now := time.Now()
	session := FinishedSession{
		Role:       devAgent1Role,
		TaskKey:    "task-1",
		Outcome:    sessionCompleted,
		FinishedAt: now.Add(-config.FailedSessionRetryDelay),
	}

	mu.Lock()
	defer mu.Unlock()

	if canRetry, _ := canRetryFailedSessionLocked(devAgent1Role, session, now); canRetry {
		t.Fatal("expected completed session not to be retried")
	}
}

func TestShouldNotRetryUsageLimitedSession(t *testing.T) {
	withRetryTestState(t)
	now := time.Now()
	session := FinishedSession{
		Role:       devAgent1Role,
		TaskKey:    "task-1",
		Outcome:    sessionUsageLimited,
		FinishedAt: now.Add(-config.FailedSessionRetryDelay),
	}

	mu.Lock()
	defer mu.Unlock()

	canRetry, consumed := canRetryFailedSessionLocked(devAgent1Role, session, now)
	if canRetry {
		t.Fatal("expected usage-limited session not to use normal failed-session retry")
	}
	if consumed != 0 {
		t.Fatalf("consumed retries = %d, want 0", consumed)
	}
}

func TestUsageLimitedSessionResumesAfterCooldown(t *testing.T) {
	withRetryTestState(t)
	now := time.Now()
	session := FinishedSession{
		Role:    devAgent1Role,
		TaskKey: "task-1",
		Outcome: sessionUsageLimited,
	}

	mu.Lock()
	defer mu.Unlock()

	// Cooldown still active -> lane stays blocked.
	codexPausedUntil = now.Add(time.Hour)
	if shouldResumeAfterUsageLimitLocked(session, now) {
		t.Fatal("expected usage-limited lane to stay blocked while cooldown is active")
	}

	// Cooldown elapsed -> lane resumes.
	codexPausedUntil = now.Add(-time.Minute)
	if !shouldResumeAfterUsageLimitLocked(session, now) {
		t.Fatal("expected usage-limited lane to resume once cooldown elapsed")
	}

	// No pause recorded at all -> also resumable (defensive).
	codexPausedUntil = time.Time{}
	if !shouldResumeAfterUsageLimitLocked(session, now) {
		t.Fatal("expected usage-limited lane to resume when no cooldown is set")
	}
}

func TestNonUsageLimitedSessionsDoNotUseUsageLimitResume(t *testing.T) {
	withRetryTestState(t)
	now := time.Now()

	mu.Lock()
	defer mu.Unlock()
	codexPausedUntil = time.Time{}

	for _, outcome := range []SessionOutcome{sessionFailed, sessionCompleted, sessionUnknown} {
		session := FinishedSession{Role: devAgent1Role, TaskKey: "task-1", Outcome: outcome}
		if shouldResumeAfterUsageLimitLocked(session, now) {
			t.Fatalf("outcome %q should not resume via the usage-limit path", outcome)
		}
	}
}

func TestCodexLaunchPauseAfterUsageLimit(t *testing.T) {
	withRetryTestState(t)
	now := time.Now()
	until := pauseCodexLaunchesAfterUsageLimit(now)

	paused, activeUntil := codexLaunchPaused(now.Add(time.Minute))
	if !paused {
		t.Fatal("expected Codex launches to be paused")
	}
	if !activeUntil.Equal(until) {
		t.Fatalf("active pause = %s, want %s", activeUntil, until)
	}

	paused, _ = codexLaunchPaused(until.Add(time.Second))
	if paused {
		t.Fatal("expected Codex launch pause to expire")
	}
}

func TestDecideCancel(t *testing.T) {
	now := time.Now()
	within := now.Add(-config.CancelGracePeriod + time.Second)
	elapsed := now.Add(-config.CancelGracePeriod - time.Second)

	cases := []struct {
		name       string
		matches    bool
		completing bool
		pending    bool
		since      time.Time
		want       cancelAction
	}{
		{"board still wants it", true, false, false, time.Time{}, cancelKeep},
		{"completing outweighs mismatch", false, true, true, elapsed, cancelKeep},
		{"first mismatch starts the clock", false, false, false, time.Time{}, cancelWait},
		{"mismatch within grace keeps waiting", false, false, true, within, cancelWait},
		{"mismatch past grace cancels", false, false, true, elapsed, cancelNow},
	}

	for _, tc := range cases {
		if got := decideCancel(tc.matches, tc.completing, tc.pending, tc.since, now); got != tc.want {
			t.Errorf("%s: decideCancel = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTeamLeadScheduleKeyChangesWhenDependencyCompletes(t *testing.T) {
	oldCount := config.DevAgentCount
	config.DevAgentCount = 2
	t.Cleanup(func() { config.DevAgentCount = oldCount })

	task := board.Task{
		Section:      "Backlog",
		Title:        "Build next slice",
		ID:           "CR-014",
		Status:       "Backlog",
		Category:     "AFK",
		Dependencies: "CR-001",
		Key:          "Backlog\x00Build next slice\x00\x00\x00Backlog",
	}

	before := teamLeadScheduleKey(task, []board.Task{task}, map[string]bool{})
	after := teamLeadScheduleKey(task, []board.Task{task}, map[string]bool{"CR-001": true})

	if before == after {
		t.Fatalf("expected Team Lead schedule key to change when CR-001 becomes done: %q", before)
	}
}

func TestCompletionFollowUpTaskUsesOnlyEndedDevSessions(t *testing.T) {
	withRetryTestState(t)

	mu.Lock()
	finished[devAgent1Role] = FinishedSession{Role: devAgent1Role, Task: "Completed work", TaskKey: "task-1", Outcome: sessionCompleted}
	finished[devAgent2Role] = FinishedSession{Role: devAgent2Role, Task: "Failed work", TaskKey: "task-2", Outcome: sessionFailed}
	finished[teamLeadRole] = FinishedSession{Role: teamLeadRole, Task: "Groom", TaskKey: "team", Outcome: sessionCompleted}
	mu.Unlock()

	task, ok := completionFollowUpTask()
	if !ok {
		t.Fatal("expected ended dev sessions to schedule a Team Lead follow-up")
	}
	if task.Section != teamLeadFollowUpSection {
		t.Fatalf("section = %q, want %q", task.Section, teamLeadFollowUpSection)
	}
	for _, want := range []string{"Completed work", "Failed work"} {
		if !strings.Contains(task.Body, want) {
			t.Fatalf("follow-up body missing %q: %s", want, task.Body)
		}
	}
	if strings.Contains(task.Body, "Groom") {
		t.Fatalf("follow-up body must exclude Team Lead sessions: %s", task.Body)
	}
}

func TestTeamLeadScheduleKeyChangesWhenDevLaneEmpties(t *testing.T) {
	oldCount := config.DevAgentCount
	config.DevAgentCount = 2
	t.Cleanup(func() { config.DevAgentCount = oldCount })

	backlog := board.Task{
		Section: "Backlog",
		Title:   "Build next slice",
		ID:      "CR-014",
		Status:  "Backlog",
		Key:     "Backlog\x00Build next slice\x00\x00\x00Backlog",
	}
	active := board.Task{
		Section: config.DevAgentSection(1),
		Title:   "Scaffold app",
		ID:      "CR-001",
		Owner:   config.DevAgentRole(1),
		Status:  "In Progress",
		Key:     config.DevAgentSection(1) + "\x00Scaffold app\x00" + config.DevAgentRole(1) + "\x00agent/1/scaffold\x00In Progress",
	}

	busy := teamLeadScheduleKey(backlog, []board.Task{backlog, active}, map[string]bool{})
	empty := teamLeadScheduleKey(backlog, []board.Task{backlog}, map[string]bool{})

	if busy == empty {
		t.Fatalf("expected Team Lead schedule key to change when a dev lane empties: %q", busy)
	}
}

func TestBoardStateHashDetectsAnyBoardEdit(t *testing.T) {
	dir := t.TempDir()
	oldBacklog, oldTasks, oldArchive := config.BacklogFile, config.TasksFile, config.ArchiveFile
	config.BacklogFile = filepath.Join(dir, "BACKLOG.md")
	config.TasksFile = filepath.Join(dir, "TASKS.md")
	config.ArchiveFile = filepath.Join(dir, "ARCHIVE.md")
	t.Cleanup(func() {
		config.BacklogFile = oldBacklog
		config.TasksFile = oldTasks
		config.ArchiveFile = oldArchive
	})

	for _, path := range []string{config.BacklogFile, config.TasksFile, config.ArchiveFile} {
		if err := os.WriteFile(path, []byte("# board\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	before := boardStateHash()
	if before == "" {
		t.Fatal("expected a board hash")
	}
	if again := boardStateHash(); again != before {
		t.Fatalf("board hash changed without an edit: %q != %q", before, again)
	}

	if err := os.WriteFile(config.TasksFile, []byte("# board\n\n### New task\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if after := boardStateHash(); after == before {
		t.Fatal("expected a board edit to change the hash")
	}
}

func TestReconcileRestartKeepsRecordedLease(t *testing.T) {
	withRetryTestState(t)
	oldStore := controllerStateStore
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "controller.json"))
	t.Cleanup(func() { controllerStateStore = oldStore })
	task := board.Task{ID: "LOOP-02", Branch: "agent/1/reconcile", Key: "LOOP-02", Title: "reconcile"}
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: controllerTaskID(task, task.Key), Role: devAgent1Role, Branch: task.Branch}, LeaseID: controllerLeaseID(devAgent1Role, task, task.Key), HeartbeatAt: time.Now(), Attempt: 1}
	state := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}
	if err := commitControllerState(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadControllerState()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := findControllerLease(loaded, devAgent1Role)
	if !ok || got.LeaseID != lease.LeaseID {
		t.Fatalf("restart lost lease: %#v", got)
	}
	if got.Terminal != nil {
		t.Fatal("missing heartbeat/result was treated as terminal success")
	}
	// A simulated restart has no process-local running entry. Exercise the same
	// durable gate reconcile uses before it reaches workspace/launch handling.
	mu.Lock()
	running = map[string]RunningSession{}
	mu.Unlock()
	if _, launch := reconcileLeaseDecision(loaded, devAgent1Role, controllerLeaseID(devAgent1Role, task, task.Key)); launch {
		t.Fatal("reconciliation would launch a duplicate for the recorded intent")
	}
	// Reapplying unchanged intent replaces the same role lease, it cannot append
	// another lease or produce another launch decision.
	loaded.Leases = replaceControllerLease(loaded.Leases, lease)
	if err := commitControllerState(loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = loadControllerState()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Leases) != 1 {
		t.Fatalf("unchanged reconciliation has %d leases, want 1", len(loaded.Leases))
	}
}

func TestReconcileIdempotentTerminalLeaseRetained(t *testing.T) {
	withRetryTestState(t)
	oldStore := controllerStateStore
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "controller.json"))
	t.Cleanup(func() { controllerStateStore = oldStore })
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	task := board.Task{ID: "LOOP-02", Branch: "agent/1/reconcile", Key: "LOOP-02", Title: "reconcile"}
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: controllerTaskID(task, task.Key), Role: devAgent1Role, Branch: task.Branch}, LeaseID: controllerLeaseID(devAgent1Role, task, task.Key), HeartbeatAt: now, Attempt: 1, Terminal: &ControllerTerminalResult{Outcome: string(sessionCompleted), FinishedAt: now.Add(time.Minute), Evidence: "worker result observed"}}
	state := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}
	if err := commitControllerState(state); err != nil {
		t.Fatal(err)
	}
	fingerprintKey := "LOOP-02\x00lane=dev-agent-1\x00done=prior"
	restoreTerminalLease(lease, task, fingerprintKey)
	restoreTerminalLease(lease, task, fingerprintKey)
	mu.Lock()
	got := finished[devAgent1Role]
	mu.Unlock()
	if got.Outcome != sessionCompleted || got.FinishedAt != lease.Terminal.FinishedAt || got.TaskKey != fingerprintKey {
		t.Fatalf("terminal evidence not reconstructed: %#v", got)
	}
	// Repeated restoration for the same fingerprint must leave the consumed
	// result keyed to that desired intent (and therefore stable for follow-up).
	restoreTerminalLease(lease, task, fingerprintKey)
	mu.Lock()
	gotAgain := finished[devAgent1Role]
	mu.Unlock()
	if gotAgain.TaskKey != fingerprintKey {
		t.Fatalf("terminal restoration changed task key: got %q want %q", gotAgain.TaskKey, fingerprintKey)
	}
	loaded, err := loadControllerState()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Leases) != 1 || loaded.Leases[0].Terminal == nil || loaded.Leases[0].Terminal.Evidence != "worker result observed" {
		t.Fatalf("terminal evidence was not retained: %#v", loaded)
	}
}

func withRetryTestState(t *testing.T) {
	t.Helper()

	oldLogFilePath := config.LogFilePath
	config.LogFilePath = filepath.Join(t.TempDir(), "orchestrator.log")
	failedSessionRetryCounts = map[string]int{}
	circuitBreakers = map[string]*circuitBreaker{}
	recoveryDecisions = map[string]string{}
	codexPausedUntil = time.Time{}

	t.Cleanup(func() {
		config.LogFilePath = oldLogFilePath
		failedSessionRetryCounts = map[string]int{}
		circuitBreakers = map[string]*circuitBreaker{}
		recoveryDecisions = map[string]string{}
		codexPausedUntil = time.Time{}
	})
}

func TestCircuitBreakerOpensAfterThreshold(t *testing.T) {
	withRetryTestState(t)
	now := time.Now()
	key := "role\x00task-1"

	mu.Lock()
	defer mu.Unlock()

	for i := 0; i < circuitThreshold; i++ {
		recordCircuitResultLocked(key, false, now)
	}
	if !circuitOpenLocked(key, now) {
		t.Fatal("expected circuit to be open after threshold failures")
	}

	// Success resets
	recordCircuitResultLocked(key, true, now.Add(circuitBaseDelay))
	if circuitOpenLocked(key, now.Add(circuitBaseDelay)) {
		t.Fatal("expected circuit to be closed after success")
	}
}
