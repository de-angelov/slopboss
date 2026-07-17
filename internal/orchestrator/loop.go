package orchestrator

import (
	"context"
	"fmt"
	"os/signal"
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
	p, err := provider.ByName(providerName)
	if err != nil {
		return err
	}
	sessionProvider = p

	config.DevAgentCount = config.DiscoverDevAgentCount()

	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	config.MustMkdir(config.LogsRoot)
	config.LogFilePath = config.NewRunLogFilePath(time.Now())
	logx.Event("orchestrator started")
	logx.Event("repo root: %s", config.RepoRoot)
	logx.Event("agent provider: %s", sessionProvider.Name())
	logx.Event("dev agents: %d", config.DevAgentCount)
	if config.DevAgentCount == 0 {
		logx.Event("no repo-agent-* workspaces found; run 'slopboss setup' first")
	}

	// Stop the TUI when the process is signalled (SIGTERM, or SIGINT that isn't
	// consumed by the terminal as Ctrl-C).
	go func() {
		<-ctx.Done()
		ui.Stop()
	}()

	// Reconcile loop: poll the board and converge sessions on PollInterval.
	go func() {
		ticker := time.NewTicker(config.PollInterval)
		defer ticker.Stop()
		for {
			tasks, err := board.ReadBoardTasks()
			if err != nil {
				logx.Event("failed to read board files: %v", err)
				ui.SetError(err)
			} else {
				// Refresh the merged-to-main task set before reconciling so work
				// that landed on main but was never archived still counts as done
				// for dependency resolution and cancellation this tick.
				board.RefreshMergedMainIDs()
				issues := board.RefreshArchiveCompletionIssues()
				for _, issue := range issues {
					logx.Event("archive verification failed: %s", issue.Error())
				}
				reconcile(tasks)
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
	// state (sessions starting/finishing, token counts) surface within
	// UIRefreshInterval instead of only at the poll period. Reads are cheap here
	// because ReadBoardTasks is mtime/size cached; session reconciliation stays on
	// PollInterval in the goroutine above.
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

	stop() // wind down the loop goroutines
	logx.Event("shutting down; stopping running sessions")
	shutdownSessions()
	logx.Event("orchestrator stopped")
	return runErr
}
