package orchestrator

import (
	"context"
	"fmt"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/logx"
	"github.com/de-angelov/slopboss/internal/provider"
)

// RunLoop runs the orchestrator loop: poll the task board, reconcile it against
// running agent sessions, and start/stop backend sessions to match. It drives the
// supplied UI and runs until interrupted (SIGINT/SIGTERM), then cancels in-flight
// sessions and exits.
func RunLoop(parent context.Context, providerName string, ui UI) error {
	return RunLoopWithMode(parent, providerName, "planner", ui)
}

// RunLoopWithMode selects the Team Lead coordination policy for this run.
func RunLoopWithMode(parent context.Context, providerName, mode string, ui UI) error {
	if mode != "planner" && mode != "agent" {
		return fmt.Errorf("invalid team lead mode %q (use planner or agent)", mode)
	}
	unlock, err := lockController()
	if err != nil {
		return err
	}
	defer unlock()

	p, err := provider.ByName(providerName)
	if err != nil {
		return err
	}
	sessionProvider = p
	controllerStateStore = NewControllerStateStore(filepath.Join(config.WorkspacesRoot, "controller-state.json"))

	config.DevAgentCount = config.DiscoverDevAgentCount()

	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	config.MustMkdir(config.LogsRoot)
	config.LogFilePath = config.NewRunLogFilePath(time.Now())
	logx.Event("orchestrator started")
	logx.Event("repo root: %s", config.RepoRoot)
	logx.Event("agent provider: %s", sessionProvider.Name())
	logx.Event("team lead mode: %s", mode)
	logx.Event("dev agents: %d", config.DevAgentCount)
	if config.DevAgentCount == 0 {
		logx.Event("no repo-agent-* workspaces found; run 'slopboss setup' first")
	}

	// Stop the TUI when the process is signalled (SIGTERM, or SIGINT that isn't
	// consumed by the terminal as Ctrl-C).
	uiExited := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-uiExited:
			return
		case <-ctx.Done():
			select {
			case <-uiExited:
				return
			default:
			}
			ui.Stop()
		}
	}()

	// Reconcile loop: poll the board and converge sessions on PollInterval.
	reconcileDone := make(chan struct{})
	go func() {
		defer close(reconcileDone)
		ticker := time.NewTicker(config.PollInterval)
		defer ticker.Stop()
		for {
			tasks, err := board.ReadBoardTasks()
			if err != nil {
				logx.Event("failed to read board files: %v", err)
				ui.SetError(err)
			} else {
				if err := board.RefreshMainBranch(); err != nil {
					logx.Event("failed to refresh origin/%s before merge reconciliation: %v", config.BaseBranch, err)
					ui.SetError(err)
				}
				// Refresh the merged-to-main task set before reconciling so work
				// that landed on main but was never archived still counts as done
				// for dependency resolution and cancellation this tick.
				board.RefreshMergedMainIDs()
				issues := board.RefreshArchiveCompletionIssues()
				for _, issue := range issues {
					logx.Event("archive verification failed: %s", issue.Error())
				}
				reconcileWithMode(tasks, mode)
				ui.SetTasks(tasks)
				if len(issues) > 0 {
					ui.SetError(fmt.Errorf("archive verification failed: %s", issues[0].Error()))
				}
			}
			ui.Refresh()

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	// Fast display refresh: re-read the board and redraw on UIRefreshInterval so
	// board changes (an agent picking up a task, a status flip) and asynchronous
	// session state surface within UIRefreshInterval instead of only at the poll
	// period. Reads are cheap here because ReadBoardTasks is mtime/size cached;
	// session reconciliation stays on PollInterval in the goroutine above.
	go func() {
		ticker := time.NewTicker(config.UIRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if tasks, err := board.ReadBoardTasks(); err != nil {
					ui.SetError(err)
				} else {
					ui.SetTasks(tasks)
				}
				ui.Refresh()
			}
		}
	}()

	runErr := ui.Run()
	close(uiExited)

	stop() // wind down the loop goroutines
	<-watcherDone
	<-reconcileDone
	logx.Event("shutting down; stopping running sessions")
	shutdownSessions()
	logx.Event("orchestrator stopped")
	return runErr
}
