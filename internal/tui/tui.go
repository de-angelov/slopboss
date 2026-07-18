// Package tui renders slopboss's live terminal dashboard: a header, a table of
// agent lanes, and a footer tailing the log. It reads session state through
// orchestrator.Snapshot and implements orchestrator.UI, so the dependency points
// one way (tui -> orchestrator).
package tui

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/logx"
	"github.com/de-angelov/slopboss/internal/orchestrator"
)

// AgentUI is the live tview dashboard: a header panel, a bordered agents table,
// and a footer showing the latest log line. It is updated from background
// goroutines via Refresh(), which marshals the redraw onto tview's event loop.
type AgentUI struct {
	app    *tview.Application
	table  *tview.Table
	header *tview.TextView
	footer *tview.TextView

	mu      sync.Mutex
	tasks   []board.Task
	readErr error
}

// New constructs the dashboard. The returned *AgentUI satisfies orchestrator.UI.
func New() *AgentUI {
	u := &AgentUI{
		app:    tview.NewApplication(),
		table:  tview.NewTable().SetFixed(1, 0).SetSelectable(false, false),
		header: tview.NewTextView().SetDynamicColors(true),
		footer: tview.NewTextView().SetDynamicColors(true),
	}
	u.table.SetBorder(true).SetTitle(" Agents ")

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(u.header, 2, 0, false).
		AddItem(u.table, 0, 1, true).
		AddItem(u.footer, 2, 0, false)

	u.app.SetRoot(layout, true)
	u.app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyCtrlC || ev.Rune() == 'q' {
			u.app.Stop()
			return nil
		}
		return ev
	})
	return u
}

func (u *AgentUI) SetTasks(tasks []board.Task) {
	u.mu.Lock()
	u.tasks = tasks
	u.readErr = nil
	u.mu.Unlock()
}

func (u *AgentUI) SetError(err error) {
	u.mu.Lock()
	u.readErr = err
	u.mu.Unlock()
}

// Refresh redraws the dashboard on tview's event loop. Safe to call from any
// goroutine; it blocks briefly until the draw is processed.
func (u *AgentUI) Refresh() {
	u.app.QueueUpdateDraw(func() { u.render() })
}

// Run starts the tview event loop and blocks until the UI stops.
func (u *AgentUI) Run() error { return u.app.Run() }

// Stop halts the tview event loop.
func (u *AgentUI) Stop() { u.app.Stop() }

func (u *AgentUI) render() {
	u.mu.Lock()
	tasks := u.tasks
	readErr := u.readErr
	u.mu.Unlock()

	snap := orchestrator.Snapshot()
	rows := buildRows(tasks, snap)

	head := fmt.Sprintf("[::b]slopboss[::-]  board: %s   provider: %s   agents: %d   %s",
		filepath.Base(config.RepoRoot), snap.ProviderName, config.DevAgentCount, time.Now().Format("15:04:05"))
	if readErr != nil {
		head += fmt.Sprintf("   [red]board error: %s[-]", tview.Escape(readErr.Error()))
	}
	u.header.SetText(head)

	u.table.Clear()
	for c, title := range []string{"", "ROLE", "STATUS", "TASK", "BRANCH"} {
		u.table.SetCell(0, c, tview.NewTableCell(title).
			SetTextColor(tcell.ColorYellow).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false))
	}
	for i, row := range rows {
		r := i + 1
		u.table.SetCell(r, 0, tview.NewTableCell(row.marker).SetTextColor(tcell.ColorGreen))
		u.table.SetCell(r, 1, tview.NewTableCell(row.role))
		u.table.SetCell(r, 2, tview.NewTableCell(row.status).SetTextColor(statusColor(row.status)))
		u.table.SetCell(r, 3, tview.NewTableCell(tview.Escape(truncate(row.task, 48))).SetExpansion(1))
		u.table.SetCell(r, 4, tview.NewTableCell(tview.Escape(truncate(row.branch, 36))))
	}

	u.footer.SetText(fmt.Sprintf("latest: %s\n[gray]press q or Ctrl-C to stop[-]", tview.Escape(logx.LatestLogLine())))
}

// statusColor maps a row status to a tcell color, preserving the semantics of
// the previous ANSI renderer.
func statusColor(status string) tcell.Color {
	switch status {
	case "running", "completed":
		return tcell.ColorGreen
	case "failed", "board error":
		return tcell.ColorRed
	case "usage-limited":
		return tcell.ColorFuchsia
	case "In Progress":
		return tcell.ColorAqua
	case "Blocked", "backlog pending":
		return tcell.ColorYellow
	case "idle", "waiting for lane", "Backlog":
		return tcell.ColorGray
	default:
		return tcell.ColorWhite
	}
}

// Ensure *AgentUI satisfies the orchestrator UI contract at compile time.
var _ orchestrator.UI = (*AgentUI)(nil)
