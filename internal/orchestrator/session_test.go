package orchestrator

import (
	"testing"
)

func TestShutdownSessionsCancelsAndDrains(t *testing.T) {
	withRetryTestState(t)

	mu.Lock()
	running = map[string]RunningSession{}
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		running = map[string]RunningSession{}
		mu.Unlock()
	})

	cancelled := make(chan string, 2)
	for _, role := range []string{devAgent1Role, devAgent2Role} {
		r := role
		mu.Lock()
		running[r] = RunningSession{
			Role: r,
			// Mimic a session goroutine: on cancel it removes itself from
			// `running`, which is what lets shutdownSessions observe the drain.
			Cancel: func() {
				cancelled <- r
				mu.Lock()
				delete(running, r)
				mu.Unlock()
			},
		}
		mu.Unlock()
	}

	shutdownSessions()

	mu.Lock()
	remaining := len(running)
	mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected all sessions drained, %d remain", remaining)
	}
	if got := len(cancelled); got != 2 {
		t.Fatalf("expected 2 sessions cancelled, got %d", got)
	}
}

func TestRecordCodexTokenUsageAccumulatesByRole(t *testing.T) {
	withRetryTestState(t)

	recordCodexTokenUsage(devAgent1Role, 100)
	recordCodexTokenUsage(devAgent1Role, 25)
	recordCodexTokenUsage(devAgent2Role, 7)

	mu.Lock()
	defer mu.Unlock()

	if tokenUsageByRole[devAgent1Role] != 125 {
		t.Fatalf("Dev Agent 1 tokens = %d, want 125", tokenUsageByRole[devAgent1Role])
	}
	if tokenUsageByRole[devAgent2Role] != 7 {
		t.Fatalf("Dev Agent 2 tokens = %d, want 7", tokenUsageByRole[devAgent2Role])
	}
}
