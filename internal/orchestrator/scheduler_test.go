package orchestrator

import (
	"path/filepath"
	"testing"
	"time"

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

func withRetryTestState(t *testing.T) {
	t.Helper()

	oldLogFilePath := config.LogFilePath
	config.LogFilePath = filepath.Join(t.TempDir(), "orchestrator.log")
	failedSessionRetryCounts = map[string]int{}
	tokenUsageByRole = map[string]int{}
	codexPausedUntil = time.Time{}

	t.Cleanup(func() {
		config.LogFilePath = oldLogFilePath
		failedSessionRetryCounts = map[string]int{}
		tokenUsageByRole = map[string]int{}
		codexPausedUntil = time.Time{}
	})
}
