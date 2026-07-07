package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/de-angelov/slopboss/internal/board"
	"github.com/de-angelov/slopboss/internal/config"
)

func TestBuildGroomPromptLoadsInstructionsAndBoard(t *testing.T) {
	oldAgents, oldTL, oldTech := config.AgentsFile, config.TlAgentInstructionsFile, config.TechFile
	dir := t.TempDir()
	config.AgentsFile = filepath.Join(dir, "AGENTS.md")
	config.TlAgentInstructionsFile = filepath.Join(dir, "TEAM_LEAD_AGENT.md")
	config.TechFile = filepath.Join(dir, "TECH.md")
	t.Cleanup(func() {
		config.AgentsFile, config.TlAgentInstructionsFile, config.TechFile = oldAgents, oldTL, oldTech
	})

	for path, content := range map[string]string{
		config.AgentsFile:              "COMMON RULES MARKER",
		config.TlAgentInstructionsFile: "TEAM LEAD MARKER",
		config.TechFile:                "TECH MARKER",
	} {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	built := buildGroomPrompt([]board.Task{
		{Section: "Backlog", Title: "Existing Backlog Item", Status: "Backlog", Body: "do the thing"},
	})

	for _, want := range []string{
		"COMMON RULES MARKER",
		"TEAM LEAD MARKER",
		"TECH MARKER",
		"INTERACTIVE backlog grooming session",
		"Existing Backlog Item",       // board context is included
		"do NOT modify the Dev Agent", // grooming guardrail
	} {
		if !strings.Contains(built, want) {
			t.Fatalf("groom prompt missing %q\n%s", want, built)
		}
	}
}
