package setup

import (
	"strings"
	"testing"
)

func TestWorkspaceDirs(t *testing.T) {
	got := workspaceDirs(3)
	want := []string{"repo-tl", "repo-agent-1", "repo-agent-2", "repo-agent-3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("workspaceDirs(3) = %v, want %v", got, want)
	}

	if only := workspaceDirs(1); len(only) != 2 || only[0] != "repo-tl" || only[1] != "repo-agent-1" {
		t.Fatalf("workspaceDirs(1) = %v, want [repo-tl repo-agent-1]", only)
	}
}

func TestOptionsDefaults(t *testing.T) {
	o := Options{}.withDefaults()
	if o.RepoURL != DefaultRepoURL || o.RepoSSHURL != DefaultRepoSSHURL {
		t.Fatalf("default URLs not applied: %+v", o)
	}
	if o.DevAgents != DefaultDevAgents {
		t.Fatalf("DevAgents default = %d, want %d", o.DevAgents, DefaultDevAgents)
	}

	if got := (Options{DevAgents: 4}).withDefaults().DevAgents; got != 4 {
		t.Fatalf("explicit DevAgents overridden: got %d, want 4", got)
	}
}
