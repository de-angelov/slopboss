package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTasksTemplateHasOneLanePerAgent(t *testing.T) {
	got := tasksTemplate(3)
	for i := 1; i <= 3; i++ {
		if !strings.Contains(got, "## Dev Agent "+itoa(i)+" In Progress") {
			t.Fatalf("tasks template missing lane %d:\n%s", i, got)
		}
	}
	if strings.Contains(got, "## Dev Agent 4 In Progress") {
		t.Fatal("tasks template should not have a 4th lane for 3 agents")
	}
}

func TestScaffoldBoardFilesCreatesMissingAndKeepsExisting(t *testing.T) {
	root := t.TempDir()

	// Pre-existing file with custom content must be preserved.
	existing := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(existing, []byte("MY CUSTOM RULES"), 0644); err != nil {
		t.Fatal(err)
	}

	created, err := scaffoldBoardFiles(root, 2)
	if err != nil {
		t.Fatal(err)
	}

	// AGENTS.md existed, so it must not be in the created set nor modified.
	for _, name := range created {
		if name == "AGENTS.md" {
			t.Fatal("AGENTS.md already existed and must not be recreated")
		}
	}
	if data, _ := os.ReadFile(existing); string(data) != "MY CUSTOM RULES" {
		t.Fatalf("existing AGENTS.md was overwritten: %q", string(data))
	}

	// All seven marker files must now exist.
	for _, name := range []string{
		"BACKLOG.md", "TASKS.md", "ARCHIVE.md",
		"AGENTS.md", "DEV_AGENT.md", "TEAM_LEAD_AGENT.md", "TECH.md",
	} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("expected %s to exist after scaffold: %v", name, err)
		}
	}

	// TASKS.md should carry the requested lanes.
	tasks, _ := os.ReadFile(filepath.Join(root, "TASKS.md"))
	if !strings.Contains(string(tasks), "## Dev Agent 2 In Progress") {
		t.Fatalf("scaffolded TASKS.md missing agent-2 lane:\n%s", tasks)
	}

	// A second run creates nothing new (idempotent).
	created2, err := scaffoldBoardFiles(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(created2) != 0 {
		t.Fatalf("second scaffold created %v, want none", created2)
	}
}

func TestAgentsTemplateIsGenericAndCountAware(t *testing.T) {
	got := agentsTemplate(3)
	for i := 1; i <= 3; i++ {
		if !strings.Contains(got, "Dev Agent "+itoa(i)) {
			t.Fatalf("AGENTS.md missing role Dev Agent %d:\n%s", i, got)
		}
		if !strings.Contains(got, "workspaces/repo-agent-"+itoa(i)) {
			t.Fatalf("AGENTS.md missing workspace for agent %d", i)
		}
	}
	if strings.Contains(got, "Dev Agent 4") {
		t.Fatal("AGENTS.md leaked a 4th agent for a 3-agent setup")
	}
	if strings.Contains(got, "agent-framework") {
		t.Fatal("AGENTS.md default must not carry the source repo name")
	}
}

func itoa(n int) string {
	return string(rune('0' + n))
}
