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
	// RepoSSHURL defaults to RepoURL, and DevAgents defaults to DefaultDevAgents.
	o := Options{RepoURL: "git@example.com:acme/app.git"}.withDefaults()
	if o.RepoSSHURL != o.RepoURL {
		t.Fatalf("RepoSSHURL should default to RepoURL: %+v", o)
	}
	if o.DevAgents != DefaultDevAgents {
		t.Fatalf("DevAgents default = %d, want %d", o.DevAgents, DefaultDevAgents)
	}

	if got := (Options{DevAgents: 4}).withDefaults().DevAgents; got != 4 {
		t.Fatalf("explicit DevAgents overridden: got %d, want 4", got)
	}

	// An explicit SSH URL is preserved.
	if o := (Options{RepoURL: "https://x/y", RepoSSHURL: "git@x:y.git"}).withDefaults(); o.RepoSSHURL != "git@x:y.git" {
		t.Fatalf("explicit RepoSSHURL overridden: %+v", o)
	}

	// BaseBranch defaults to main, and an explicit value is preserved.
	if o := (Options{RepoURL: "x"}).withDefaults(); o.BaseBranch != "main" {
		t.Fatalf("BaseBranch default = %q, want main", o.BaseBranch)
	}
	if o := (Options{RepoURL: "x", BaseBranch: "develop"}).withDefaults(); o.BaseBranch != "develop" {
		t.Fatalf("explicit BaseBranch overridden: %q", o.BaseBranch)
	}
}
