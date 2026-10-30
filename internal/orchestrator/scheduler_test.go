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

func withRetryTestState(t *testing.T) {
	t.Helper()

	oldLogFilePath := config.LogFilePath
	config.LogFilePath = filepath.Join(t.TempDir(), "orchestrator.log")
	failedSessionRetryCounts = map[string]int{}
	circuitBreakers = map[string]*circuitBreaker{}
	codexPausedUntil = time.Time{}

	t.Cleanup(func() {
		config.LogFilePath = oldLogFilePath
		failedSessionRetryCounts = map[string]int{}
		circuitBreakers = map[string]*circuitBreaker{}
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
