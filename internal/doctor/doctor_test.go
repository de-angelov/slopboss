package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, broken bool) Options {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"workspaces/repo-tl", "workspaces/repo-agent-1"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	content := map[string]string{
		"BACKLOG.md": "## Backlog\n\n### Pending\nTask ID: A-1\nOwner: Unassigned\nStatus: Backlog\nDependencies: DONE-1\n",
		"TASKS.md":   "## Dev Agent 1 In Progress\n\n### Active\nTask ID: B-1\nOwner: Dev Agent 1\nStatus: In Progress\nDependencies: none\n",
		"ARCHIVE.md": "## Done\n\n### Finished\nTask ID: DONE-1\nOwner: Dev Agent 1\nStatus: Done\n",
	}
	if broken {
		content["TASKS.md"] = "## Dev Agent 1 In Progress\n\n### Active\nTask ID: B-1\nOwner: Wrong\nStatus: Ready\nDependencies: GONE-1\n"
	}
	return Options{Root: root, Provider: "codex", DevAgents: 1,
		ReadFile: func(p string) ([]byte, error) {
			v, ok := content[filepath.Base(p)]
			if !ok {
				return nil, os.ErrNotExist
			}
			return []byte(v), nil
		},
		LookPath: func(string) (string, error) { return "/fake/tool", nil },
		Run:      func(string, ...string) error { return nil },
	}
}

func TestDoctorHealthy(t *testing.T) {
	findings := Check(fixture(t, false))
	for _, f := range findings {
		if f.Level == "FAIL" {
			t.Fatalf("unexpected failure: %+v", f)
		}
	}
}

func TestDoctorFailuresAreActionable(t *testing.T) {
	o := fixture(t, true)
	o.LookPath = func(n string) (string, error) {
		if n == "git" {
			return "", errors.New("missing")
		}
		return "", errors.New("missing")
	}
	o.Stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	findings := Check(o)
	got := strings.Builder{}
	for _, f := range findings {
		if f.Level == "FAIL" {
			got.WriteString(f.Message + "\n")
		}
	}
	for _, want := range []string{"invalid lifecycle metadata", "GONE-1", "install it or put it on PATH", "slopboss setup"} {
		if !strings.Contains(got.String(), want) {
			t.Errorf("failure output missing %q:\n%s", want, got.String())
		}
	}
}

func TestOriginProbesUseTeamLeadWorkspace(t *testing.T) {
	o := fixture(t, false)
	workspace := filepath.Join(o.Root, "workspaces", "repo-tl")
	var gitCalls [][]string
	o.Run = func(name string, args ...string) error {
		if name == "/fake/tool" && len(args) > 0 && args[0] == "-C" {
			gitCalls = append(gitCalls, append([]string(nil), args...))
		}
		return nil
	}
	findings := Check(o)
	if len(gitCalls) != 2 {
		t.Fatalf("expected two git probes, got %v", gitCalls)
	}
	for _, args := range gitCalls {
		if len(args) < 3 || args[1] != workspace {
			t.Errorf("git probe did not use Team Lead workspace %q: %v", workspace, args)
		}
	}
	for _, f := range findings {
		if f.Check == "origin" && f.Level == "FAIL" {
			t.Fatalf("unexpected origin failure: %+v", f)
		}
	}
}

func TestMissingWorkspaceOriginIsActionable(t *testing.T) {
	o := fixture(t, false)
	workspace := filepath.Join(o.Root, "workspaces", "repo-tl")
	o.Run = func(name string, args ...string) error {
		if len(args) >= 4 && args[0] == "-C" && args[1] == workspace && args[2] == "remote" {
			return errors.New("no origin")
		}
		return nil
	}
	findings := Check(o)
	for _, f := range findings {
		if f.Check == "origin" && f.Level == "FAIL" {
			if !strings.Contains(f.Message, workspace) || !strings.Contains(f.Message, "git -C") || !strings.Contains(f.Message, "remote add origin") {
				t.Fatalf("workspace origin failure is not actionable: %+v", f)
			}
			return
		}
	}
	t.Fatal("expected missing workspace origin failure")
}
