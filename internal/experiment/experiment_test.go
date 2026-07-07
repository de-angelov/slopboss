package experiment

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/de-angelov/slopboss/internal/board"
	cfg "github.com/de-angelov/slopboss/internal/config"
)

func TestReadExperimentConfigDefaultsAndResolvesPaths(t *testing.T) {
	root := t.TempDir()
	oldRepoRoot := cfg.RepoRoot
	oldAgent1Path := cfg.Agent1Path
	cfg.RepoRoot = root
	cfg.Agent1Path = initGitWorkspace(t)
	t.Cleanup(func() {
		cfg.RepoRoot = oldRepoRoot
		cfg.Agent1Path = oldAgent1Path
	})

	configPath := filepath.Join(root, "experiment.json")
	if err := os.WriteFile(configPath, []byte(`{
  "name": "Prompt trial",
  "ticketFile": "tickets/auth.md",
  "variants": [{"name": "baseline"}]
}`), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := ReadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if config.SourceWorkspace != cfg.Agent1Path {
		t.Fatalf("SourceWorkspace = %q, want %q", config.SourceWorkspace, cfg.Agent1Path)
	}
	if config.BaseBranch != "main" {
		t.Fatalf("BaseBranch = %q, want current branch main", config.BaseBranch)
	}
	if config.TicketFile != filepath.Join(root, "tickets", "auth.md") {
		t.Fatalf("TicketFile = %q, want resolved path", config.TicketFile)
	}
	if config.OutputDir != filepath.Join(root, "evals") {
		t.Fatalf("OutputDir = %q, want default evals dir", config.OutputDir)
	}
	if config.TimeoutMinutes != 90 {
		t.Fatalf("TimeoutMinutes = %d, want 90", config.TimeoutMinutes)
	}
	if config.PrepareTimeoutMinutes != 20 {
		t.Fatalf("PrepareTimeoutMinutes = %d, want 20", config.PrepareTimeoutMinutes)
	}
}

func TestBuildExperimentPromptIncludesVariantAndRules(t *testing.T) {
	oldAgentsFile := cfg.AgentsFile
	oldDevAgentInstructionsFile := cfg.DevAgentInstructionsFile
	oldTechFile := cfg.TechFile
	dir := t.TempDir()
	cfg.AgentsFile = filepath.Join(dir, "AGENTS.md")
	cfg.DevAgentInstructionsFile = filepath.Join(dir, "DEV_AGENT.md")
	cfg.TechFile = filepath.Join(dir, "TECH.md")
	t.Cleanup(func() {
		cfg.AgentsFile = oldAgentsFile
		cfg.DevAgentInstructionsFile = oldDevAgentInstructionsFile
		cfg.TechFile = oldTechFile
	})

	mustWriteTestFile(t, cfg.AgentsFile, "common rules")
	mustWriteTestFile(t, cfg.DevAgentInstructionsFile, "dev rules")
	mustWriteTestFile(t, cfg.TechFile, "tech rules")
	promptFile := filepath.Join(dir, "variant.md")
	mustWriteTestFile(t, promptFile, "use low reasoning")

	prompt, err := buildExperimentPrompt(ExperimentConfig{PromptMode: "bounded"}, board.Task{Body: "Build auth"}, ExperimentVariant{Name: "low", PromptFile: promptFile})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"common rules",
		"tech rules",
		"use low reasoning",
		"Build auth",
		"Exploration budget",
		"Do not push",
		"Do not install dependencies",
		"current working directory is already the assigned product repository",
		"Test contract, not implementation",
		"do not invent an interaction harness",
		"avoid adding more test code than implementation code",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "dev rules") {
		t.Fatalf("prompt should not include full DEV_AGENT.md instructions\n%s", prompt)
	}
}

