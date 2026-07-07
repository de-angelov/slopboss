package main

import (
	"github.com/spf13/cobra"

	"github.com/de-angelov/slopboss/internal/orchestrator"
	"github.com/de-angelov/slopboss/internal/provider"
)

var groomProvider string

var groomCmd = &cobra.Command{
	Use:   "groom",
	Short: "Start an interactive Team Lead session to groom the backlog",
	Long: `Launch the Team Lead agent interactively, preloaded with its instructions
(AGENTS.md, TEAM_LEAD_AGENT.md, TECH.md) and the current board, ready to capture
and prioritize new tasks in BACKLOG.md. This is a one-off grooming session,
separate from the autonomous "run" loop.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := provider.ByName(providerOrConfigured(groomProvider))
		if err != nil {
			return err
		}
		return orchestrator.Groom(cmd.Context(), p)
	},
}

func init() {
	groomCmd.Flags().StringVar(&groomProvider, "provider", "", "agent backend: codex or claude (default: configured provider)")
}
