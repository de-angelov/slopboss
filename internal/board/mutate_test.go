package board

import (
	"os"
	"path/filepath"
	"strconv"
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

	got, err := Set("TASK-1", Update{File: "tasks", Section: "Dev Agent 1 In Progress", Owner: "Dev Agent 1", Status: "In Progress", Category: "AFK"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "Dev Agent 1" || got.Status != "In Progress" || got.Category != "AFK" {
		t.Fatalf("updated task = %#v", got)
	}
	data, err := os.ReadFile(config.TasksFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Owner: Dev Agent 1", "Status: In Progress", "Category: AFK", "Keep this body exactly."} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("board lost %q:\n%s", want, data)
		}
	}
}

func TestSetSelectsDuplicateIDBySourceSection(t *testing.T) {
	dir := t.TempDir()
	withBoardFiles(t, dir)
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n\n### Backlog copy\nTask ID: DUP-1\nOwner: Unassigned\nStatus: Backlog\n\nBacklog body.\n")
	writeBoard(t, config.TasksFile, "# TASKS\n\n## Dev Agent 2 In Progress\n\n### Active copy\nTask ID: DUP-1\nOwner: Dev Agent 2\nStatus: In Progress\n\nActive body.\n")
	if _, err := Set("DUP-1", Update{Owner: "Dev Agent 1"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous Set error = %v", err)
	}
	got, err := Set("DUP-1", Update{SourceSection: "Dev Agent 2 In Progress", Branch: "agent/2/dog-02"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Section != "Dev Agent 2 In Progress" || got.Branch != "agent/2/dog-02" || got.Owner != "Dev Agent 2" {
		t.Fatalf("selected task = %#v", got)
	}
	backlog, _ := os.ReadFile(config.BacklogFile)
	tasks, _ := os.ReadFile(config.TasksFile)
	if !strings.Contains(string(backlog), "Backlog body.") || strings.Contains(string(backlog), "agent/2/dog-02") || !strings.Contains(string(tasks), "Active body.") || !strings.Contains(string(tasks), "agent/2/dog-02") {
		t.Fatalf("wrong duplicate was changed\nbacklog:\n%s\ntasks:\n%s", backlog, tasks)
	}
}

func TestSetValidatesSelectedDuplicate(t *testing.T) {
	withBoardFiles(t, t.TempDir())
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n\n### Backlog copy\nTask ID: DUP-1\nOwner: Unassigned\nStatus: Backlog\n")
	writeBoard(t, config.TasksFile, "# TASKS\n\n## Dev Agent 2 In Progress\n\n### Active copy\nTask ID: DUP-1\nOwner: Dev Agent 2\nStatus: In Progress\n\n## Blocked Tasks\n")
	got, err := Set("DUP-1", Update{SourceSection: "Dev Agent 2 In Progress", Owner: "Dev Agent 2"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Section != "Dev Agent 2 In Progress" || got.Status != "In Progress" {
		t.Fatalf("updated task = %#v", got)
	}
}

func TestCreateRejectsUnreliableTaskLookup(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
	}{
		{"duplicate ID", func(t *testing.T) {
			record := "### Existing\nTask ID: DUP-1\nOwner: Unassigned\nStatus: Backlog\n"
			writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n\n"+record)
			writeBoard(t, config.TasksFile, "# TASKS\n\n## Blocked Tasks\n\n"+record)
		}},
		{"missing board file", func(t *testing.T) {
			if err := os.Remove(config.ArchiveFile); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withBoardFiles(t, t.TempDir())
			tc.setup(t)
			before, err := os.ReadFile(config.BacklogFile)
			if err != nil {
				t.Fatal(err)
			}
			_, err = CreateBacklogTask(Task{ID: "DUP-1", Title: "New", Category: "AFK", Body: "Objective: x\nScope: x\nOut of Scope: x\nAcceptance Criteria: x\nVerification: x"})
			if err == nil {
				t.Fatal("expected lookup error")
			}
			after, err := os.ReadFile(config.BacklogFile)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("backlog changed after lookup error")
			}
		})
	}
}

func TestReconcileDuplicatePreservesSelectedIdenticalRecord(t *testing.T) {
	dir := t.TempDir()
	withBoardFiles(t, dir)
	record := "### Shared task\nTask ID: DUP-SAFE\nOwner: Unassigned\nStatus: Backlog\n\nThe prose survives.\n"
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n\n"+record)
	writeBoard(t, config.TasksFile, "# TASKS\n\n## Blocked Tasks\n\n"+record)
	if _, err := ReconcileDuplicate("DUP-SAFE", "Blocked Tasks"); err != nil {
		t.Fatal(err)
	}
	backlog, _ := os.ReadFile(config.BacklogFile)
	tasks, _ := os.ReadFile(config.TasksFile)
	if strings.Contains(string(backlog), "DUP-SAFE") || strings.Count(string(tasks), "DUP-SAFE") != 1 || !strings.Contains(string(tasks), "The prose survives.") {
		t.Fatalf("reconciliation did not preserve selected record\nbacklog:\n%s\ntasks:\n%s", backlog, tasks)
	}
}

func TestReconcileDuplicateRejectsDifferentContentWithoutChanges(t *testing.T) {
	dir := t.TempDir()
	withBoardFiles(t, dir)
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n\n### Copy\nTask ID: DUP-CONFLICT\nOwner: Unassigned\nStatus: Backlog\n\nOriginal prose.\n")
	writeBoard(t, config.TasksFile, "# TASKS\n\n## Blocked Tasks\n\n### Copy\nTask ID: DUP-CONFLICT\nOwner: Unassigned\nStatus: Backlog\n\nDifferent prose.\n")
	backlog, _ := os.ReadFile(config.BacklogFile)
	tasks, _ := os.ReadFile(config.TasksFile)
	if _, err := ReconcileDuplicate("DUP-CONFLICT", "Blocked Tasks"); err == nil || !strings.Contains(err.Error(), "conflicting duplicate") {
		t.Fatalf("expected safe conflict error, got %v", err)
	}
	afterBacklog, _ := os.ReadFile(config.BacklogFile)
	afterTasks, _ := os.ReadFile(config.TasksFile)
	if string(backlog) != string(afterBacklog) || string(tasks) != string(afterTasks) {
		t.Fatal("documents changed after conflicting reconciliation")
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
	if strings.Count(string(backlog)+string(tasks), "TASK-2") != 1 {
		t.Fatal("cross-file move did not preserve exactly one task")
	}
	if _, err := ValidateLifecycle(got, "tasks"); err != nil {
		t.Fatalf("moved task has invalid lifecycle placement: %v", err)
	}
}

func TestSetArchivesWithRepeatedDoneSections(t *testing.T) {
	dir := t.TempDir()
	withBoardFiles(t, dir)
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n\n### Archive me\nTask ID: TASK-repeat-done\nOwner: Unassigned\nStatus: Backlog\n\nNew record survives.\n")
	archive := "# ARCHIVE\n\n## Done\n\n### Earlier record\nTask ID: TASK-earlier\nOwner: Dev Agent 2\nStatus: Done\n\nEarlier body.\n\n# ARCHIVE snapshot\n\n## Done\n\n### Another record\nTask ID: TASK-another\nOwner: Dev Agent 1\nStatus: Done\n\nAnother body.\n"
	writeBoard(t, config.ArchiveFile, archive)

	got, err := Set("TASK-repeat-done", Update{File: "archive", Section: "Done", Owner: "Dev Agent 1", Status: "Done"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Section != "Done" || got.Status != "Done" {
		t.Fatalf("archived task metadata = %#v", got)
	}
	gotArchive, err := os.ReadFile(config.ArchiveFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(gotArchive)
	for _, want := range []string{"TASK-earlier", "Earlier body.", "TASK-another", "Another body.", "TASK-repeat-done", "New record survives.", "Status: Done"} {
		if !strings.Contains(text, want) {
			t.Fatalf("archive lost %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "Task ID: TASK-repeat-done") != 1 {
		t.Fatalf("new archive record was not inserted exactly once:\n%s", text)
	}
	if _, err := ValidateLifecycle(got, "archive"); err != nil {
		t.Fatalf("archived task has invalid lifecycle: %v", err)
	}
}

func TestSetArchiveCollapsesIdenticalRecordsAndRejectsConflicts(t *testing.T) {
	for _, conflicting := range []bool{false, true} {
		t.Run(map[bool]string{false: "identical", true: "conflicting"}[conflicting], func(t *testing.T) {
			dir := t.TempDir()
			withBoardFiles(t, dir)
			source := "# BACKLOG\n\n## Backlog\n\n### Archive me\nTask ID: TASK-new-archive\nOwner: Unassigned\nStatus: Backlog\n\nNew body.\n"
			duplicate := "### Historical\nTask ID: LOOP-03\nOwner: Dev Agent 1\nStatus: Done\n\nHistorical body.\n"
			archive := "# ARCHIVE\n\n## Done\n\n" + duplicate + "\n"
			if conflicting {
				archive += strings.Replace(duplicate, "Historical body.", "Different body.", 1)
			} else {
				archive += duplicate
			}
			writeBoard(t, config.BacklogFile, source)
			writeBoard(t, config.ArchiveFile, archive)

			_, err := Set("TASK-new-archive", Update{File: "archive", Section: "Done", Owner: "Dev Agent 2", Status: "Done"})
			got, _ := os.ReadFile(config.ArchiveFile)
			if conflicting {
				if err == nil || !strings.Contains(err.Error(), "conflicting duplicate task ID") {
					t.Fatalf("Set error = %v, want conflicting duplicate rejection", err)
				}
				if string(got) != archive {
					t.Fatalf("archive changed on rejection:\n%s", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(got), "Task ID: LOOP-03") != 1 || !strings.Contains(string(got), "TASK-new-archive") {
				t.Fatalf("archive was not deduplicated and moved:\n%s", got)
			}
		})
	}
}

func TestSetArchiveCollapsesRecordsAcrossRepeatedArchiveSnapshotHeadings(t *testing.T) {
	dir := t.TempDir()
	withBoardFiles(t, dir)
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n\n### Archive me\nTask ID: TASK-snapshot-archive\nOwner: Unassigned\nStatus: Backlog\n\nNew body.\n")
	duplicate := "### Historical\nTask ID: OPS-02\nOwner: Dev Agent 2\nStatus: Done\n\nObjective:\nPreserve this historical body.\n"
	archive := "# ARCHIVE\n\n## Done\n\n" + duplicate + "\n# ARCHIVE\n\n## Done\n\n" + duplicate
	writeBoard(t, config.ArchiveFile, archive)

	_, err := Set("TASK-snapshot-archive", Update{File: "archive", Section: "Done", Owner: "Dev Agent 2", Status: "Done"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(config.ArchiveFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(got), "Task ID: OPS-02") != 1 {
		t.Fatalf("identical snapshot record was not collapsed:\n%s", got)
	}
	if !strings.Contains(string(got), "Preserve this historical body.") || !strings.Contains(string(got), "TASK-snapshot-archive") {
		t.Fatalf("archive did not retain history and new task:\n%s", got)
	}
}

func TestSetArchivesWithFiveTopLevelHeadings(t *testing.T) {
	dir := t.TempDir()
	withBoardFiles(t, dir)
	writeBoard(t, config.BacklogFile, "# BACKLOG\n\n## Backlog\n\n### Archive me\nTask ID: TASK-five-headings\nOwner: Unassigned\nStatus: Backlog\n\nNew record survives.\n")

	var archive strings.Builder
	for i := 1; i <= 5; i++ {
		archive.WriteString("# ARCHIVE snapshot\n\n## Done\n\n### Historical record\n")
		archive.WriteString("Task ID: HIST-")
		archive.WriteString(strconv.Itoa(i))
		archive.WriteString("\nOwner: Dev Agent 1\nStatus: Done\n\nHistorical body ")
		archive.WriteString(strconv.Itoa(i))
		archive.WriteString(".\n\n")
	}
	writeBoard(t, config.ArchiveFile, archive.String())

	_, err := Set("TASK-five-headings", Update{File: "archive", Section: "Done", Owner: "Dev Agent 2", Status: "Done"})
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(config.ArchiveFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for i := 1; i <= 5; i++ {
		for _, want := range []string{"Task ID: HIST-" + strconv.Itoa(i), "Historical body " + strconv.Itoa(i) + "."} {
			if !strings.Contains(text, want) {
				t.Fatalf("archive lost %q:\n%s", want, text)
			}
		}
	}
	if strings.Count(text, "Task ID: TASK-five-headings") != 1 {
		t.Fatalf("moved task should appear exactly once:\n%s", text)
	}
}

func TestSetMovesTaskWithinDocument(t *testing.T) {
	dir := t.TempDir()
	withBoardFiles(t, dir)
	writeBoard(t, config.TasksFile, "# TASKS\n\n## Dev Agent 1 In Progress\n\n### Move me\nTask ID: TASK-same-file\nOwner: Dev Agent 1\nStatus: In Progress\n\nBody.\n\n## Blocked Tasks\n")

	got, err := Set("TASK-same-file", Update{File: "tasks", Section: "Blocked Tasks", Owner: "Unassigned", Status: "Blocked"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Section != "Blocked Tasks" || got.Status != "Blocked" || got.Owner != "Unassigned" {
		t.Fatalf("same-file move = %#v", got)
	}
	data, _ := os.ReadFile(config.TasksFile)
	if strings.Count(string(data), "TASK-same-file") != 1 {
		t.Fatal("same-file move did not preserve exactly one task")
	}
	if _, err := ValidateLifecycle(got, "tasks"); err != nil {
		t.Fatalf("same-file move has invalid lifecycle placement: %v", err)
	}
}

func TestSetRejectsCrossFileMoveCreatingDuplicateHeading(t *testing.T) {
	for _, duplicate := range []string{"# TASKS", "## Dev Agent 1 In Progress"} {
		t.Run(duplicate, func(t *testing.T) {
			dir := t.TempDir()
			withBoardFiles(t, dir)
			source := "# BACKLOG\n\n## Backlog\n\n### Move me\nTask ID: TASK-duplicate\nOwner: Unassigned\nStatus: Backlog\n\nBody.\n"
			target := "# TASKS\n\n## Dev Agent 1 In Progress\n"
			if duplicate == "# TASKS" {
				target = "# TASKS\n\n# TASKS\n\n## Dev Agent 1 In Progress\n"
			}
			if duplicate == "## Dev Agent 1 In Progress" {
				target += "\n## Dev Agent 1 In Progress\n"
			}
			writeBoard(t, config.BacklogFile, source)
			writeBoard(t, config.TasksFile, target)
			_, err := Set("TASK-duplicate", Update{File: "tasks", Section: "Dev Agent 1 In Progress", Owner: "Dev Agent 1", Status: "In Progress"})
			if err == nil {
				t.Fatal("Set succeeded despite duplicate heading")
			}
			gotSource, _ := os.ReadFile(config.BacklogFile)
			gotTarget, _ := os.ReadFile(config.TasksFile)
			if string(gotSource) != source || string(gotTarget) != target {
				t.Fatalf("documents changed after rejection\nsource: %q\ntarget: %q", gotSource, gotTarget)
			}
			if strings.Count(string(gotSource)+string(gotTarget), "TASK-duplicate") != 1 {
				t.Fatal("task was lost or duplicated")
			}
		})
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

func TestSetMetadataUpdateRejectsMalformedCandidateWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	withBoardFiles(t, dir)
	original := "# BACKLOG\n\n## Backlog\n\n### Incomplete task\nTask ID: TASK-malformed-update\nStatus: Backlog\n"
	writeBoard(t, config.BacklogFile, original)

	if _, err := Set("TASK-malformed-update", Update{Category: "AFK"}); err == nil || !strings.Contains(err.Error(), "unparseable task block") {
		t.Fatalf("Set malformed candidate error = %v", err)
	}
	got, err := os.ReadFile(config.BacklogFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("metadata update changed malformed document\n got: %q\nwant: %q", got, original)
	}
}

func TestValidateWholeDocumentAllowsEmptyBoardsAndRejectsMalformedTasks(t *testing.T) {
	if err := validateWholeDocument("# TASKS\n\n## Dev Agent 1 In Progress\n", "tasks"); err != nil {
		t.Fatalf("empty board validation error = %v", err)
	}
	data := "# TASKS\n\n## Dev Agent 1 In Progress\n\n### Broken\nOwner: Dev Agent 1\nStatus: In Progress\n"
	if err := validateWholeDocument(data, "tasks"); err == nil || !strings.Contains(err.Error(), "unparseable task block") {
		t.Fatalf("malformed task validation error = %v", err)
	}
}

func TestNormalizeTasksRepairsRepeatedDocumentWithoutLosingRecords(t *testing.T) {
	dir := t.TempDir()
	withBoardFiles(t, dir)
	first := "### Live task\nTask ID: TASK-live\nCategory: AFK\nOwner: Dev Agent 1\nBranch: agent/1/live\nStatus: In Progress\nDependencies: none\nBlocking tasks: none\n\nLive body.\n"
	other := "### Other task\nTask ID: TASK-other\nCategory: AFK\nOwner: Dev Agent 2\nBranch: agent/2/other\nStatus: In Progress\nDependencies: none\nBlocking tasks: none\n\nOther body.\n"
	malformed := "# TASKS\n\n## Dev Agent 1 In Progress\n\n" + first + "\n# TASKS\n\n## Dev Agent 1 In Progress\n\n" + first + "\n## Dev Agent 2 In Progress\n\n" + other + "\n# TASKS\n\n## Dev Agent 2 In Progress\n"
	writeBoard(t, config.TasksFile, malformed)
	if err := NormalizeTasks(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(config.TasksFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if strings.Count(text, "# TASKS") != 1 || strings.Count(text, "## Dev Agent 1 In Progress") != 1 || strings.Count(text, "## Dev Agent 2 In Progress") != 1 {
		t.Fatalf("document headings were not normalized:\n%s", text)
	}
	for _, want := range []string{"TASK-live", "Live body.", "TASK-other", "Other body."} {
		if !strings.Contains(text, want) {
			t.Fatalf("normalized document lost %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "Task ID: TASK-live") != 1 || strings.Count(text, "Task ID: TASK-other") != 1 {
		t.Fatalf("normalized records are duplicated:\n%s", text)
	}
	if err := validateWholeDocument(text, "tasks"); err != nil {
		t.Fatalf("normalized document invalid: %v", err)
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
