package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/de-angelov/slopboss/internal/config"
)

func TestSetUpdatesOnlyStructuralMetadata(t *testing.T) {
	dir := t.TempDir()
	withBoardFiles(t, dir)
	writeBoard(t, config.BacklogFile, `# BACKLOG

## Backlog

### Structured task
Task ID: TASK-1
Owner: Unassigned
Status: Backlog

Keep this body exactly.
`)

	got, err := Set("TASK-1", Update{Owner: "Dev Agent 1", Status: "In Progress", Category: "AFK"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "Dev Agent 1" || got.Status != "In Progress" || got.Category != "AFK" {
		t.Fatalf("updated task = %#v", got)
	}
	data, err := os.ReadFile(config.BacklogFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Owner: Dev Agent 1", "Status: In Progress", "Category: AFK", "Keep this body exactly."} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("board lost %q:\n%s", want, data)
		}
	}
}

func TestSetMovesWholeTaskToExplicitLocation(t *testing.T) {
	dir := t.TempDir()
	withBoardFiles(t, dir)
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n\n### Move me\nTask ID: TASK-2\nStatus: Backlog\n\nBody survives.\n")
	writeBoard(t, config.TasksFile, "# TASKS\n\n## Dev Agent 1 In Progress\n")
	writeBoard(t, config.ArchiveFile, "# ARCHIVE\n\n## Done\n")

	got, err := Set("TASK-2", Update{File: "tasks", Section: "Dev Agent 1 In Progress", Owner: "Dev Agent 1", Status: "In Progress"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Section != "Dev Agent 1 In Progress" || got.Owner != "Dev Agent 1" {
		t.Fatalf("moved task = %#v", got)
	}
	backlog, _ := os.ReadFile(config.BacklogFile)
	tasks, _ := os.ReadFile(config.TasksFile)
	if strings.Contains(string(backlog), "TASK-2") {
		t.Fatalf("task remained in source:\n%s", backlog)
	}
	for _, want := range []string{"TASK-2", "Body survives.", "Status: In Progress"} {
		if !strings.Contains(string(tasks), want) {
			t.Fatalf("destination missing %q:\n%s", want, tasks)
		}
	}
}

func TestSetRejectsInvalidStructuralValues(t *testing.T) {
	dir := t.TempDir()
	withBoardFiles(t, dir)
	writeBoard(t, config.BacklogFile, "## Backlog\n\n### Task\nTask ID: TASK-3\n")
	if _, err := Set("TASK-3", Update{Status: "Ready"}); err == nil || !strings.Contains(err.Error(), "invalid status") {
		t.Fatalf("Set invalid status error = %v", err)
	}
	if _, err := Set("TASK-3", Update{Section: "Backlog"}); err == nil || !strings.Contains(err.Error(), "--file and --section") {
		t.Fatalf("Set ambiguous move error = %v", err)
	}
	if _, err := Set("TASK-3", Update{File: "tasks", Section: "Team Lead In Progress"}); err == nil || !strings.Contains(err.Error(), "Team Lead coordination") {
		t.Fatalf("Set Team Lead lane error = %v", err)
	}
}

func withBoardFiles(t *testing.T, root string) {
	t.Helper()
	oldRoot := config.RepoRoot
	config.SetRoot(root)
	t.Cleanup(func() { config.SetRoot(oldRoot) })
	for _, path := range []string{config.BacklogFile, config.TasksFile, config.ArchiveFile} {
		writeBoard(t, path, "# board\n")
	}
}

func writeBoard(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}
