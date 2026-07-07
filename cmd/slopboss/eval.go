package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/de-angelov/slopboss/internal/experiment"
	"github.com/de-angelov/slopboss/internal/provider"
)

var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Design and run model/prompt/backend evals",
	Long: `Design and run model/prompt/backend evals.

Use "eval groom" to design an eval interactively with the Team Lead (written to
EVAL.md), then "eval run" to execute it and collect a token/diff report.`,
}

var (
	evalConfig   string
	evalProvider string
	evalDryRun   bool
)

var evalRunCmd = &cobra.Command{
	Use:         "run",
	Annotations: needsBoard,
	Short:       "Run an eval from a config file",
	Long: `Run an eval: for each variant in the config, prepare an isolated git worktree,
run the configured backend against the ticket prompt, and collect token/diff
metrics into a report.

By default it runs the EVAL.md at the repo root (the file "eval groom" writes);
pass --config only to run a different file. The config may be Markdown or JSON.
The backend defaults to --provider, but the config and each variant may override
it, so a single run can compare codex against claude.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath := evalConfig
		if configPath == "" {
			configPath = experiment.EvalFilePath()
			if _, err := os.Stat(configPath); err != nil {
				return fmt.Errorf("no eval at %s — create one with 'slopboss eval groom', or pass --config <file>", configPath)
			}
		}

		cfg, err := experiment.ReadConfig(configPath)
		if err != nil {
			return fmt.Errorf("eval config error: %w", err)
		}

		run, err := experiment.Run(cmd.Context(), cfg, providerOrConfigured(evalProvider), evalDryRun)
		if err != nil {
			return fmt.Errorf("eval failed: %w", err)
		}

		fmt.Printf("Eval complete: %s\n", filepath.Join(cfg.ResolvedOutputDir(), run.Name, "report.md"))
		return nil
	},
}

var evalGroomProvider string

var evalGroomCmd = &cobra.Command{
	Use:         "groom",
	Annotations: needsBoard,
	Short:       "Interactively design an eval (EVAL.md) with the Team Lead",
	Long: `Launch the Team Lead agent interactively, preloaded with its instructions and
the current board, to help you design a model/prompt/backend eval and capture it
in EVAL.md — the same way "slopboss groom" curates the backlog.

This only authors the eval file; run it afterwards with "eval run".`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := provider.ByName(providerOrConfigured(evalGroomProvider))
		if err != nil {
			return err
		}
		return experiment.Groom(cmd.Context(), p)
	},
}

func init() {
	evalRunCmd.Flags().StringVar(&evalConfig, "config", "", "eval config to run; defaults to EVAL.md at the repo root")
	evalRunCmd.Flags().StringVar(&evalProvider, "provider", "", "default agent backend for variants: codex or claude (default: configured provider)")
	evalRunCmd.Flags().BoolVar(&evalDryRun, "dry-run", false, "prepare prompts and worktrees without running the backend")

	evalGroomCmd.Flags().StringVar(&evalGroomProvider, "provider", "", "agent backend: codex or claude (default: configured provider)")

	evalCmd.AddCommand(evalRunCmd, evalGroomCmd)
}
