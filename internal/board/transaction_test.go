package board

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/de-angelov/slopboss/internal/config"
)

func TestInterruptedMoveRecovery(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "intent only", true: "destination written"}[partial], func(t *testing.T) {
			withBoardFiles(t, t.TempDir())
			beforeBacklog := "# BACKLOG\n\n## Backlog\n\n### Task\nTask ID: T-1\nStatus: Backlog\n"
			beforeTasks := "# TASKS\n\n## Dev Agent 1 In Progress\n"
			afterBacklog := "# BACKLOG\n\n## Backlog\n"
			afterTasks := beforeTasks + "\n### Task\nTask ID: T-1\nStatus: In Progress\n"
			writeBoard(t, config.BacklogFile, beforeBacklog)
			writeBoard(t, config.TasksFile, beforeTasks)
			if err := os.MkdirAll(config.WorkspacesRoot, 0755); err != nil {
				t.Fatal(err)
			}
			tx := boardTransaction{Changes: []boardChange{{File: "tasks", Before: beforeTasks, After: afterTasks}, {File: "backlog", Before: beforeBacklog, After: afterBacklog}}}
			data, _ := json.Marshal(tx)
			if err := os.WriteFile(journalPath(), data, 0600); err != nil {
				t.Fatal(err)
			}
			if partial {
				writeBoard(t, config.TasksFile, afterTasks)
			}
			if _, err := ReadBoardTasks(); err != nil {
				t.Fatal(err)
			}
			backlog, _ := os.ReadFile(config.BacklogFile)
			tasks, _ := os.ReadFile(config.TasksFile)
			if string(backlog) != afterBacklog || string(tasks) != afterTasks {
				t.Fatalf("recovery left inconsistent files: %q %q", backlog, tasks)
			}
			if _, err := os.Stat(journalPath()); !os.IsNotExist(err) {
				t.Fatalf("journal not removed: %v", err)
			}
		})
	}
}

func TestInterruptedMoveRefusesConflictingExternalEdit(t *testing.T) {
	withBoardFiles(t, t.TempDir())
	writeBoard(t, config.TasksFile, "external edit")
	if err := os.MkdirAll(config.WorkspacesRoot, 0755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(boardTransaction{Changes: []boardChange{{File: "tasks", Before: "old", After: "new"}}})
	if err := os.WriteFile(journalPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBoardTasks(); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("expected conflict, got %v", err)
	}
	got, _ := os.ReadFile(config.TasksFile)
	if string(got) != "external edit" {
		t.Fatalf("overwrote external edit: %q", got)
	}
}
