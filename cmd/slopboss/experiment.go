package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/de-angelov/slopboss/internal/experiment"
	"github.com/de-angelov/slopboss/internal/provider"
)

var experimentCmd = &cobra.Command{
	Use:   "experiment",
	Short: "Design and run model/prompt/backend experiments",
	Long: `Design and run model/prompt/backend experiments.

Use "experiment groom" to author an experiment interactively with the Team Lead
(written to EXPERIMENT.md), then "experiment run" to execute it and collect a
token/diff report.`,
}

var (
	experimentConfig   string
	experimentProvider string
	experimentDryRun   bool
)

var experimentRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run an experiment from a config file",
	Long: `Run an experiment: for each variant in the config, prepare an isolated git
worktree, run the configured backend against the ticket prompt, and collect
token/diff metrics into a report.

By default it runs the EXPERIMENT.md at the repo root (the file "experiment
groom" writes); pass --config only to run a different file. The config may be
Markdown or JSON. The backend defaults to --provider, but the config and each
variant may override it, so a single run can compare codex against claude.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath := experimentConfig
		if configPath == "" {
			configPath = experiment.ExperimentFilePath()
			if _, err := os.Stat(configPath); err != nil {
				return fmt.Errorf("no experiment at %s — create one with 'slopboss experiment groom', or pass --config <file>", configPath)
			}
		}

		cfg, err := experiment.ReadConfig(configPath)
		if err != nil {
			return fmt.Errorf("experiment config error: %w", err)
		}

		run, err := experiment.Run(cmd.Context(), cfg, providerOrConfigured(experimentProvider), experimentDryRun)
		if err != nil {
			return fmt.Errorf("experiment failed: %w", err)
		}

		fmt.Printf("Experiment complete: %s\n", filepath.Join(cfg.ResolvedOutputDir(), run.Name, "report.md"))
		return nil
	},
}

var experimentGroomProvider string

var experimentGroomCmd = &cobra.Command{
	Use:   "groom",
	Short: "Interactively design an experiment (EXPERIMENT.md) with the Team Lead",
	Long: `Launch the Team Lead agent interactively, preloaded with its instructions and
the current board, to help you design a model/prompt/backend experiment and
capture it in EXPERIMENT.md — the same way "slopboss groom" curates the backlog.

This only authors the experiment file; run it afterwards with "experiment run".`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := provider.ByName(providerOrConfigured(experimentGroomProvider))
		if err != nil {
			return err
		}
		return experiment.Groom(cmd.Context(), p)
	},
}

func init() {
	experimentRunCmd.Flags().StringVar(&experimentConfig, "config", "", "experiment config to run; defaults to EXPERIMENT.md at the repo root")
	experimentRunCmd.Flags().StringVar(&experimentProvider, "provider", "", "default agent backend for variants: codex or claude (default: configured provider)")
	experimentRunCmd.Flags().BoolVar(&experimentDryRun, "dry-run", false, "prepare prompts and worktrees without running the backend")

	experimentGroomCmd.Flags().StringVar(&experimentGroomProvider, "provider", "", "agent backend: codex or claude (default: configured provider)")

	experimentCmd.AddCommand(experimentRunCmd, experimentGroomCmd)
}
