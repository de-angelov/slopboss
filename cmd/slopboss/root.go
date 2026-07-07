package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/de-angelov/slopboss/internal/config"
)

// providerOrConfigured returns the --provider flag value, or the persisted
// default provider when the flag was left empty.
func providerOrConfigured(flag string) string {
	if strings.TrimSpace(flag) != "" {
		return flag
	}
	return config.Provider
}

// boardDir is the global --dir flag: the board directory to operate in. When
// unset, slopboss uses the current directory (resolved by walking up for the
// board marker files).
var boardDir string

var rootCmd = &cobra.Command{
	Use:   "slopboss",
	Short: "slopboss orchestrates a multi-agent development workflow",
	Long: `slopboss drives a board-based multi-agent development workflow.

It reconciles a markdown task board against running agent sessions (Codex or
Claude Code), prepares the per-agent workspaces, and runs model/prompt
experiments.

By default slopboss operates on the board in the current directory; use --dir to
point it at a board directory from anywhere.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	// Applies to every subcommand: resolve which board to operate on, then verify
	// board-requiring commands actually found one. Resolution order:
	//   1. --dir (explicit)
	//   2. the current directory, if it is a board
	//   3. the active board recorded by setup (global config)
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if boardDir != "" {
			abs, err := filepath.Abs(boardDir)
			if err != nil {
				return err
			}
			config.SetRoot(abs)
		}
		if cmd.Annotations["needsBoard"] != "true" {
			return nil
		}

		// Fall back to the remembered board when the current dir isn't one.
		if boardDir == "" && !config.IsBoardRoot() {
			if b := config.ActiveBoard(); b != "" {
				config.SetRoot(b)
				if config.IsBoardRoot() {
					fmt.Fprintf(os.Stderr, "Using board %s (from config; pass --dir to override)\n", config.RepoRoot)
				}
			}
		}
		if !config.IsBoardRoot() {
			return fmt.Errorf(
				"%s is not a slopboss board directory — cd into your board, or point at it with --dir <board> (create one with 'slopboss setup')",
				config.RepoRoot,
			)
		}
		return nil
	},
}

// needsBoard marks a command that must run against an existing board.
var needsBoard = map[string]string{"needsBoard": "true"}

// Execute runs the root command and maps errors to a non-zero exit code.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&boardDir, "dir", "", "board directory to operate in (default: current directory)")
	rootCmd.AddCommand(runCmd, groomCmd, setupCmd, experimentCmd, versionCmd)
}
