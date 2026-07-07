package main

import (
	"github.com/spf13/cobra"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/orchestrator"
	"github.com/de-angelov/slopboss/internal/tui"
)

var runProvider string

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the orchestrator reconcile loop",
	Long: `Run the orchestrator loop: poll the task board, reconcile it against running
agent sessions, and start/stop backend sessions to match. Runs until
interrupted (SIGINT/SIGTERM), then cancels in-flight sessions and exits.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return orchestrator.RunLoop(cmd.Context(), runProvider, tui.New())
	},
}

func init() {
	runCmd.Flags().StringVar(&runProvider, "provider", config.DefaultProviderName, "agent backend to use: codex or claude")
}
