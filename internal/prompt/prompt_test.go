package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
)

func TestTeamLeadContextIncludesDepsAndCompletedIDs(t *testing.T) {
	// Completed IDs are read from ARCHIVE.md, so point ArchiveFile at a temp file.
	oldArchive := config.ArchiveFile
	config.ArchiveFile = filepath.Join(t.TempDir(), "ARCHIVE.md")
	t.Cleanup(func() { config.ArchiveFile = oldArchive })
	if err := os.WriteFile(config.ArchiveFile, []byte("# ARCHIVE\n\n## Done\n\n### Completed Dependency\n\nTask ID: DONE-01\nStatus: Done\n"), 0644); err != nil {
		t.Fatal(err)
	}

	context := BuildTaskContext(config.TeamLeadRole, board.Task{}, []board.Task{
		{
			Section:      "Backlog",
			Title:        "Ready Work",
			ID:           "READY-01",
			Status:       "Backlog",
			Category:     "AFK",
			Dependencies: "DONE-01",
			Body:         "Task ID: READY-01\nDependencies: DONE-01",
		},
	})

	// The backlog summary must now carry the dependency so grooming can reason.
	if !strings.Contains(context, "Deps: DONE-01") {
		t.Fatalf("expected backlog summary to include dependencies; got:\n%s", context)
	}
	// The completed dependency must be resolvable from the compact ID list.
	if !strings.Contains(context, "DONE-01") || !strings.Contains(context, "Completed task IDs") {
		t.Fatalf("expected completed task ID list to include DONE-01; got:\n%s", context)
	}
}

func TestDevAgentContextDoesNotIncludeArchiveTasks(t *testing.T) {
	context := BuildTaskContext(config.DevAgent1Role, board.Task{}, []board.Task{
		{
			Section: "Done",
			Title:   "Completed Dependency",
			Status:  "Done",
			Body:    "Task ID: DONE-01\nStatus: Done",
		},
	})

	if strings.Contains(context, "Completed Dependency") {
		t.Fatal("expected dev agent context to exclude archive tasks")
	}
}

func TestDevAgentPromptIncludesTokenGuardrails(t *testing.T) {
	oldAgentsFile := config.AgentsFile
	oldDevAgentInstructionsFile := config.DevAgentInstructionsFile
	oldTechFile := config.TechFile
	dir := t.TempDir()
	config.AgentsFile = filepath.Join(dir, "AGENTS.md")
	config.DevAgentInstructionsFile = filepath.Join(dir, "DEV_AGENT.md")
	config.TechFile = filepath.Join(dir, "TECH.md")
	t.Cleanup(func() {
		config.AgentsFile = oldAgentsFile
		config.DevAgentInstructionsFile = oldDevAgentInstructionsFile
		config.TechFile = oldTechFile
	})

	for path, content := range map[string]string{
		config.AgentsFile:               "common rules",
		config.DevAgentInstructionsFile: "dev rules",
		config.TechFile:                 "tech rules",
	} {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	built := BuildPrompt(
		config.DevAgent1Role,
		board.Task{
			Section: "Dev Agent 1 In Progress",
			Title:   "Build auth",
			Owner:   config.DevAgent1Role,
			Branch:  "agent/1/auth",
			Status:  "In Progress",
			Body:    "Build auth body",
		},
		nil,
		DevAgentRuntimeInstructions(),
	)

	for _, want := range []string{
		"current working directory is already the assigned product repository",
		"Test contract, not implementation",
		"do not invent an interaction harness",
		"avoid adding more test code than implementation code",
		"If verification reveals unrelated failures, stop and mark the task Blocked",
		"Set Status: Blocked",
	} {
		if !strings.Contains(built, want) {
			t.Fatalf("prompt missing %q\n%s", want, built)
		}
	}
}

func TestBuildPromptKeepsStaticPrefixBeforePerRunContext(t *testing.T) {
	oldAgentsFile := config.AgentsFile
	oldDevAgentInstructionsFile := config.DevAgentInstructionsFile
	oldTechFile := config.TechFile
	dir := t.TempDir()
	config.AgentsFile = filepath.Join(dir, "AGENTS.md")
	config.DevAgentInstructionsFile = filepath.Join(dir, "DEV_AGENT.md")
	config.TechFile = filepath.Join(dir, "TECH.md")
	t.Cleanup(func() {
		config.AgentsFile = oldAgentsFile
		config.DevAgentInstructionsFile = oldDevAgentInstructionsFile
		config.TechFile = oldTechFile
	})

	for path, content := range map[string]string{
		config.AgentsFile:               "common rules",
		config.DevAgentInstructionsFile: "dev rules",
		config.TechFile:                 "tech rules",
	} {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	built := BuildPrompt(
		config.DevAgent1Role,
		board.Task{
			Section: "Dev Agent 1 In Progress",
			Title:   "Build auth",
			Owner:   config.DevAgent1Role,
			Branch:  "agent/1/auth",
			Status:  "In Progress",
			Body:    "Build auth body",
		},
		nil,
		DevAgentRuntimeInstructions(),
	)

	divider := strings.Index(built, "PER-RUN CONTEXT")
	if divider < 0 {
		t.Fatalf("prompt missing PER-RUN CONTEXT divider\n%s", built)
	}

	// Everything stable must sit in the cacheable prefix, before the divider.
	for _, want := range []string{"common rules", "dev rules", "tech rules", "RUNTIME INSTRUCTIONS"} {
		if idx := strings.Index(built, want); idx < 0 || idx >= divider {
			t.Fatalf("expected %q in the static prefix (before divider at %d), found at %d", want, divider, idx)
		}
	}

	// Everything that changes each tick must sit after the divider so it does not
	// invalidate the cached prefix.
	for _, want := range []string{"BOARD CONTEXT", "Build auth body"} {
		if idx := strings.Index(built, want); idx < divider {
			t.Fatalf("expected %q after the divider at %d, found at %d", want, divider, idx)
		}
	}
}
