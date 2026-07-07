// Package setup clones and prepares the per-agent product-repo workspaces that
// the orchestrator drives, scaffolds the board/config files, and (unless skipped)
// runs an interactive Team Lead session to discover the product's tech stack.
package setup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/provider"
)

// TechAnswers holds the user's answers to the native tech quiz; the agent turns
// them into TECH.md headlessly (no interactive TUI).
type TechAnswers struct {
	Product      string
	Stack        string
	Verification string
	Conventions  string
}

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
	// Interview, when set, is the backend used to synthesize TECH.md from Tech.
	// Nil (or a nil Tech) skips the quiz and leaves the placeholder TECH.md.
	Interview provider.Provider
	// Tech holds the native quiz answers the backend turns into TECH.md.
	Tech *TechAnswers
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

	if opts.Interview != nil && opts.Tech != nil && opts.BoardRoot != "" {
		fmt.Println()
		if err := writeTechFromAnswers(ctx, opts); err != nil {
			return fmt.Errorf("tech quiz: %w", err)
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

// writeTechFromAnswers runs the backend headlessly (no interactive TUI) to turn
// the native quiz answers into TECH.md. Its output goes to a buffer, never the
// terminal, so there is no TUI double-render; the agent writes ./TECH.md itself.
func writeTechFromAnswers(ctx context.Context, opts Options) error {
	p := opts.Interview
	fmt.Printf("Generating %s with %s from your answers...\n", filepath.Join(filepath.Base(opts.BoardRoot), "TECH.md"), p.Name())

	model := p.DefaultModel(config.TeamLeadRole)
	cmd := p.Command(ctx, model, 0) // headless
	cmd.Dir = opts.BoardRoot
	cmd.Stdin = strings.NewReader(techSynthesisPrompt(*opts.Tech))

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, tail(out.String(), 300))
	}

	fmt.Println("✓ wrote TECH.md")
	return nil
}

// techSynthesisPrompt tells the backend to write ./TECH.md from the answers,
// inferring the mechanical bits (exact commands, layout) rather than asking.
func techSynthesisPrompt(a TechAnswers) string {
	return fmt.Sprintf(`Write the file ./TECH.md (in your current working directory) for a new project, using the user's answers below. Do this task only: write that one file, then stop. Do not run other commands, do not print explanations.

Infer the exact install, test, build/typecheck, and lint commands and the conventional directory layout from the stack — fill them in concretely, do not leave placeholders.

Answers:
- Building: %s
- Stack: %s
- Verification / definition of done: %s
- Conventions / avoid: %s

Write ./TECH.md in EXACTLY this Markdown shape (omit any line that does not apply):

# TECH

<one short paragraph: what this product is>

## Technology Stack

- Language:
- Framework:
- Package manager / runtime:

## Commands

- Install:
- Test:
- Build / typecheck:
- Lint / format:

## Conventions

- Directory layout:
- Testing:
- Definition of done:
- Avoid:
`, emptyDash(a.Product), emptyDash(a.Stack), emptyDash(a.Verification), emptyDash(a.Conventions))
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(not specified — use your best judgement)"
	}
	return s
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
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
