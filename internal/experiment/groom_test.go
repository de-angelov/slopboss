package experiment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/de-angelov/slopboss/internal/board"
	cfg "github.com/de-angelov/slopboss/internal/config"
)

func TestBuildExperimentGroomPromptLoadsInstructionsBoardAndFormat(t *testing.T) {
	oldAgents, oldTL, oldTech := cfg.AgentsFile, cfg.TlAgentInstructionsFile, cfg.TechFile
	dir := t.TempDir()
	cfg.AgentsFile = filepath.Join(dir, "AGENTS.md")
	cfg.TlAgentInstructionsFile = filepath.Join(dir, "TEAM_LEAD_AGENT.md")
	cfg.TechFile = filepath.Join(dir, "TECH.md")
	t.Cleanup(func() {
		cfg.AgentsFile, cfg.TlAgentInstructionsFile, cfg.TechFile = oldAgents, oldTL, oldTech
	})

	for path, content := range map[string]string{
		cfg.AgentsFile:              "COMMON RULES MARKER",
		cfg.TlAgentInstructionsFile: "TEAM LEAD MARKER",
		cfg.TechFile:                "TECH MARKER",
	} {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	got := buildExperimentGroomPrompt([]board.Task{
		{Section: "Backlog", Title: "Add dark-mode toggle", Status: "Backlog", Body: "toggle the theme"},
	})

	for _, want := range []string{
		"COMMON RULES MARKER",
		"TEAM LEAD MARKER",
		"TECH MARKER",
		"INTERACTIVE experiment-design session",
		"Add dark-mode toggle",   // board context is included
		"EXPERIMENT FILE FORMAT", // the format spec is embedded
		"## Variants",            // format spec content
		ExperimentFileName,       // tells the TL which file to write
		"Do NOT run the experiment",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("experiment groom prompt missing %q\n%s", want, got)
		}
	}
}
