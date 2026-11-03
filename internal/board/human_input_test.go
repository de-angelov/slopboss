package board

import (
	"os"
	"strings"
	"testing"

	"github.com/de-angelov/slopboss/internal/config"
)

func TestClearCompletedHumanInputPreservesOtherEntries(t *testing.T) {
	withBoardFiles(t, t.TempDir())
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Awaiting Human Input\n\n- H-1 — old request\n  continuation text\n\n- H-2 — keep this\n\n## Backlog\n")
	writeBoard(t, config.ArchiveFile, "# ARCHIVE\n\n## Done\n\n### Resolved\nTask ID: H-1\nOwner: Unassigned\nStatus: Done\n")
	removed, err := ClearCompletedHumanInput("H-1")
	if err != nil || !removed {
		t.Fatalf("clear: %v %v", removed, err)
	}
	data, _ := os.ReadFile(config.BacklogFile)
	if strings.Contains(string(data), "old request") || strings.Contains(string(data), "continuation text") || !strings.Contains(string(data), "H-2 — keep this") {
		t.Fatalf("unexpected backlog: %s", data)
	}
	if removed, err = ClearCompletedHumanInput("H-1"); err != nil || removed {
		t.Fatalf("repeat: %v %v", removed, err)
	}
}
func TestClearHumanInputRejectsUnfinishedTask(t *testing.T) {
	withBoardFiles(t, t.TempDir())
	original := "# BACKLOG\n\n## Awaiting Human Input\n\n- H-1 — answer\n\n## Backlog\n\n### Open\nTask ID: H-1\nOwner: Unassigned\nStatus: Backlog\n"
	writeBoard(t, config.BacklogFile, original)
	if _, err := ClearCompletedHumanInput("H-1"); err == nil {
		t.Fatal("unfinished task was cleared")
	}
	data, _ := os.ReadFile(config.BacklogFile)
	if string(data) != original {
		t.Fatal("failed operation changed board")
	}
}
