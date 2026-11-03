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

func TestTeamLeadPlannerPrioritizesFollowUpAndFallsBackToAudit(t *testing.T) {
	withRetryTestState(t)
	oldFinished, oldActivity := finished, lastTeamLeadActivity
	t.Cleanup(func() { finished, lastTeamLeadActivity = oldFinished, oldActivity })
	finished = map[string]FinishedSession{devAgent1Role: {Role: devAgent1Role, Task: "Finished work", TaskKey: "work", Outcome: sessionCompleted}}
	lastTeamLeadActivity = time.Time{}
	blocked := []board.Task{{ID: "BLOCKED-1", Title: "Blocked", Section: "Backlog", Status: "Backlog", Dependencies: "MISSING"}}
	state := ControllerState{}
	plan := func() (desiredSession, bool) {
		return planTeamLeadAction(blocked, map[string]bool{}, state, map[string]desiredSession{}, map[string]bool{})
	}
	action, ok := plan()
	if !ok || action.Task.Section != teamLeadFollowUpSection {
		t.Fatalf("expected completion follow-up first, got %#v", action)
	}
	finished = map[string]FinishedSession{}
	action, ok = plan()
	if !ok || action.Task.Section != teamLeadDecomposeSection {
		t.Fatalf("expected decomposition, got %#v", action)
	}
	state.Leases = []ControllerWorkerLease{{Intent: ControllerTaskIntent{Role: teamLeadRole}, LeaseID: controllerLeaseID(teamLeadRole, action.Task, action.TaskKey), Attempt: config.MaxFailedSessionRetries + 1, Terminal: &ControllerTerminalResult{Outcome: string(sessionCompleted)}}}
	action, ok = plan()
	if !ok || action.Task.Section != teamLeadIdleAuditSection {
		t.Fatalf("expected audit after exhausted decomposition, got %#v", action)
	}
}

func TestCompletedDecompositionRetriesAfterCooldown(t *testing.T) {
	withRetryTestState(t)
	oldStore, oldLaunch, oldWorkspace := controllerStateStore, launchControllerSession, controllerWorkspaceExists
	oldRunning, oldFinished, oldPending, oldActivity := running, finished, cancelPendingSince, lastTeamLeadActivity
	t.Cleanup(func() {
		controllerStateStore, launchControllerSession, controllerWorkspaceExists = oldStore, oldLaunch, oldWorkspace
		running, finished, cancelPendingSince, lastTeamLeadActivity = oldRunning, oldFinished, oldPending, oldActivity
	})
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "state.json"))
	running, finished, cancelPendingSince = map[string]RunningSession{}, map[string]FinishedSession{}, map[string]time.Time{}
	lastTeamLeadActivity = time.Now()
	tasks := []board.Task{{ID: "WAIT", Title: "Waiting", Section: "Backlog", Status: "Backlog", Dependencies: "MISSING"}}
	task := decompositionTask([]string{"WAIT"})
	finishedAt := time.Now().Add(-config.FailedSessionRetryDelay - time.Second)
	lease := ControllerWorkerLease{
		Intent:  ControllerTaskIntent{TaskID: controllerTaskID(task, task.Key), Role: teamLeadRole, Branch: controllerBranch(task, teamLeadRole)},
		LeaseID: controllerLeaseID(teamLeadRole, task, task.Key), HeartbeatAt: finishedAt, Attempt: 1,
		Terminal: &ControllerTerminalResult{Outcome: string(sessionCompleted), FinishedAt: finishedAt, Evidence: "pass completed"},
	}
	if err := commitControllerState(ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}); err != nil {
		t.Fatal(err)
	}
	launches := 0
	controllerWorkspaceExists = func(string) bool { return true }
	launchControllerSession = func(role string, _ board.Task, _ string, _ []board.Task, _ string) {
		if role == teamLeadRole {
			launches++
		}
	}
	reconcile(tasks)
	if launches != 1 {
		t.Fatalf("unchanged completed decomposition launched %d retries, want 1", launches)
	}
	state, err := loadControllerState()
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Leases[0]; got.Attempt != 2 || got.Terminal != nil {
		t.Fatalf("retry lease = %#v", got)
	}
}

