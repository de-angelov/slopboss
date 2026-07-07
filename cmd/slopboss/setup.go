package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/provider"
	"github.com/de-angelov/slopboss/internal/setup"
)

var (
	setupRepoURL       string
	setupSSHURL        string
	setupBranch        string
	setupProvider      string
	setupAgents        int
	setupSkipInterview bool
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive wizard to clone workspaces and scaffold the board",
	Long: `Run an interactive wizard (like "npm init") that asks for the board directory,
product repository, base branch, number of dev agents, and agent backend, then:
clones the team-lead (repo-tl) and dev-agent (repo-agent-1..N) workspaces, creates
the base branch if the repo doesn't have it, scaffolds the board files + CONFIG.md,
and (unless declined) runs the Team Lead tech-stack interview that writes TECH.md.

Any answer provided as a flag is not prompted for, so setup can also run fully
non-interactively.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		reader := bufio.NewReader(os.Stdin)

		// 1. Board directory — everything is scaffolded here and later commands run
		//    from it. When --dir is given the root PersistentPreRunE already applied
		//    it; otherwise prompt (default cwd) and repoint.
		if boardDir == "" {
			cwd, _ := os.Getwd()
			dir := ask(reader, "Board directory", cwd)
			absDir, err := filepath.Abs(strings.TrimSpace(dir))
			if err != nil {
				return err
			}
			config.SetRoot(absDir)
		}
		if err := os.MkdirAll(config.RepoRoot, 0755); err != nil {
			return err
		}

		// 2. Product repository (required; defaults to the persisted value on re-run).
		repoURL := setupRepoURL
		if !cmd.Flags().Changed("repo") {
			repoURL = ask(reader, "Product repository (GitHub URL, HTTPS or SSH)", config.RepoURL)
		}
		repoURL = strings.TrimSpace(repoURL)
		if repoURL == "" {
			return fmt.Errorf("a product repository is required")
		}

		// 3. Base branch (created if missing).
		branch := setupBranch
		if !cmd.Flags().Changed("branch") {
			branch = ask(reader, "Base/integration branch (created if missing)", config.BaseBranch)
		}

		// 4. Dev agents.
		agents := setupAgents
		if !cmd.Flags().Changed("agents") {
			agents = askInt(reader, "Number of dev agents", config.DevAgentCount)
		}
		if agents < 1 {
			return fmt.Errorf("number of dev agents must be at least 1")
		}

		// 5. Agent backend (persisted so run/groom/experiment default to it).
		providerName := setupProvider
		if !cmd.Flags().Changed("provider") {
			providerName = ask(reader, "Agent backend (codex/claude)", config.Provider)
		}
		if _, err := provider.ByName(providerName); err != nil {
			return err
		}

		// 6. Tech interview — the backend drives an adaptive Q&A (slopboss relays
		//    each question/answer natively, so no agent-TUI double-render), then it
		//    writes TECH.md.
		runQuiz := !setupSkipInterview
		if !cmd.Flags().Changed("skip-interview") {
			runQuiz = askYesNo(reader, "Run the tech-stack interview now?", true)
		}
		var interview provider.Provider
		if runQuiz {
			interview, _ = provider.ByName(providerName) // already validated above
		}

		return setup.Run(cmd.Context(), setup.Options{
			WorkspacesRoot: config.WorkspacesRoot,
			BoardRoot:      config.RepoRoot,
			RepoURL:        repoURL,
			RepoSSHURL:     setupSSHURL,
			DevAgents:      agents,
			BaseBranch:     branch,
			Provider:       providerName,
			Interview:      interview,
		})
	},
}

// ask prompts for a string, showing the default in brackets and returning it when
// the user just presses Enter.
func ask(r *bufio.Reader, label, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	line, _ := r.ReadString('\n')
	if line = strings.TrimSpace(line); line != "" {
		return line
	}
	return def
}

func askInt(r *bufio.Reader, label string, def int) int {
	if n, err := strconv.Atoi(ask(r, label, strconv.Itoa(def))); err == nil {
		return n
	}
	return def
}

func askYesNo(r *bufio.Reader, label string, def bool) bool {
	hint := "Y/n"
	if !def {
		hint = "y/N"
	}
	fmt.Printf("%s [%s]: ", label, hint)
	line, _ := r.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes":
		return true
	default:
		return false
	}
}

func init() {
	setupCmd.Flags().StringVar(&setupRepoURL, "repo", "", "product repository to clone (prompted if omitted)")
	setupCmd.Flags().StringVar(&setupSSHURL, "ssh-url", "", "origin URL to set after cloning (defaults to --repo)")
	setupCmd.Flags().StringVar(&setupBranch, "branch", "", "base/integration branch agents target; created if missing (prompted if omitted)")
	setupCmd.Flags().IntVar(&setupAgents, "agents", config.DevAgentCount, "number of dev-agent workspaces to create")
	setupCmd.Flags().StringVar(&setupProvider, "provider", "", "agent backend to use and persist: codex or claude (default: configured provider)")
	setupCmd.Flags().BoolVar(&setupSkipInterview, "skip-interview", false, "skip the tech-stack interview (leaves a placeholder TECH.md)")
}
