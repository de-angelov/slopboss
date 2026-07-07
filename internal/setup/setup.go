// Package setup clones and prepares the per-agent product-repo workspaces that
// the orchestrator drives, scaffolds the board/config files, and (unless skipped)
// runs an interactive Team Lead session to discover the product's tech stack.
package setup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/provider"
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
	// RepoURL is the product repository cloned for each workspace. Required.
	RepoURL string
	// RepoSSHURL is the origin URL set on each workspace after cloning. Defaults
	// to RepoURL when empty.
	RepoSSHURL string
	// DevAgents is the number of dev-agent workspaces (repo-agent-1..N) to
	// create, in addition to the single team-lead workspace.
	DevAgents int
	// Interview, when set, is the backend used for the interactive tech-stack
	// discovery session. Nil skips the interview and leaves the placeholder
	// TECH.md in place.
	Interview provider.Provider
}

func (o Options) withDefaults() Options {
	if o.RepoSSHURL == "" {
		o.RepoSSHURL = o.RepoURL
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

// Run creates (or refreshes) each agent workspace under WorkspacesRoot, scaffolds
// the board files, and (unless Interview is nil) runs the tech-stack interview.
func Run(ctx context.Context, opts Options) error {
	opts = opts.withDefaults()
	if opts.WorkspacesRoot == "" {
		return fmt.Errorf("setup: WorkspacesRoot is required")
	}
	if strings.TrimSpace(opts.RepoURL) == "" {
		return fmt.Errorf("setup: a product repository (--repo) is required")
	}

	fmt.Println("Setting up AI development workflow...")
	fmt.Println("Product repo:", opts.RepoURL)
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

	if opts.Interview != nil && opts.BoardRoot != "" {
		fmt.Println()
		if err := runTechInterview(ctx, opts); err != nil {
			return fmt.Errorf("tech interview: %w", err)
		}
	}

	fmt.Println()
	fmt.Println("Setup complete.")
	fmt.Println("Next steps:")
	if opts.Interview == nil {
		fmt.Println("  # fill in TECH.md (or re-run setup without --skip-interview)")
	}
	fmt.Println("  slopboss run")
	return nil
}

// runTechInterview launches an interactive Team Lead session, run at the board
// root so it can inspect the freshly cloned product repo under workspaces/repo-tl
// and write TECH.md next to the other board files.
func runTechInterview(ctx context.Context, opts Options) error {
	p := opts.Interview
	fmt.Printf("Starting %s tech-stack interview (writes %s)\n", p.Name(), filepath.Join(opts.BoardRoot, "TECH.md"))

	promptText := buildTechInterviewPrompt(opts)
	model := p.DefaultModel(config.TeamLeadRole)

	cmd := p.InteractiveCommand(ctx, model, promptText)
	cmd.Dir = opts.BoardRoot
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func buildTechInterviewPrompt(opts Options) string {
	agents := readOrEmpty(filepath.Join(opts.BoardRoot, "AGENTS.md"))
	teamLead := readOrEmpty(filepath.Join(opts.BoardRoot, "TEAM_LEAD_AGENT.md"))

	return fmt.Sprintf(`You are the Team Lead agent in an INTERACTIVE tech-stack discovery session.

================ AGENTS.md COMMON RULES ================

%s

================ TEAM LEAD INSTRUCTIONS ================

%s

================ TECH DISCOVERY SESSION ================

The product repository was just cloned to ./workspaces/repo-tl (relative to your
current directory). Your job is to produce ./TECH.md — the product's technical
standards and verification commands that dev agents will rely on.

Do this:
- Inspect ./workspaces/repo-tl to detect the stack: read manifests and configs
  (e.g. package.json, go.mod, pyproject.toml, Cargo.toml, Makefile, CI configs)
  and note the language/framework, package manager, and how to install, test,
  build, lint, and typecheck.
- Ask the user ONE targeted question at a time to confirm or fill gaps
  (preferred test command, conventions, directory layout, definition of done),
  and provide your recommended answer with each question.
- Do NOT modify any files under ./workspaces/**; only inspect them.
- When the stack is clear, WRITE ./TECH.md with at least these sections:
  Technology Stack, Install, Test, Build / Typecheck, Lint, and Key Conventions.
  Prefer exact commands over prose.
- Begin by summarizing what you detected from the repo, then ask your first
  question.
`, agents, teamLead)
}

func readOrEmpty(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "(not found)"
	}
	return string(data)
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
