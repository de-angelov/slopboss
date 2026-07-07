package experiment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfg "github.com/de-angelov/slopboss/internal/config"
)

const sampleExperimentMarkdown = `# Experiment: dark-mode-backends

We want to compare codex and claude on the same ticket. This prose line is
ignored by the parser.

- Task: Add dark-mode toggle
- Prompt mode: bounded
- Provider: codex
- Timeout minutes: 45
- Prepare command: npm ci

## Variants

### codex-baseline
- Provider: codex
- Model: gpt-5.5
- Config: reasoning=low, verbosity=high

### claude-baseline
- Provider: claude
- Model: claude-sonnet-5
`

func TestParseMarkdownConfig(t *testing.T) {
	config, err := parseMarkdownConfig(sampleExperimentMarkdown)
	if err != nil {
		t.Fatal(err)
	}

	if config.Name != "dark-mode-backends" {
		t.Fatalf("Name = %q, want dark-mode-backends", config.Name)
	}
	if config.TaskTitle != "Add dark-mode toggle" {
		t.Fatalf("TaskTitle = %q", config.TaskTitle)
	}
	if config.PromptMode != "bounded" {
		t.Fatalf("PromptMode = %q", config.PromptMode)
	}
	if config.Provider != "codex" {
		t.Fatalf("Provider = %q", config.Provider)
	}
	if config.TimeoutMinutes != 45 {
		t.Fatalf("TimeoutMinutes = %d, want 45", config.TimeoutMinutes)
	}
	if len(config.PrepareCommands) != 1 || config.PrepareCommands[0] != "npm ci" {
		t.Fatalf("PrepareCommands = %v", config.PrepareCommands)
	}

	if len(config.Variants) != 2 {
		t.Fatalf("parsed %d variants, want 2", len(config.Variants))
	}
	codex := config.Variants[0]
	if codex.Name != "codex-baseline" || codex.Provider != "codex" || codex.Model != "gpt-5.5" {
		t.Fatalf("codex variant = %+v", codex)
	}
	if codex.Config["reasoning"] != "low" || codex.Config["verbosity"] != "high" {
		t.Fatalf("codex variant config = %v", codex.Config)
	}
	claude := config.Variants[1]
	if claude.Name != "claude-baseline" || claude.Provider != "claude" || claude.Model != "claude-sonnet-5" {
		t.Fatalf("claude variant = %+v", claude)
	}
}

func TestParseMarkdownConfigRejectsUnknownKeys(t *testing.T) {
	if _, err := parseMarkdownConfig("# Experiment: x\n\n- Tickit: foo.md\n"); err == nil {
		t.Fatal("expected error for unknown top-level setting")
	}
	if _, err := parseMarkdownConfig("# Experiment: x\n\n## Variants\n\n### v1\n- Modle: gpt-5.5\n"); err == nil {
		t.Fatal("expected error for unknown variant setting")
	}
}

func TestParseMarkdownConfigIgnoresProseAndHeadingsOutsideVariants(t *testing.T) {
	config, err := parseMarkdownConfig("# Experiment: x\n\nJust some notes: not a setting.\n\n## Notes\n\n### not-a-variant\n\n- Task: T\n\n## Variants\n\n### v1\n- Model: m\n")
	if err != nil {
		t.Fatal(err)
	}
	if config.TaskTitle != "T" {
		t.Fatalf("TaskTitle = %q, want T", config.TaskTitle)
	}
	if len(config.Variants) != 1 || config.Variants[0].Name != "v1" {
		t.Fatalf("variants = %+v (a ### under a non-Variants section must be ignored)", config.Variants)
	}
}

func TestReadConfigParsesMarkdownEndToEnd(t *testing.T) {
	oldRoot := cfg.RepoRoot
	cfg.RepoRoot = t.TempDir()
	t.Cleanup(func() { cfg.RepoRoot = oldRoot })

	path := filepath.Join(cfg.RepoRoot, "EXPERIMENT.md")
	if err := os.WriteFile(path, []byte(sampleExperimentMarkdown), 0644); err != nil {
		t.Fatal(err)
	}

	config, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	// Defaults from the shared validation path are applied to Markdown configs too.
	if config.PromptRole != cfg.DevAgent1Role {
		t.Fatalf("PromptRole default = %q, want %q", config.PromptRole, cfg.DevAgent1Role)
	}
	if config.PrepareTimeoutMinutes != 20 {
		t.Fatalf("PrepareTimeoutMinutes default = %d, want 20", config.PrepareTimeoutMinutes)
	}
	if len(config.Variants) != 2 {
		t.Fatalf("variants = %d, want 2", len(config.Variants))
	}
}

func TestReadConfigMarkdownSurfacesUnknownKeyWithPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "EXPERIMENT.md")
	if err := os.WriteFile(path, []byte("# Experiment: x\n\n- Bogus: 1\n\n## Variants\n\n### v\n- Model: m\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadConfig(path)
	if err == nil {
		t.Fatal("expected error for unknown setting")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error should include the config path, got: %v", err)
	}
}