func TestBuildExperimentPromptCanMirrorDevOrchestratorPrompt(t *testing.T) {
	oldRepoRoot := cfg.RepoRoot
	oldAgentsFile := cfg.AgentsFile
	oldDevAgentInstructionsFile := cfg.DevAgentInstructionsFile
	oldTechFile := cfg.TechFile
	oldBacklogFile := cfg.BacklogFile
	oldTasksFile := cfg.TasksFile
	oldArchiveFile := cfg.ArchiveFile
	dir := t.TempDir()
	cfg.RepoRoot = dir
	cfg.AgentsFile = filepath.Join(dir, "AGENTS.md")
	cfg.DevAgentInstructionsFile = filepath.Join(dir, "DEV_AGENT.md")
	cfg.TechFile = filepath.Join(dir, "TECH.md")
	cfg.BacklogFile = filepath.Join(dir, "BACKLOG.md")
	cfg.TasksFile = filepath.Join(dir, "TASKS.md")
	cfg.ArchiveFile = filepath.Join(dir, "ARCHIVE.md")
	t.Cleanup(func() {
		cfg.RepoRoot = oldRepoRoot
		cfg.AgentsFile = oldAgentsFile
		cfg.DevAgentInstructionsFile = oldDevAgentInstructionsFile
		cfg.TechFile = oldTechFile
		cfg.BacklogFile = oldBacklogFile
		cfg.TasksFile = oldTasksFile
		cfg.ArchiveFile = oldArchiveFile
	})

	mustWriteTestFile(t, cfg.AgentsFile, "common rules")
	mustWriteTestFile(t, cfg.DevAgentInstructionsFile, "dev rules")
	mustWriteTestFile(t, cfg.TechFile, "tech rules")
	mustWriteTestFile(t, cfg.BacklogFile, "# BACKLOG\n")
	mustWriteTestFile(t, cfg.TasksFile, "# TASKS\n\n## Dev Agent 2 In Progress\n\n### Other Task\nOwner: Dev Agent 2\nBranch: agent/2/other\nStatus: In Progress\n\nOther body\n")
	mustWriteTestFile(t, cfg.ArchiveFile, "# ARCHIVE\n")

	prompt, err := buildExperimentPrompt(
		ExperimentConfig{PromptMode: "orchestrator-dev", PromptRole: cfg.DevAgent1Role},
		board.Task{
			Section: "Dev Agent 1 In Progress",
			Title:   "Build auth",
			Owner:   cfg.DevAgent1Role,
			Branch:  "agent/1/auth",
			Status:  "In Progress",
			Body:    "Build auth body",
		},
		ExperimentVariant{Name: "orchestrator"},
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"Active role: Dev Agent 1",
		"dev rules",
		"Other active dev-agent work",
		"Other Task",
		"Build auth body",
		"EXPERIMENT SAFETY OVERRIDES",
		"Do not push",
		"current working directory is already the assigned product repository",
		"Test contract, not implementation",
		"do not invent an interaction harness",
		"avoid adding more test code than implementation code",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q\n%s", want, prompt)
		}
	}
}

func TestExperimentPrepareCommandsSymlinkSourceNodeModules(t *testing.T) {
	source := t.TempDir()
	worktree := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}
	mustWriteTestFile(t, filepath.Join(worktree, "package-lock.json"), "{}")

	commands := experimentPrepareCommands(ExperimentConfig{SourceWorkspace: source}, worktree)
	if len(commands) != 1 {
		t.Fatalf("commands = %v, want one symlink command", commands)
	}
	if !strings.Contains(commands[0], "ln -s") || !strings.Contains(commands[0], "node_modules") {
		t.Fatalf("command = %q, want node_modules symlink", commands[0])
	}
}

func TestExperimentPrepareCommandsFallsBackToNpmCi(t *testing.T) {
	worktree := t.TempDir()
	mustWriteTestFile(t, filepath.Join(worktree, "package-lock.json"), "{}")

	commands := experimentPrepareCommands(ExperimentConfig{SourceWorkspace: t.TempDir()}, worktree)
	if len(commands) != 1 || commands[0] != "npm ci" {
		t.Fatalf("commands = %v, want npm ci", commands)
	}
}

