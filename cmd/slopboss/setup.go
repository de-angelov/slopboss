package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/provider"
	"github.com/de-angelov/slopboss/internal/setup"
)

var (
	setupRepoURL       string
	setupSSHURL        string
	setupProvider      string
	setupAgents        int
	setupSkipInterview bool
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Clone and prepare the per-agent workspaces",
	Long: `Create (or refresh) the team-lead workspace (repo-tl) and N dev-agent
workspaces (repo-agent-1..N) under the repo's workspaces/ directory, cloning the
product repository and pointing origin at its SSH URL, then scaffold the board
files.

By default it finishes with an interactive Team Lead tech-stack interview that
inspects the cloned repo and writes TECH.md; pass --skip-interview to skip it.

The number of dev agents chosen here is what "slopboss run" discovers at startup
by counting the repo-agent-* workspaces.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if setupAgents < 1 {
			return fmt.Errorf("--agents must be at least 1")
		}

		repoURL := strings.TrimSpace(setupRepoURL)
		if repoURL == "" {
			repoURL = promptForRepo()
		}
		if repoURL == "" {
			return fmt.Errorf("a product repository is required (pass --repo or answer the prompt)")
		}

		var interview provider.Provider
		if !setupSkipInterview {
			p, err := provider.ByName(setupProvider)
			if err != nil {
				return err
			}
			interview = p
		}

		return setup.Run(cmd.Context(), setup.Options{
			WorkspacesRoot: config.WorkspacesRoot,
			BoardRoot:      config.RepoRoot,
			RepoURL:        repoURL,
			RepoSSHURL:     setupSSHURL,
			DevAgents:      setupAgents,
			Interview:      interview,
		})
	},
}

// promptForRepo asks for the product repository URL on stdin when --repo was not
// given, so setup stays usable interactively without memorizing the flag.
func promptForRepo() string {
	fmt.Print("Product repository to clone (GitHub URL, HTTPS or SSH): ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return ""
	}
	return strings.TrimSpace(line)
}

func init() {
	setupCmd.Flags().StringVar(&setupRepoURL, "repo", "", "product repository to clone (required; prompted if omitted)")
	setupCmd.Flags().StringVar(&setupSSHURL, "ssh-url", "", "origin URL to set after cloning (defaults to --repo)")
	setupCmd.Flags().IntVar(&setupAgents, "agents", setup.DefaultDevAgents, "number of dev-agent workspaces to create")
	setupCmd.Flags().StringVar(&setupProvider, "provider", config.DefaultProviderName, "agent backend for the tech interview: codex or claude")
	setupCmd.Flags().BoolVar(&setupSkipInterview, "skip-interview", false, "skip the interactive tech-stack interview (leaves a placeholder TECH.md)")
}
