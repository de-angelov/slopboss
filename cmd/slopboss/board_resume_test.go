package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/de-angelov/slopboss/internal/orchestrator"
)

func TestBoardExposesExplicitWorkerContinuation(t *testing.T) {
	cmd, _, err := boardCmd.Find([]string{"resume"})
	if err != nil || cmd == boardCmd || cmd.Name() != "resume" {
		t.Fatalf("no worker continuation command: command=%v err=%v", cmd, err)
	}
}

// TestBoardResumeCLI exercises separate CLI processes against a persisted board
// and controller snapshot, including the fail-closed unresolved-lease path.
func TestBoardResumeCLI(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"BACKLOG.md": "# BACKLOG\n\n## Backlog\n",
		"TASKS.md":   "# TASKS\n\n## Dev Agent 1 In Progress\n\n### Fix\nTask ID: A\nCategory: AFK\nOwner: Dev Agent 1\nBranch: agent/1/a\nStatus: In Progress\n\nScope: work\n",
		"ARCHIVE.md": "# ARCHIVE\n\n## Done\n",
		"AGENTS.md":  "# Agents\n", "DEV_AGENT.md": "# Dev\n", "TEAM_LEAD_AGENT.md": "# Lead\n", "TECH.md": "# Tech\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	workspace := filepath.Join(root, "workspaces")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	lease := orchestrator.ControllerWorkerLease{Intent: orchestrator.ControllerTaskIntent{TaskID: "A", Role: "Dev Agent 1", Branch: "agent/1/a"}, LeaseID: strings.Repeat("a", 64), HeartbeatAt: now, Attempt: 1}
	statePath := filepath.Join(workspace, "controller-state.json")
	state := orchestrator.ControllerState{Version: orchestrator.ControllerStateVersion, Leases: []orchestrator.ControllerWorkerLease{lease}}
	save := func() {
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(statePath, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	binary := filepath.Join(t.TempDir(), "slopboss")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	run := func() ([]byte, error) {
		return exec.Command(binary, "--dir", root, "board", "resume", "A", "--reason", "rebase onto main").CombinedOutput()
	}
	before, err := os.ReadFile(filepath.Join(root, "TASKS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if output, err := run(); err == nil || !strings.Contains(string(output), "no terminal evidence") {
		t.Fatalf("unresolved worker: %v %s", err, output)
	}
	after, _ := os.ReadFile(filepath.Join(root, "TASKS.md"))
	if string(after) != string(before) {
		t.Fatal("unresolved request modified board")
	}
	state.Leases[0].Terminal = &orchestrator.ControllerTerminalResult{Outcome: "completed", FinishedAt: now, Evidence: "worker exited"}
	save()
	for i := 0; i < 2; i++ {
		if output, err := run(); err != nil {
			t.Fatalf("resume attempt %d: %v %s", i, err, output)
		}
	}
	data, _ := os.ReadFile(filepath.Join(root, "TASKS.md"))
	if strings.Count(string(data), "Continue after:") != 1 || !strings.Contains(string(data), "Continuation instructions: rebase onto main") {
		t.Fatalf("unexpected handoff: %s", data)
	}
}
