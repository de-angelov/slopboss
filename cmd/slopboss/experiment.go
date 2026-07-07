package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/de-angelov/slopboss/internal/experiment"
)

var (
	experimentConfig string
	experimentDryRun bool
)

var experimentCmd = &cobra.Command{
	Use:   "experiment",
	Short: "Run a model/prompt A/B experiment from a JSON config",
	Long: `Run an experiment: for each variant in the JSON config, prepare an isolated
git worktree, run the configured backend against the ticket prompt, and collect
token/diff metrics into a report.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if experimentConfig == "" {
			return fmt.Errorf("--config is required (e.g. --config ../experiments/example-agent-loop.json)")
		}

		cfg, err := experiment.ReadConfig(experimentConfig)
		if err != nil {
			return fmt.Errorf("experiment config error: %w", err)
		}

		run, err := experiment.Run(cmd.Context(), cfg, experimentDryRun)
		if err != nil {
			return fmt.Errorf("experiment failed: %w", err)
		}

		fmt.Printf("Experiment complete: %s\n", filepath.Join(cfg.ResolvedOutputDir(), run.Name, "report.md"))
		return nil
	},
}

func init() {
	experimentCmd.Flags().StringVar(&experimentConfig, "config", "", "path to experiment JSON config")
	experimentCmd.Flags().BoolVar(&experimentDryRun, "dry-run", false, "prepare prompts and worktrees without running the backend")
}
