package board

import (
	"strings"
	"testing"

	"github.com/de-angelov/slopboss/internal/config"
)

func TestTaskBodyHeadingsAndFencedShellAreNotBoardSections(t *testing.T) {
	withBoardFiles(t, t.TempDir())
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n\n### One\nTask ID: A\nOwner: Unassigned\nStatus: Backlog\n\n## Result\nFirst result.\n\n## Verification\n```sh\n# shell comment\necho ok\n```\n\n### Two\nTask ID: B\nOwner: Unassigned\nStatus: Backlog\n\n## Result\nSecond result.\n")
	if _, err := Set("A", Update{Category: "AFK"}); err != nil {
		t.Fatalf("valid task body rejected: %v", err)
	}
	tasks, err := ReadBoardTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != "A" || tasks[1].ID != "B" || !strings.Contains(tasks[0].Body, "# shell comment") || tasks[1].Section != "Backlog" {
		t.Fatalf("task bodies misparsed: %#v", tasks)
	}
}

func TestCreateAcceptsMarkdownTaskBodyHeadings(t *testing.T) {
	withBoardFiles(t, t.TempDir())
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n")
	body := "## Objective\nDo work.\n## Scope\nThis.\n## Out of Scope\nThat.\n## Acceptance Criteria\nWorks.\n## Verification\nRun test."
	if _, err := CreateBacklogTask(Task{ID: "A", Title: "Test", Category: "AFK", Body: body}); err != nil {
		t.Fatal(err)
	}
}
