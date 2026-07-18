// Package orchestrator is the heart of slopboss: it polls the board, reconciles
// it against running agent sessions, and starts/stops backend sessions to
// converge on the board's desired state. It owns all mutable session state
// behind a single mutex and exposes a read-only Snapshot for the TUI so no other
// package reaches into that state directly.
package orchestrator

import (
	"context"
	"sync"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/provider"
)

// These alias the config role identities so the session/scheduler/groom code in
// this package (and its tests) reads naturally without qualifying every
// reference. devAgent1Role/devAgent2Role are used only by tests.
const (
	teamLeadRole  = config.TeamLeadRole
	devAgent1Role = config.DevAgent1Role
	devAgent2Role = config.DevAgent2Role
)

var (
	// sessionProvider is the agent backend used for every session. Defaults to
	// codex so tests and any pre-flag path behave as before; RunLoop overrides it
	// from the --provider flag at startup.
	sessionProvider provider.Provider = mustCodex()

	mu                       sync.Mutex
	running                  = map[string]RunningSession{}
	finished                 = map[string]FinishedSession{}
	failedSessionRetryCounts = map[string]int{}
	codexPausedUntil         time.Time

	// cancelPendingSince records when a running session first stopped matching
	// the board (its task left its lane, or its key changed). Cancellation is
	// debounced by CancelGracePeriod off this timestamp so a completion-time board
	// edit or a transient team-lead reshuffle does not kill a live session. Keyed
	// by role. Guarded by mu.
	cancelPendingSince = map[string]time.Time{}
)

func mustCodex() provider.Provider {
	p, _ := provider.ByName("codex")
	return p
}

type RunningSession struct {
	Role    string
	Task    string
	TaskID  string
	TaskKey string
	Branch  string
	Cancel  context.CancelFunc
}

type FinishedSession struct {
	Role       string
	Task       string
	TaskKey    string
	Branch     string
	Outcome    SessionOutcome
	FinishedAt time.Time
}

type SessionOutcome string

const (
	sessionCompleted    SessionOutcome = "completed"
	sessionFailed       SessionOutcome = "failed"
	sessionCancelled    SessionOutcome = "cancelled"
	sessionUsageLimited SessionOutcome = "usage-limited"
	sessionUnknown      SessionOutcome = "unknown"
)

// StateSnapshot is an immutable copy of the orchestrator's session state, taken
// under mu, for the UI to render without touching the live maps or the mutex.
type StateSnapshot struct {
	Running      map[string]RunningSession
	Finished     map[string]FinishedSession
	ProviderName string
}

// Snapshot returns a consistent copy of the current session state.
func Snapshot() StateSnapshot {
	mu.Lock()
	defer mu.Unlock()

	snap := StateSnapshot{
		Running:  make(map[string]RunningSession, len(running)),
		Finished: make(map[string]FinishedSession, len(finished)),
	}
	for k, v := range running {
		snap.Running[k] = v
	}
	for k, v := range finished {
		snap.Finished[k] = v
	}
	if sessionProvider != nil {
		snap.ProviderName = sessionProvider.Name()
	}
	return snap
}

// UI is the subset of the terminal dashboard the run loop drives. The tui package
// implements it; keeping it an interface here means orchestrator never imports
// tui, so the dependency runs one way (tui -> orchestrator).
type UI interface {
	SetTasks([]board.Task)
	SetError(error)
	Refresh()
	Run() error
	Stop()
}
