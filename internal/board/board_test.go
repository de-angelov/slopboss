package board

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/de-angelov/slopboss/internal/config"
)

func TestTaskFingerprintIgnoresBodyProgressNotes(t *testing.T) {
	base := Task{
		Section: "Dev Agent 1 In Progress",
		Title:   "Build auth",
		Owner:   config.DevAgent1Role,
		Branch:  "agent/1/auth",
		Status:  "In Progress",
		Body:    "original body",
	}
	updated := base
	updated.Body = "original body\n\nProgress:\n- Rechecked verification."

	if taskFingerprint(base) != taskFingerprint(updated) {
		t.Fatal("expected body-only task changes to keep the same fingerprint")
	}

	changedBranch := base
	changedBranch.Branch = "agent/1/other"
	if taskFingerprint(base) == taskFingerprint(changedBranch) {
		t.Fatal("expected branch changes to change the fingerprint")
	}
}

func TestReadTasksParsesCategory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "BACKLOG.md")
	content := "# BACKLOG\n\n## Backlog\n\n### Autonomous Cleanup\n\nOwner: Unassigned\nStatus: Backlog\nCategory: AFK\n\nbody\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tasks, err := ReadTasks(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("parsed %d tasks, want 1", len(tasks))
	}
	if tasks[0].Category != "AFK" {
		t.Fatalf("Category = %q, want AFK", tasks[0].Category)
	}
}

func TestAllDevAgentsBusy(t *testing.T) {
	old := config.DevAgentCount
	config.DevAgentCount = 2
	t.Cleanup(func() { config.DevAgentCount = old })

	desired := map[string]Task{config.DevAgentRole(1): {}}
	if AllDevAgentsBusy(desired) {
		t.Fatal("one of two agents free -> not all busy")
	}
	desired[config.DevAgentRole(2)] = Task{}
	if !AllDevAgentsBusy(desired) {
		t.Fatal("both agents busy -> all busy")
	}
}

func TestFirstBacklogTaskPrefersAFKOverHITL(t *testing.T) {
	tasks := []Task{
		{Section: "Backlog", Title: "Human Admin Task", Status: "Backlog", Category: "HITL"},
		{Section: "Backlog", Title: "Autonomous Cleanup", Status: "Backlog", Category: "AFK"},
		{Section: "Backlog", Title: "Another AFK", Status: "Backlog", Category: "AFK"},
	}

	got := FirstBacklogTask(tasks)
	if got.Title != "Autonomous Cleanup" {
		t.Fatalf("FirstBacklogTask = %q, want %q (first AFK, skipping head-of-line HITL)", got.Title, "Autonomous Cleanup")
	}
}

func TestFirstBacklogTaskFallsBackToFirstWhenNoAFK(t *testing.T) {
	tasks := []Task{
		{Section: "Backlog", Title: "Human Admin Task", Status: "Backlog", Category: "HITL"},
		{Section: "Backlog", Title: "Another Human Task", Status: "Backlog", Category: "HITL"},
	}

	got := FirstBacklogTask(tasks)
	if got.Title != "Human Admin Task" {
		t.Fatalf("FirstBacklogTask = %q, want %q (first pending when no AFK exists)", got.Title, "Human Admin Task")
	}
}

func TestFirstBacklogTaskIgnoresActiveAndCompleted(t *testing.T) {
	tasks := []Task{
		{Section: "Dev Agent 1 In Progress", Title: "Active AFK", Status: "In Progress", Category: "AFK"},
		{Section: "Backlog", Title: "Pending HITL", Status: "Backlog", Category: "HITL"},
	}

	got := FirstBacklogTask(tasks)
	if got.Title != "Pending HITL" {
		t.Fatalf("FirstBacklogTask = %q, want %q (only Backlog-section pending tasks are eligible)", got.Title, "Pending HITL")
	}
}

func TestFirstBacklogTaskSkipsUnmetDependencies(t *testing.T) {
	// Only DONE-DEP is completed; point ArchiveFile at a temp file holding it.
	oldArchive := config.ArchiveFile
	config.ArchiveFile = filepath.Join(t.TempDir(), "ARCHIVE.md")
	t.Cleanup(func() { config.ArchiveFile = oldArchive })
	if err := os.WriteFile(config.ArchiveFile, []byte("## Done\n\nTask ID: DONE-DEP\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tasks := []Task{
		// First AFK by board order, but blocked by a missing/unmet dependency.
		{Section: "Backlog", Title: "Blocked Cleanup", ID: "CLEAN-01", Status: "Backlog", Category: "AFK", Dependencies: "MISSING-DEP"},
		// Ready AFK: its only dependency is completed.
		{Section: "Backlog", Title: "Ready Backend", ID: "BACK-01", Status: "Backlog", Category: "AFK", Dependencies: "DONE-DEP"},
	}

	got := FirstBacklogTask(tasks)
	if got.ID != "BACK-01" {
		t.Fatalf("FirstBacklogTask = %q, want BACK-01 (skip the AFK task with an unmet dependency)", got.ID)
	}
}

func TestFirstBacklogTaskIdleWhenAllBlocked(t *testing.T) {
	oldArchive := config.ArchiveFile
	config.ArchiveFile = filepath.Join(t.TempDir(), "ARCHIVE.md")
	t.Cleanup(func() { config.ArchiveFile = oldArchive })
	if err := os.WriteFile(config.ArchiveFile, []byte("## Done\n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tasks := []Task{
		{Section: "Backlog", Title: "Blocked A", ID: "A", Status: "Backlog", Category: "AFK", Dependencies: "NOPE"},
		{Section: "Backlog", Title: "Blocked B", ID: "B", Status: "Backlog", Category: "HITL", Dependencies: "ALSO-NOPE"},
	}

	if got := FirstBacklogTask(tasks); got.Title != "" {
		t.Fatalf("FirstBacklogTask = %q, want empty (nothing assignable -> team lead idle)", got.Title)
	}
}
