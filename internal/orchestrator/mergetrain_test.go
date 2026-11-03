package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
)

func TestFormatCommitMessage(t *testing.T) {
	tests := []struct {
		name      string
		taskID    string
		taskTitle string
		rawMsg    string
		wantFirst string
		hasBody   bool
	}{
		{
			name:      "empty raw message triggers fallback",
			taskID:    "TASK-100",
			taskTitle: "Do the needful",
			rawMsg:    "",
			wantFirst: "TASK-100: Do the needful",
			hasBody:   true,
		},
		{
			name:      "raw message without task ID gets prepended",
			taskID:    "TASK-101",
			taskTitle: "Add tests",
			rawMsg:    "Add new parser tests\n\nDetailed rationale here.",
			wantFirst: "TASK-101: Add new parser tests",
			hasBody:   true,
		},
		{
			name:      "raw message already having task ID is preserved",
			taskID:    "TASK-102",
			taskTitle: "Fix docs",
			rawMsg:    "TASK-102: update readme\n\nPreserve this description.",
			wantFirst: "TASK-102: update readme",
			hasBody:   true,
		},
		{
			name:      "raw message with colon-less task ID prefix is preserved",
			taskID:    "TASK-103",
			taskTitle: "Refactor",
			rawMsg:    "TASK-103 refactor module\n\nLine 2",
			wantFirst: "TASK-103 refactor module",
			hasBody:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatCommitMessage(tt.taskID, tt.taskTitle, tt.rawMsg)
			lines := strings.Split(got, "\n")
			if lines[0] != tt.wantFirst {
				t.Fatalf("first line = %q, want %q", lines[0], tt.wantFirst)
			}
			if tt.hasBody && len(lines) < 2 {
				t.Fatalf("expected body lines in formatted message: %q", got)
			}
		})
	}
}

func TestLandCompletedTaskAutoCommitsAndSquashes(t *testing.T) {
	// Initialize a remote bare repo and a local workspace clone
	tempDir := t.TempDir()
	remoteDir := filepath.Join(tempDir, "remote.git")
	workspace := filepath.Join(tempDir, "workspace")

	// Init bare remote
	runCmd(t, tempDir, "git", "init", "--bare", "-b", "main", remoteDir)

	// Clone to workspace
	runCmd(t, tempDir, "git", "clone", remoteDir, workspace)
	runCmd(t, workspace, "git", "config", "user.email", "test@example.com")
	runCmd(t, workspace, "git", "config", "user.name", "Test User")

	// Initial commit on main
	initialFile := filepath.Join(workspace, "README.md")
	if err := os.WriteFile(initialFile, []byte("# Initial\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, workspace, "git", "add", "README.md")
	runCmd(t, workspace, "git", "commit", "-m", "initial commit")
	runCmd(t, workspace, "git", "push", "origin", "main")

	// Setup board directory in tempDir
	boardDir := filepath.Join(tempDir, "board")
	if err := os.MkdirAll(boardDir, 0755); err != nil {
		t.Fatal(err)
	}

	tasksFile := filepath.Join(boardDir, "TASKS.md")
	archiveFile := filepath.Join(boardDir, "ARCHIVE.md")
	backlogFile := filepath.Join(boardDir, "BACKLOG.md")

	taskContent := "# TASKS\n\n## Dev Agent 1 In Progress\n\n### Implement feature\nTask ID: TASK-001\nOwner: Dev Agent 1\nBranch: agent/1/feature\nStatus: In Progress\n\nObjective: Implement feature\nVerification: test -f feature.txt\n"
	if err := os.WriteFile(tasksFile, []byte(taskContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archiveFile, []byte("# ARCHIVE\n\n## Done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlogFile, []byte("# BACKLOG\n\n## Backlog\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Point config to boardDir and workspace
	config.RepoRoot = boardDir
	config.TasksFile = tasksFile
	config.ArchiveFile = archiveFile
	config.BacklogFile = backlogFile
	config.BaseBranch = "main"

	// Create feature branch
	runCmd(t, workspace, "git", "checkout", "-b", "agent/1/feature")
	featureFile := filepath.Join(workspace, "feature.txt")
	if err := os.WriteFile(featureFile, []byte("feature content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	task := board.Task{
		ID:     "TASK-001",
		Title:  "Implement feature",
		Owner:  "Dev Agent 1",
		Branch: "agent/1/feature",
		Status: "In Progress",
		Body:   "Objective: Implement feature\nVerification: test -f feature.txt",
	}

	outcome, evidence := LandCompletedTask(context.Background(), task, "Dev Agent 1", workspace)
	if outcome != sessionCompleted {
		t.Fatalf("LandCompletedTask failed: outcome=%v, evidence=%s", outcome, evidence)
	}

	// Verify main has the squash commit
	runCmd(t, workspace, "git", "checkout", "main")
	runCmd(t, workspace, "git", "pull", "origin", "main")
	logOut := runCmdOutput(t, workspace, "git", "log", "-1", "--pretty=%B")
	if !strings.Contains(logOut, "TASK-001: Implement feature") {
		t.Fatalf("expected squash commit on main to contain TASK-001, got: %s", logOut)
	}

	// Verify ARCHIVE.md has TASK-001
	archiveData, err := os.ReadFile(archiveFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(archiveData), "TASK-001") {
		t.Fatalf("expected ARCHIVE.md to contain TASK-001, got: %s", string(archiveData))
	}
}

func runCmd(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cmd %s %v failed: %v\n%s", name, args, err, out)
	}
}

func runCmdOutput(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("cmd %s %v failed: %v", name, args, err)
	}
	return string(out)
}
