package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/setup"
)

var (
	setupRepoURL string
	setupSSHURL  string
	setupAgents  int
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Clone and prepare the per-agent workspaces",
	Long: `Create (or refresh) the team-lead workspace (repo-tl) and N dev-agent
workspaces (repo-agent-1..N) under the repo's workspaces/ directory, cloning the
product repository and pointing origin at its SSH URL.

The number of dev agents chosen here is what "slopboss run" discovers at startup
by counting the repo-agent-* workspaces.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if setupAgents < 1 {
			return fmt.Errorf("--agents must be at least 1")
		}
		return setup.Run(setup.Options{
			WorkspacesRoot: config.WorkspacesRoot,
			BoardRoot:      config.RepoRoot,
			RepoURL:        setupRepoURL,
			RepoSSHURL:     setupSSHURL,
			DevAgents:      setupAgents,
		})
	},
}

func init() {
	setupCmd.Flags().StringVar(&setupRepoURL, "repo", setup.DefaultRepoURL, "product repo HTTPS URL to clone")
	setupCmd.Flags().StringVar(&setupSSHURL, "ssh-url", setup.DefaultRepoSSHURL, "origin SSH URL to set after cloning")
	setupCmd.Flags().IntVar(&setupAgents, "agents", setup.DefaultDevAgents, "number of dev-agent workspaces to create")
}