func TestCompletedTeamLeadPassRecoveryIsBounded(t *testing.T) {
	for _, prefix := range []string{"decompose\x00", "unblock\x00"} {
		task := board.Task{Key: prefix + "work"}
		lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: task.Key, Role: teamLeadRole}, LeaseID: controllerLeaseID(teamLeadRole, task, task.Key), Attempt: 1, HeartbeatAt: time.Now(), Terminal: &ControllerTerminalResult{Outcome: string(sessionCompleted), FinishedAt: time.Now(), Evidence: "finished"}}
		state := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}
		if _, launch, _ := recoveryLeaseDecision(state, teamLeadRole, lease.LeaseID); !launch {
			t.Errorf("%q should allow retry", prefix)
		}
		state.Leases[0].Attempt = config.MaxFailedSessionRetries + 1
		if _, launch, _ := recoveryLeaseDecision(state, teamLeadRole, lease.LeaseID); launch {
			t.Errorf("%q exceeded its retry budget", prefix)
		}
	}
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: "ordinary", Role: teamLeadRole}, LeaseID: "ordinary", Attempt: 1, HeartbeatAt: time.Now(), Terminal: &ControllerTerminalResult{Outcome: string(sessionCompleted), FinishedAt: time.Now(), Evidence: "finished"}}
	if _, launch, _ := recoveryLeaseDecision(ControllerState{Leases: []ControllerWorkerLease{lease}}, teamLeadRole, lease.LeaseID); launch {
		t.Fatal("ordinary completed work must not retry")
	}
}

func TestCanRetryFailedSessionAfterDelay(t *testing.T) {
	now := time.Now()
	session := FinishedSession{Role: teamLeadRole, TaskKey: "task-1", Outcome: sessionFailed, FinishedAt: now.Add(-config.FailedSessionRetryDelay)}
	if !canRetryFailedSessionLocked(teamLeadRole, session, now) {
		t.Fatal("expected retry after cooldown")
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

func TestExpiredLeaseRequiresReviewAndSameLiveLeaseIsNotRetried(t *testing.T) {
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{Role: devAgent1Role}, LeaseID: "id", HeartbeatAt: time.Now().Add(-11 * time.Minute), Attempt: 1}
	state := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}
	if _, launch, reason := recoveryLeaseDecision(state, devAgent1Role, "id"); launch || !strings.Contains(reason, "no terminal evidence") {
		t.Fatalf("expired lease must require review: launch=%v reason=%q", launch, reason)
	}
	if _, launch, reason := recoveryLeaseDecision(state, devAgent1Role, "new-task"); launch || !strings.Contains(reason, "no terminal evidence") {
		t.Fatalf("expired lease must prevent a different task: launch=%v reason=%q", launch, reason)
	}
	lease.HeartbeatAt = time.Now()
	state.Leases[0] = lease
	if _, launch, _ := recoveryLeaseDecision(state, devAgent1Role, "id"); launch {
		t.Fatal("live lease should suppress duplicate launch")
	}
}

