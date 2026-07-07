package board

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/de-angelov/slopboss/internal/config"
)

func TestMergedIDFromSubject(t *testing.T) {
	cases := []struct {
		subject string
		want    string
	}{
		{"BOARD-03B persist board drag-and-drop moves with rollback on failure", "BOARD-03B"},
		{"TICKET-08 add ticket state-update action to board route", "TICKET-08"},
		{"COMMENT-02C add ticket details comments UI and add-comment form", "COMMENT-02C"},
		{"BOARD-PERF-01A profile board rendering at scale", "BOARD-PERF-01A"},
		{"TICKET-WF-01 align ticket details screen with wireframe hierarchy", "TICKET-WF-01"},
		{"  BOARD-04B wire board filter controls to filter query model", "BOARD-04B"},
		// No leading task-ID token -> no match.
		{"WIP before switching to agent/1/board-03b", ""},
		{"Merge pull request #42 from feature", ""},
		{"fix a typo in the readme", ""},
		{"", ""},
	}

	for _, tc := range cases {
		if got := mergedIDFromSubject(tc.subject); got != tc.want {
			t.Errorf("mergedIDFromSubject(%q) = %q, want %q", tc.subject, got, tc.want)
		}
	}
}

func TestMergedIDDoesNotMatchLongerSibling(t *testing.T) {
	// A commit for BOARD-03 must not be mistaken for BOARD-03B (or vice versa):
	// the regex greedily consumes the whole hyphenated token, so each subject
	// resolves to exactly one ID.
	if got := mergedIDFromSubject("BOARD-03 add client-side board drag interaction"); got != "BOARD-03" {
		t.Fatalf("mergedIDFromSubject for BOARD-03 = %q, want BOARD-03", got)
	}
	if got := mergedIDFromSubject("BOARD-03B persist drag moves"); got != "BOARD-03B" {
		t.Fatalf("mergedIDFromSubject for BOARD-03B = %q, want BOARD-03B", got)
	}
}

// TestCompletedSetUnionsMergedMainIDs verifies that a task merged to main but not
// present in ARCHIVE.md still counts as done — the core of the reconciliation fix.
func TestCompletedSetUnionsMergedMainIDs(t *testing.T) {
	oldArchive := config.ArchiveFile
	config.ArchiveFile = filepath.Join(t.TempDir(), "ARCHIVE.md")
	t.Cleanup(func() { config.ArchiveFile = oldArchive })
	if err := os.WriteFile(config.ArchiveFile, []byte("## Done\n\nTask ID: ARCHIVED-01\n"), 0644); err != nil {
		t.Fatal(err)
	}

	oldMerged := mergedMainIDs
	mergedMainIDs = map[string]bool{"MERGED-ONLY-02": true}
	t.Cleanup(func() { mergedMainIDs = oldMerged })

	done := CompletedSet()
	if !done["ARCHIVED-01"] {
		t.Error("expected archived task to be in completed set")
	}
	if !done["MERGED-ONLY-02"] {
		t.Error("expected merged-to-main task to be in completed set even though it is not in ARCHIVE.md")
	}
}

// TestFirstBacklogTaskUnblockedByMergedDependency exercises the end-to-end effect:
// a backlog task whose only dependency merged to main (but was never archived)
// becomes assignable instead of stalling forever.
func TestFirstBacklogTaskUnblockedByMergedDependency(t *testing.T) {
	oldArchive := config.ArchiveFile
	config.ArchiveFile = filepath.Join(t.TempDir(), "ARCHIVE.md")
	t.Cleanup(func() { config.ArchiveFile = oldArchive })
	if err := os.WriteFile(config.ArchiveFile, []byte("## Done\n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	oldMerged := mergedMainIDs
	mergedMainIDs = map[string]bool{"BOARD-03B": true}
	t.Cleanup(func() { mergedMainIDs = oldMerged })

	tasks := []Task{
		{Section: "Backlog", Title: "Board Wireframe", ID: "BOARD-WF-01", Status: "Backlog", Category: "AFK", Dependencies: "BOARD-03B"},
	}

	if got := FirstBacklogTask(tasks); got.ID != "BOARD-WF-01" {
		t.Fatalf("FirstBacklogTask = %q, want BOARD-WF-01 (dependency merged to main should unblock it)", got.ID)
	}
}
