package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
)

func TestAgentUIRenderPopulatesTable(t *testing.T) {
	oldCount := config.DevAgentCount
	config.DevAgentCount = 2
	t.Cleanup(func() { config.DevAgentCount = oldCount })

	ui := New()
	ui.SetTasks([]board.Task{
		{
			Section: "Dev Agent 1 In Progress",
			Title:   "Build auth",
			Owner:   "Dev Agent 1",
			Branch:  "agent/1/auth",
			Status:  "In Progress",
		},
	})
	ui.render() // populates widgets; no terminal needed

	// header row + team lead + 2 dev agents
	if got := ui.table.GetRowCount(); got != 4 {
		t.Fatalf("row count = %d, want 4 (header + TL + 2 dev agents)", got)
	}
	if got := ui.table.GetCell(0, 1).Text; got != "ROLE" {
		t.Fatalf("header ROLE column = %q", got)
	}

	found := false
	for r := 1; r < ui.table.GetRowCount(); r++ {
		if ui.table.GetCell(r, 1).Text != "Dev Agent 1" {
			continue
		}
		found = true
		if got := ui.table.GetCell(r, 2).Text; got != "In Progress" {
			t.Fatalf("Dev Agent 1 status = %q, want In Progress", got)
		}
		if got := ui.table.GetCell(r, 3).Text; got != "Build auth" {
			t.Fatalf("Dev Agent 1 task = %q, want Build auth", got)
		}
	}
	if !found {
		t.Fatal("Dev Agent 1 row not rendered")
	}
}

func TestStatusColor(t *testing.T) {
	cases := map[string]tcell.Color{
		"running":       tcell.ColorGreen,
		"completed":     tcell.ColorGreen,
		"failed":        tcell.ColorRed,
		"board error":   tcell.ColorRed,
		"usage-limited": tcell.ColorFuchsia,
		"In Progress":   tcell.ColorAqua,
		"idle":          tcell.ColorGray,
	}
	for status, want := range cases {
		if got := statusColor(status); got != want {
			t.Fatalf("statusColor(%q) = %v, want %v", status, got, want)
		}
	}
}
