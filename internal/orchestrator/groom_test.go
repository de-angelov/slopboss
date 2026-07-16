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
		{Section: "Backlog", Title: "Existing Backlog Item", Status: "Backlog", Category: "AFK", Body: "do the thing"},
		{Section: "Backlog", Title: "Choose payment provider", ID: "PAY-01", Status: "Backlog", Category: "HITL", Body: "pick provider"},
		{Section: "Backlog", Title: "Approve launch copy", ID: "COPY-01", Status: "Blocked", Category: "HITL", Body: "approve copy"},
		{Section: "Backlog", Title: "Old human task", ID: "OLD-01", Status: "Done", Category: "HITL", Body: "done"},
	})

	for _, want := range []string{
		"COMMON RULES MARKER",
		"TEAM LEAD MARKER",
		"TECH MARKER",
		"INTERACTIVE backlog grooming session",
		"Existing Backlog Item",       // board context is included
		"do NOT modify the Dev Agent", // grooming guardrail
		"HUMAN DECISIONS WAITING",
		"2 task(s) awaiting human decision.",
		"PAY-01 - Choose payment provider",
		"COPY-01 - Approve launch copy",
	} {
		if !strings.Contains(built, want) {
			t.Fatalf("groom prompt missing %q\n%s", want, built)
		}
	}
	if strings.Contains(built, "OLD-01 - Old human task") {
		t.Fatalf("groom prompt should not list completed HITL tasks\n%s", built)
	}
}

func TestBuildHumanDecisionSummaryShowsZero(t *testing.T) {
	got := buildHumanDecisionSummary([]board.Task{
		{Section: "Backlog", Title: "Autonomous work", Status: "Backlog", Category: "AFK"},
	})
	if got != "0 task(s) awaiting human decision." {
		t.Fatalf("summary = %q", got)
	}
}
