// Package setup clones and prepares the per-agent product-repo workspaces that
// the orchestrator drives.
package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Default product repository the agents work in. Overridable via Options.
const (
	DefaultRepoURL    = "https://github.com/de-angelov/agent-task-test"
	DefaultRepoSSHURL = "git@github.com:de-angelov/agent-task-test.git"
)

// DefaultDevAgents is the number of dev-agent workspaces created when the count
// is not specified.
const DefaultDevAgents = 2

// Options configures a setup run.
type Options struct {
	// WorkspacesRoot is the directory the workspace clones live under.
	WorkspacesRoot string
	// BoardRoot is the directory the root board/config markdown files live in.
	// Missing files are scaffolded there; existing files are never overwritten.
	// Leave empty to skip board scaffolding.
	BoardRoot string
	// RepoURL is the HTTPS URL cloned for a fresh workspace.
	RepoURL string
	// RepoSSHURL is the origin URL set on each workspace after cloning.
	RepoSSHURL string
	// DevAgents is the number of dev-agent workspaces (repo-agent-1..N) to
	// create, in addition to the single team-lead workspace.
	DevAgents int
}

func (o Options) withDefaults() Options {
	if o.RepoURL == "" {
		o.RepoURL = DefaultRepoURL
	}
	if o.RepoSSHURL == "" {
		o.RepoSSHURL = DefaultRepoSSHURL
	}
	if o.DevAgents < 1 {
		o.DevAgents = DefaultDevAgents
	}
	return o
}

// workspaceDirs returns the workspace clone names for the given dev-agent count:
// a single team-lead workspace plus repo-agent-1..N.
func workspaceDirs(devAgents int) []string {
	dirs := []string{"repo-tl"}
	for i := 1; i <= devAgents; i++ {
		dirs = append(dirs, fmt.Sprintf("repo-agent-%d", i))
	}
	return dirs
}

// Run creates (or refreshes) each agent workspace under WorkspacesRoot.
func Run(opts Options) error {
	opts = opts.withDefaults()
	if opts.WorkspacesRoot == "" {
		return fmt.Errorf("setup: WorkspacesRoot is required")
	}

	fmt.Println("Setting up AI development workflow...")
	fmt.Println("Workspaces:", opts.WorkspacesRoot)
	fmt.Printf("Dev agents: %d\n", opts.DevAgents)

	if opts.BoardRoot != "" {
		fmt.Println()
		fmt.Println("Board files:", opts.BoardRoot)
		if _, err := scaffoldBoardFiles(opts.BoardRoot, opts.DevAgents); err != nil {
			return err
		}
		fmt.Println()
	}

	if err := mkdir(opts.WorkspacesRoot); err != nil {
		return err
	}

	for _, dir := range workspaceDirs(opts.DevAgents) {
		if err := createClone(opts, dir); err != nil {
			return err
		}
	}

	fmt.Println()
	fmt.Println("Setup complete.")
	fmt.Println("Next steps:")
	fmt.Println("  slopboss run")
	return nil
}

func createClone(opts Options, dir string) error {
	path := filepath.Join(opts.WorkspacesRoot, dir)

	if _, err := os.Stat(path); err == nil {
		fmt.Println("• exists:", dir)
	} else {
		if err := run(opts.WorkspacesRoot, "git", "clone", opts.RepoURL, dir); err != nil {
			return err
		}
		fmt.Println("✓ clone:", dir)
	}

	if err := ensureSSHRemote(path, opts.RepoSSHURL); err != nil {
		return err
	}
	if err := ensureMainBranch(path); err != nil {
		return err
	}
	return removeWorkspaceTaskBoard(path)
}

func ensureSSHRemote(repoPath, sshURL string) error {
	if err := run(repoPath, "git", "remote", "set-url", "origin", sshURL); err != nil {
		return err
	}
	fmt.Println("✓ remote:", sshURL)
	return nil
}

func ensureMainBranch(repoPath string) error {
	steps := [][]string{
		{"fetch", "origin", "main"},
		{"remote", "set-head", "origin", "main"},
		{"checkout", "main"},
		{"branch", "--set-upstream-to", "origin/main", "main"},
	}
	for _, args := range steps {
		if err := run(repoPath, "git", args...); err != nil {
			return err
		}
	}
	fmt.Println("✓ default branch: main")
	return nil
}

func removeWorkspaceTaskBoard(repoPath string) error {
	taskBoard := filepath.Join(repoPath, "TASKS.md")
	if err := os.Remove(taskBoard); err == nil {
		fmt.Println("✓ removed:", filepath.Join(filepath.Base(repoPath), "TASKS.md"))
	}
	return nil
}

func run(dir, name string, args ...string) error {
	fmt.Println("$", name, strings.Join(args, " "))

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command %q failed: %w", name, err)
	}
	return nil
}

func mkdir(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}
	fmt.Println("✓ dir:", filepath.Base(path))
	return nil
}