func TestRecoveryRetryCountComesFromDurableAttempt(t *testing.T) {
	lease := ControllerWorkerLease{Intent: ControllerTaskIntent{Role: devAgent1Role}, LeaseID: "id", Attempt: config.MaxFailedSessionRetries + 1, HeartbeatAt: time.Now(), Terminal: &ControllerTerminalResult{Outcome: string(sessionFailed), FinishedAt: time.Now(), Evidence: "transient"}}
	state := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{lease}}
	got, retry, _ := recoveryLeaseDecision(state, devAgent1Role, "id")
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

	canRetry := canRetryFailedSessionLocked(devAgent1Role, session, now)
	if canRetry {
		t.Fatal("expected failed session to wait for cooldown")
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

	if canRetry := canRetryFailedSessionLocked(devAgent1Role, session, now); canRetry {
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

	canRetry := canRetryFailedSessionLocked(devAgent1Role, session, now)
	if canRetry {
		t.Fatal("expected usage-limited session not to use normal failed-session retry")
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
	until := pauseLaunchesAfterUsageLimit(now)

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
	if _, launch, _ := recoveryLeaseDecision(loaded, devAgent1Role, controllerLeaseID(devAgent1Role, task, task.Key)); launch {
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

func TestTerminalEvidenceHasOneDurableFollowUpHandoff(t *testing.T) {
	withRetryTestState(t)
	mu.Lock()
	finished = map[string]FinishedSession{}
	mu.Unlock()
	oldStore := controllerStateStore
	controllerStateStore = NewControllerStateStore(filepath.Join(t.TempDir(), "controller.json"))
	t.Cleanup(func() { controllerStateStore = oldStore })
	task := board.Task{ID: "SIMP-01", Branch: "agent/1/simp", Key: "SIMP-01", Title: "Simplify terminal lifecycle"}
	leaseID := controllerLeaseID(devAgent1Role, task, task.Key)
	state := ControllerState{Version: ControllerStateVersion, Leases: []ControllerWorkerLease{{
		Intent:  ControllerTaskIntent{TaskID: controllerTaskID(task, task.Key), Role: devAgent1Role, Branch: task.Branch},
		LeaseID: leaseID, HeartbeatAt: time.Now(), Attempt: 1,
	}}}
	if err := commitControllerState(state); err != nil {
		t.Fatal(err)
	}
	if err := reportSessionTerminal(leaseID, sessionCompleted, devAgent1Role, task.Key); err != nil {
		t.Fatal(err)
	}
	first, err := loadControllerState()
	if err != nil {
		t.Fatal(err)
	}
	if err := reportSessionTerminal(leaseID, sessionCompleted, devAgent1Role, task.Key); err != nil {
		t.Fatalf("equivalent terminal evidence should be idempotent: %v", err)
	}
	second, err := loadControllerState()
	if err != nil {
		t.Fatal(err)
	}
	if *first.Leases[0].Terminal != *second.Leases[0].Terminal {
		t.Fatalf("repeated evidence changed durable terminal transition: first %#v, second %#v", first.Leases[0].Terminal, second.Leases[0].Terminal)
	}

	// Follow-up ownership is reconstructed from the durable controller record.
	restoreTerminalLease(second.Leases[0], task, task.Key)
	restored := Snapshot().Finished
	if len(restored) != 1 || restored[devAgent1Role].Outcome != sessionCompleted {
		t.Fatalf("durable terminal result produced finished sessions %#v, want one completed dev session", restored)
	}
	followUp, ok := completionFollowUpTask()
	if !ok || !strings.Contains(followUp.Body, task.Title) {
		t.Fatalf("durable completion did not produce its Team Lead hand-off: task=%#v ok=%v", followUp, ok)
	}
	restoreTerminalLease(second.Leases[0], task, task.Key)
	followUpAgain, ok := completionFollowUpTask()
	if !ok || followUpAgain.Key != followUp.Key {
		t.Fatalf("repeated reconciliation duplicated or changed follow-up: first=%#v second=%#v", followUp, followUpAgain)
	}
}

func withRetryTestState(t *testing.T) {
	t.Helper()

	oldLogFilePath := config.LogFilePath
	config.LogFilePath = filepath.Join(t.TempDir(), "orchestrator.log")
	circuitBreakers = map[string]*circuitBreaker{}
	recoveryDecisions = map[string]string{}
	codexPausedUntil = time.Time{}

	t.Cleanup(func() {
		config.LogFilePath = oldLogFilePath
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

func TestDurableLoopFailureModes(t *testing.T) {
	for _, mode := range []string{
		"controller crash and restart",
		"worker lost after lease acquisition",
		"malformed persisted state",
		"retry exhaustion with independent ready work",
		"retry after restart uses durable attempt",
	} {
		t.Run(mode, func(t *testing.T) {
			withRetryTestState(t)
			root := t.TempDir()
			oldWorkspaces, oldTeamLead, oldAgent1, oldAgent2, oldCount := config.WorkspacesRoot, config.TeamLeadPath, config.Agent1Path, config.Agent2Path, config.DevAgentCount
			config.WorkspacesRoot = filepath.Join(root, "workspaces")
			config.TeamLeadPath = filepath.Join(config.WorkspacesRoot, "repo-tl")
			config.Agent1Path = filepath.Join(config.WorkspacesRoot, "repo-agent-1")
			config.Agent2Path = filepath.Join(config.WorkspacesRoot, "repo-agent-2")
			config.DevAgentCount = 2
			for _, dir := range []string{config.TeamLeadPath, config.Agent1Path, config.Agent2Path} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				config.WorkspacesRoot, config.TeamLeadPath, config.Agent1Path, config.Agent2Path, config.DevAgentCount = oldWorkspaces, oldTeamLead, oldAgent1, oldAgent2, oldCount
			})

			statePath := filepath.Join(root, "controller.json")
			controllerStateStore = NewControllerStateStore(statePath)
			oldLaunch := launchControllerSession
			oldWorkspaceCheck := controllerWorkspaceExists
			launches := []string{}
			launchControllerSession = func(role string, task board.Task, taskKey string, _ []board.Task, leaseID string) {
				launches = append(launches, role+"/"+taskKey+"/"+leaseID)
			}
			controllerWorkspaceExists = func(role string) bool { return role == devAgent1Role || role == devAgent2Role || role == teamLeadRole }
			t.Cleanup(func() { launchControllerSession, controllerWorkspaceExists = oldLaunch, oldWorkspaceCheck })
			mu.Lock()
			running = map[string]RunningSession{}
			finished = map[string]FinishedSession{}
			cancelPendingSince = map[string]time.Time{}
			lastTeamLeadActivity = time.Now()
			mu.Unlock()

			now := time.Now()
			task1 := board.Task{ID: "LOOP-05-A", Key: "LOOP-05-A", Title: "recover task", Section: config.DevAgentSection(1), Owner: devAgent1Role, Status: "In Progress", Branch: "agent/1/recover"}
			task2 := board.Task{ID: "LOOP-05-B", Key: "LOOP-05-B", Title: "independent task", Section: config.DevAgentSection(2), Owner: devAgent2Role, Status: "In Progress", Branch: "agent/2/independent"}
			leaseFor := func(task board.Task, role string, heartbeat time.Time, attempt int, terminal *ControllerTerminalResult) ControllerWorkerLease {
				return ControllerWorkerLease{Intent: ControllerTaskIntent{TaskID: task.ID, Role: role, Branch: task.Branch}, LeaseID: controllerLeaseID(role, task, task.Key), HeartbeatAt: heartbeat, Attempt: attempt, Terminal: terminal}
			}
			tasks := []board.Task{task1}
			state := ControllerState{Version: ControllerStateVersion}
			switch mode {
			case "controller crash and restart":
				state.Leases = []ControllerWorkerLease{leaseFor(task1, devAgent1Role, now, 1, nil)}
			case "worker lost after lease acquisition":
				state.Leases = []ControllerWorkerLease{leaseFor(task1, devAgent1Role, now.Add(-11*time.Minute), 1, nil)}
			case "retry exhaustion with independent ready work":
				fallthrough
			case "retry after restart uses durable attempt":
				tasks = append(tasks, task2)
				finishedAt := now
				attempt := config.MaxFailedSessionRetries + 1
				if mode == "retry after restart uses durable attempt" {
					attempt = 1
					finishedAt = now.Add(-config.FailedSessionRetryDelay)
				}
				state.Leases = []ControllerWorkerLease{
					leaseFor(task1, devAgent1Role, finishedAt, attempt, &ControllerTerminalResult{Outcome: string(sessionFailed), FinishedAt: finishedAt, Evidence: "transient worker failure"}),
				}
			case "malformed persisted state":
				if err := os.WriteFile(statePath, []byte(`{"version":1,"leases":[`), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "malformed persisted state" {
				if err := commitControllerState(state); err != nil {
					t.Fatal(err)
				}
			}

			if mode == "malformed persisted state" {
				if _, err := loadControllerState(); err == nil || !strings.Contains(err.Error(), "decode controller state") {
					t.Fatalf("malformed state error = %v, want descriptive decode error", err)
				}
				reconcile(tasks)
				if len(launches) != 0 {
					t.Fatalf("malformed state started workers: %v", launches)
				}
				return
			}
			if mode == "worker lost after lease acquisition" {
				lost, launch, reason := recoveryLeaseDecision(state, devAgent1Role, state.Leases[0].LeaseID)
				if launch || !strings.Contains(reason, "no terminal evidence") || lost.Attempt != 1 {
					t.Fatalf("expired lease must require review: %#v, launch=%v reason=%q", lost, launch, reason)
				}
				return
			}

			reconcile(tasks)
			firstCount := len(launches)
			reconcile(tasks)
			// Retry exhaustion restores the durable failure into finished state;
			// the existing Team Lead follow-up policy may therefore schedule its
			// own distinct task. The durable gate must still suppress another
			// launch for either task identity.
			for _, launch := range launches[firstCount:] {
				for _, previous := range launches[:firstCount] {
					if launch == previous {
						t.Fatalf("repeat reconcile duplicated launch %q", launch)
					}
				}
			}
			persisted, err := loadControllerState()
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "controller crash and restart":
				if firstCount != 0 || len(persisted.Leases) != 1 || persisted.Leases[0].Attempt != 1 || persisted.Leases[0].Terminal != nil {
					t.Fatalf("restart did not preserve active lease without relaunch: launches=%v state=%#v", launches, persisted)
				}
			case "retry exhaustion with independent ready work":
				recovery := Snapshot().Recovery[task1.Key]
				if !strings.Contains(recovery, "Blocked") || !strings.Contains(recovery, "Team Lead") || !strings.Contains(recovery, "transient worker failure") {
					t.Fatalf("affected task recovery decision = %q", recovery)
				}
				if firstCount != 1 || !strings.Contains(launches[0], devAgent2Role+"/"+task2.Key) {
					t.Fatalf("exhausted task should stay blocked while independent task launches once: %v", launches)
				}
				if len(persisted.Leases) < 2 {
					t.Fatalf("exhaustion lost independent lease: %#v", persisted.Leases)
				}
				blockedLease, found := findControllerLease(persisted, devAgent1Role)
				if !found || blockedLease.Attempt != config.MaxFailedSessionRetries+1 || blockedLease.Terminal == nil {
					t.Fatalf("exhaustion altered affected lease: %#v", blockedLease)
				}
			case "retry after restart uses durable attempt":
				if len(launches) < 2 {
					t.Fatalf("durable retry and independent ready work did not both launch: %v", launches)
				}
				for _, task := range []board.Task{task1, task2} {
					count := 0
					for _, launch := range launches {
						if strings.Contains(launch, "/"+task.Key+"/") {
							count++
						}
					}
					if count != 1 {
						t.Fatalf("task %s launch count = %d, launches %v", task.Key, count, launches)
					}
				}
				retried, found := findControllerLease(persisted, devAgent1Role)
				if !found || retried.Attempt != 2 || retried.Terminal != nil {
					t.Fatalf("retry did not advance from durable Attempt: %#v", retried)
				}
			}
		})
	}
}

func TestIdleAuditSkippedWhenBoardUnchanged(t *testing.T) {
	withRetryTestState(t)
	oldFinished, oldActivity, oldHash := finished, lastTeamLeadActivity, lastAuditedBoardHash
	oldBacklog, oldTasks, oldArchive := config.BacklogFile, config.TasksFile, config.ArchiveFile
	t.Cleanup(func() {
		finished, lastTeamLeadActivity, lastAuditedBoardHash = oldFinished, oldActivity, oldHash
		config.BacklogFile, config.TasksFile, config.ArchiveFile = oldBacklog, oldTasks, oldArchive
	})
	dir := t.TempDir()
	config.BacklogFile = filepath.Join(dir, "BACKLOG.md")
	config.TasksFile = filepath.Join(dir, "TASKS.md")
	config.ArchiveFile = filepath.Join(dir, "ARCHIVE.md")
	_ = os.WriteFile(config.BacklogFile, []byte("# BACKLOG\n"), 0644)
	_ = os.WriteFile(config.TasksFile, []byte("# TASKS\n"), 0644)
	_ = os.WriteFile(config.ArchiveFile, []byte("# ARCHIVE\n"), 0644)

	finished = map[string]FinishedSession{}
	lastTeamLeadActivity = time.Now().Add(-2 * time.Hour)
	state := ControllerState{}

	// When lastAuditedBoardHash matches current boardStateHash:
	lastAuditedBoardHash = boardStateHash()
	if lastAuditedBoardHash == "" {
		t.Fatal("expected non-empty board hash")
	}

	action, ok := planTeamLeadAction(nil, map[string]bool{}, state, map[string]desiredSession{}, map[string]bool{})
	if ok {
		t.Fatalf("expected audit to be skipped when board is unchanged, got: %#v", action)
	}

	// But if board changes (e.g. human adds a task to backlog):
	lastTeamLeadActivity = time.Now().Add(-2 * time.Hour)
	_ = os.WriteFile(config.BacklogFile, []byte("# BACKLOG\n\n### New Task\n"), 0644)
	action, ok = planTeamLeadAction(nil, map[string]bool{}, state, map[string]desiredSession{}, map[string]bool{})
	if !ok || action.Task.Section != teamLeadIdleAuditSection {
		t.Fatalf("expected audit to trigger after board change, got: %#v", action)
	}
}
