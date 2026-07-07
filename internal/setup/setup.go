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
const DefaultDevAgents = config.DefaultDevAgents

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
	// BaseBranch is the product integration branch dev agents branch from and
	// merge into. It is persisted to CONFIG.md and created on the remote if
	// missing. Defaults to "main".
	BaseBranch string
	// Provider is the default agent backend (codex/claude) persisted to CONFIG.md
	// so run/groom/experiment default to it without a flag.
	Provider string
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
	if strings.TrimSpace(o.BaseBranch) == "" {
		o.BaseBranch = "main"
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

	if opts.BoardRoot != "" && isSlopbossSourceTree(opts.BoardRoot) {
		return fmt.Errorf("refusing to scaffold into the slopboss source tree (%s) — run setup from a separate orchestrator/board directory", opts.BoardRoot)
	}

	fmt.Println("Setting up AI development workflow...")
	fmt.Println("Product repo:", opts.RepoURL)
	fmt.Println("Base branch:", opts.BaseBranch)
	fmt.Println("Workspaces:", opts.WorkspacesRoot)
	fmt.Printf("Dev agents: %d\n", opts.DevAgents)

	if opts.BoardRoot != "" {
		fmt.Println()
		fmt.Println("Board files:", opts.BoardRoot)
		if _, err := scaffoldBoardFiles(opts.BoardRoot, opts.DevAgents, opts.BaseBranch); err != nil {
			return err
		}
		if err := config.SaveSettings(config.Settings{
			RepoURL:    opts.RepoURL,
			BaseBranch: opts.BaseBranch,
			Provider:   opts.Provider,
			DevAgents:  opts.DevAgents,
		}); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Println("✓ create:", filepath.Base(config.ConfigFilePath()))
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
		// Reuse an existing workspace only if it points at the requested repo;
		// otherwise it is a stale clone from a previous product and reusing it
		// silently would run agents against the wrong code.
		if origin, err := gitOutput(path, "remote", "get-url", "origin"); err == nil {
			if got := strings.TrimSpace(origin); got != opts.RepoURL && got != opts.RepoSSHURL {
				return fmt.Errorf("workspace %s already exists but its origin is %s, not %s — remove %s and re-run", dir, got, opts.RepoURL, path)
			}
		}
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
	if err := ensureBaseBranch(path, opts.BaseBranch); err != nil {
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

// ensureBaseBranch makes sure the product repo has the chosen base branch and the
// workspace is on it. If the branch does not exist on origin it is created: an
// empty repo gets a clean initial commit, and a non-empty repo branches from what
// was cloned. Dev-agent branching, squash-merges, and merge detection all target
// this branch.
func ensureBaseBranch(repoPath, branch string) error {
	branchRef, err := gitOutput(repoPath, "ls-remote", "--heads", "origin", branch)
	if err != nil {
		return fmt.Errorf("cannot reach the product repository's origin (check the URL and your git access): %w", err)
	}

	if strings.TrimSpace(branchRef) == "" {
		if err := createBaseBranch(repoPath, branch); err != nil {
			return err
		}
	} else if err := checkoutBaseBranch(repoPath, branch); err != nil {
		return err
	}

	return nil
}

// createBaseBranch creates and pushes the base branch. For an empty remote it
// requires an empty local clone too (guarding against pushing a stale workspace's
// unrelated history), and seeds a clean initial commit; for a non-empty remote it
// branches from whatever was cloned.
func createBaseBranch(repoPath, branch string) error {
	remoteEmpty := false
	if refs, _ := gitOutput(repoPath, "ls-remote", "origin"); strings.TrimSpace(refs) == "" {
		remoteEmpty = true
	}

	_, headErr := gitOutput(repoPath, "rev-parse", "--verify", "HEAD")
	hasLocalCommits := headErr == nil

	if remoteEmpty {
		if hasLocalCommits {
			return fmt.Errorf("%s has local commits but origin is empty — remove the workspace and re-run so setup can initialize %q cleanly", repoPath, branch)
		}
		fmt.Printf("• product repo is empty; initializing %q\n", branch)
		steps := [][]string{
			{"checkout", "-B", branch},
			{"commit", "--allow-empty", "-m", "chore: initialize repository"},
			{"push", "-u", "origin", branch},
		}
		if err := runSteps(repoPath, steps); err != nil {
			return err
		}
		fmt.Println("✓ created base branch:", branch)
		return nil
	}

	// Remote has history but not this branch: create it from the cloned HEAD.
	fmt.Printf("• branch %q not found on origin; creating it from the cloned HEAD\n", branch)
	steps := [][]string{
		{"checkout", "-B", branch},
		{"push", "-u", "origin", branch},
	}
	if err := runSteps(repoPath, steps); err != nil {
		return err
	}
	fmt.Println("✓ created base branch:", branch)
	return nil
}

func checkoutBaseBranch(repoPath, branch string) error {
	steps := [][]string{
		{"fetch", "origin", branch},
		{"remote", "set-head", "origin", branch},
		{"checkout", branch},
		{"branch", "--set-upstream-to", "origin/" + branch, branch},
	}
	if err := runSteps(repoPath, steps); err != nil {
		return err
	}
	fmt.Println("✓ base branch:", branch)
	return nil
}

func runSteps(repoPath string, steps [][]string) error {
	for _, args := range steps {
		if err := run(repoPath, "git", args...); err != nil {
			return err
		}
	}
	return nil
}

// gitOutput runs a git command in dir and returns its stdout.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// isSlopbossSourceTree reports whether dir is the slopboss tool's own source
// checkout, so setup can refuse to scaffold board files into it.
func isSlopbossSourceTree(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "module github.com/de-angelov/slopboss")
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
