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

	created, err := scaffoldBoardFiles(root, 2, "main")
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
	created2, err := scaffoldBoardFiles(root, 2, "main")
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

func TestAgentsTemplateDocumentsBacklogStatusVocabulary(t *testing.T) {
	got := agentsTemplate(2)
	for _, want := range []string{
		"Pending assignable backlog tasks must use Status: Backlog.",
		"readiness concept, not a valid backlog Status value.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("AGENTS.md template missing %q:\n%s", want, got)
		}
	}
}

func TestTeamLeadTemplateDocumentsReadyIsNotAStatus(t *testing.T) {
	got := teamLeadTemplate
	for _, want := range []string{
		"READY is a readiness concept, not a metadata Status.",
		"Use Status: Backlog for unassigned tasks in BACKLOG.md. Do not write",
		"Status: Ready.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("TEAM_LEAD_AGENT.md template missing %q:\n%s", want, got)
		}
	}
}

func TestTeamLeadTemplateTreatsBlockedWorkAsNonOccupying(t *testing.T) {
	got := teamLeadTemplate
	for _, want := range []string{
		"Treat a dev-agent lane as occupied only by its In Progress task.",
		"do not consume lane capacity",
		"Verification failures outside a dev task do not block that task.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("TEAM_LEAD_AGENT.md template missing lane rule %q:\n%s", want, got)
		}
	}
}

func TestDevAgentTemplateKeepsUnrelatedFailuresOutOfTheBlockerPath(t *testing.T) {
	got := devAgentTemplate("main")
	for _, want := range []string{
		"Your targeted tests and build are the merge gate.",
		"do not mark the task Blocked",
		"Note the failing command and path in the task's",
		"Blocked tasks may remain in their lane",
		"Keep the task in its lane",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("DEV_AGENT.md template missing verification rule %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "## Cleanup Protocol") {
		t.Fatal("DEV_AGENT.md still contains the over-engineered cleanup protocol")
	}
}

func TestAgentsTemplateDefinesLaneOccupancy(t *testing.T) {
	got := agentsTemplate(2)
	for _, want := range []string{
		"A dev-agent lane may contain at most one In Progress task.",
		"do not count as active work and do not consume capacity",
		"Verification failures outside the assigned task do not make that task Blocked.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("AGENTS.md template missing lane rule %q:\n%s", want, got)
		}
	}
}

func TestDevAgentTemplateUsesBaseBranch(t *testing.T) {
	got := devAgentTemplate("develop")
	if !strings.Contains(got, "squash-merged into product develop") {
		t.Fatalf("DEV_AGENT.md should reference the base branch 'develop':\n%s", got)
	}
	if strings.Contains(got, "product main") {
		t.Fatalf("DEV_AGENT.md leaked 'main' when base branch is develop:\n%s", got)
	}
}

func itoa(n int) string {
	return string(rune('0' + n))
}
