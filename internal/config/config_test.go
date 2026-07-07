package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveRepoRootFromCurrentRoot(t *testing.T) {
	root := makeRepoRoot(t)

	resolved, ok := resolveRepoRootFrom(root)
	if !ok {
		t.Fatal("expected repo root to be resolved")
	}
	if resolved != root {
		t.Fatalf("resolved root = %q, want %q", resolved, root)
	}
}

func TestResolveRepoRootFromOrchestratorSubdirectory(t *testing.T) {
	root := makeRepoRoot(t)
	orchestratorDir := filepath.Join(root, "orchestrator")
	if err := os.Mkdir(orchestratorDir, 0755); err != nil {
		t.Fatal(err)
	}

	resolved, ok := resolveRepoRootFrom(orchestratorDir)
	if !ok {
		t.Fatal("expected repo root to be resolved")
	}
	if resolved != root {
		t.Fatalf("resolved root = %q, want %q", resolved, root)
	}
}

func TestResolveRepoRootFromDirectoryWithoutMarkers(t *testing.T) {
	dir := t.TempDir()

	resolved, ok := resolveRepoRootFrom(dir)
	if ok {
		t.Fatalf("resolved root = %q, want no root", resolved)
	}
}

func TestNewRunLogFilePathUsesTimestampedLogFile(t *testing.T) {
	oldLogsRoot := LogsRoot
	LogsRoot = t.TempDir()
	t.Cleanup(func() { LogsRoot = oldLogsRoot })

	got := NewRunLogFilePath(time.Date(2026, 6, 30, 13, 45, 12, 123456789, time.UTC))
	want := filepath.Join(LogsRoot, "orchestrator-20260630-134512.123456789.log")

	if got != want {
		t.Fatalf("NewRunLogFilePath() = %q, want %q", got, want)
	}
}

func TestDevAgentRoleSectionRoundTrip(t *testing.T) {
	for k := 1; k <= 5; k++ {
		role := DevAgentRole(k)
		idx, ok := DevAgentIndexForRole(role)
		if !ok || idx != k {
			t.Fatalf("round-trip failed for %d: role=%q idx=%d ok=%v", k, role, idx, ok)
		}
		if got, want := DevAgentSection(k), role+" In Progress"; got != want {
			t.Fatalf("section for %d = %q, want %q", k, got, want)
		}
	}

	if _, ok := DevAgentIndexForRole(TeamLeadRole); ok {
		t.Fatal("team lead role should not parse as a dev agent")
	}
	if _, ok := DevAgentIndexForRole("Dev Agent x"); ok {
		t.Fatal("malformed dev role should not parse")
	}
}

func TestDevAgentRoleForActiveSectionRespectsCount(t *testing.T) {
	old := DevAgentCount
	DevAgentCount = 3
	t.Cleanup(func() { DevAgentCount = old })

	if role, ok := DevAgentRoleForActiveSection("Dev Agent 3 In Progress"); !ok || role != "Dev Agent 3" {
		t.Fatalf("expected Dev Agent 3 within count, got role=%q ok=%v", role, ok)
	}
	// The " In Progress" suffix is optional: a plain "Dev Agent K" header must
	// still resolve to the lane (guards against a Team Lead dropping the suffix).
	if role, ok := DevAgentRoleForActiveSection("Dev Agent 2"); !ok || role != "Dev Agent 2" {
		t.Fatalf("expected plain 'Dev Agent 2' to match, got role=%q ok=%v", role, ok)
	}
	if _, ok := DevAgentRoleForActiveSection("Dev Agent 4 In Progress"); ok {
		t.Fatal("Dev Agent 4 exceeds count of 3 and must not match")
	}
	if _, ok := DevAgentRoleForActiveSection("Dev Agent 4"); ok {
		t.Fatal("plain 'Dev Agent 4' also exceeds count of 3 and must not match")
	}
	if _, ok := DevAgentRoleForActiveSection("Backlog"); ok {
		t.Fatal("Backlog is not a dev-agent section")
	}
}

func TestDiscoverDevAgentCountCountsContiguousWorkspaces(t *testing.T) {
	oldRoot := WorkspacesRoot
	WorkspacesRoot = t.TempDir()
	t.Cleanup(func() { WorkspacesRoot = oldRoot })

	mkAgent := func(n int) {
		if err := os.MkdirAll(DevAgentWorkspace(n), 0755); err != nil {
			t.Fatal(err)
		}
	}

	if got := DiscoverDevAgentCount(); got != 0 {
		t.Fatalf("no workspaces -> %d, want 0", got)
	}

	mkAgent(1)
	mkAgent(2)
	mkAgent(3)
	if got := DiscoverDevAgentCount(); got != 3 {
		t.Fatalf("three contiguous workspaces -> %d, want 3", got)
	}

	// A gap at 4 means 5 is not counted.
	mkAgent(5)
	if got := DiscoverDevAgentCount(); got != 3 {
		t.Fatalf("gap after 3 -> %d, want 3 (stops at first missing index)", got)
	}

	// A plain file (not a dir) at the next index must not count as a workspace.
	WorkspacesRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(WorkspacesRoot, "repo-agent-1"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := DiscoverDevAgentCount(); got != 0 {
		t.Fatalf("file at index 1 -> %d, want 0 (must be a directory)", got)
	}
}

func makeRepoRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	for _, marker := range repoRootMarkers {
		path := filepath.Join(root, marker)
		if err := os.WriteFile(path, []byte(marker+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	return root
}