func TestExperimentPrepareCommandsCanBeSkipped(t *testing.T) {
	worktree := t.TempDir()
	mustWriteTestFile(t, filepath.Join(worktree, "package-lock.json"), "{}")

	commands := experimentPrepareCommands(ExperimentConfig{SkipPrepare: true}, worktree)
	if len(commands) != 0 {
		t.Fatalf("commands = %v, want none", commands)
	}
}

func TestResolveExperimentTaskFromBoardTask(t *testing.T) {
	oldBacklogFile := cfg.BacklogFile
	dir := t.TempDir()
	cfg.BacklogFile = filepath.Join(dir, "BACKLOG.md")
	t.Cleanup(func() {
		cfg.BacklogFile = oldBacklogFile
	})
	mustWriteTestFile(t, cfg.BacklogFile, `# BACKLOG

## Backlog

### Team Create Dialog

Task ID: UI-DIALOG-01
Status: Backlog

#### Objective

Move team creation into the shared dialog component.
`)

	task, source, err := resolveExperimentTask(ExperimentConfig{TaskTitle: "Team Create Dialog"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(task.Body, "Move team creation") {
		t.Fatalf("task body = %q", task.Body)
	}
	if task.Title != "Team Create Dialog" {
		t.Fatalf("task title = %q", task.Title)
	}
	if !strings.Contains(source, "Team Create Dialog") {
		t.Fatalf("source = %q", source)
	}
}

func TestParseDiffShortstat(t *testing.T) {
	files, insertions, deletions := parseDiffShortstat(" 3 files changed, 24 insertions(+), 7 deletions(-)")
	if files != 3 || insertions != 24 || deletions != 7 {
		t.Fatalf("got %d/%d/%d", files, insertions, deletions)
	}
}

func TestVariantProviderResolutionPrecedence(t *testing.T) {
	// per-variant provider wins over config- and run-level defaults.
	_, name, err := variantProvider(
		ExperimentConfig{Provider: "codex"},
		ExperimentVariant{Provider: "claude"},
		"codex",
	)
	if err != nil || name != "claude" {
		t.Fatalf("per-variant provider = %q err=%v, want claude", name, err)
	}

	// config-level provider wins over the run-level default.
	_, name, err = variantProvider(
		ExperimentConfig{Provider: "claude"},
		ExperimentVariant{},
		"codex",
	)
	if err != nil || name != "claude" {
		t.Fatalf("config provider = %q err=%v, want claude", name, err)
	}

	// falls back to the run-level default when nothing is set.
	_, name, err = variantProvider(ExperimentConfig{}, ExperimentVariant{}, "claude")
	if err != nil || name != "claude" {
		t.Fatalf("default provider = %q err=%v, want claude", name, err)
	}

	// an unknown provider surfaces an error.
	if _, _, err := variantProvider(ExperimentConfig{}, ExperimentVariant{Provider: "gemini"}, "codex"); err == nil {
		t.Fatal("expected error for unknown variant provider")
	}
}

func TestExperimentRunNameIsStableShape(t *testing.T) {
	name := experimentRunName("Auth Prompt Trial", time.Date(2026, 7, 2, 9, 8, 7, 0, time.UTC))
	if !strings.HasPrefix(name, "17-auth-prompt-trial-20260702-090807-") {
		t.Fatalf("unexpected run name %q", name)
	}
}

func mustWriteTestFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func initGitWorkspace(t *testing.T) string {
	t.Helper()

	workspace := t.TempDir()
	runTestGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = workspace
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
		}
	}

	runTestGit("init", "-b", "main")
	runTestGit("config", "user.email", "test@example.com")
	runTestGit("config", "user.name", "Test User")
	mustWriteTestFile(t, filepath.Join(workspace, "README.md"), "# Test\n")
	runTestGit("add", "README.md")
	runTestGit("commit", "-m", "initial")

	return workspace
}
