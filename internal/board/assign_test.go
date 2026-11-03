package board

import (
	"strings"
	"testing"

	"github.com/de-angelov/slopboss/internal/config"
)

func TestAssignIsAtomicAndIdempotent(t *testing.T) {
	withBoardFiles(t, t.TempDir())
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n\n### First\nTask ID: A\nCategory: AFK\nOwner: Unassigned\nStatus: Backlog\nDependencies: none\n\n### Second\nTask ID: B\nCategory: AFK\nOwner: Unassigned\nStatus: Backlog\nDependencies: none\n")
	first, err := Assign("A", config.DevAgentRole(1))
	if err != nil {
		t.Fatal(err)
	}
	if first.Owner != config.DevAgentRole(1) || first.Status != "In Progress" {
		t.Fatalf("assignment = %#v", first)
	}
	if _, err = Assign("A", config.DevAgentRole(1)); err != nil {
		t.Fatalf("repeat failed: %v", err)
	}
	if _, err = Assign("B", config.DevAgentRole(1)); err == nil || !strings.Contains(err.Error(), "active work") {
		t.Fatalf("occupied worker accepted: %v", err)
	}
}

func TestAssignRejectsHumanWorkAndUnfinishedDependencies(t *testing.T) {
	withBoardFiles(t, t.TempDir())
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n\n### Human\nTask ID: H\nCategory: HITL\nOwner: Unassigned\nStatus: Backlog\n\n### Dependent\nTask ID: D\nCategory: AFK\nOwner: Unassigned\nStatus: Backlog\nDependencies: MISSING\n")
	for _, id := range []string{"H", "D"} {
		if _, err := Assign(id, config.DevAgentRole(1)); err == nil {
			t.Errorf("assigned %s", id)
		}
	}
}
