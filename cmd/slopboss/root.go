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
	// Applies to every subcommand: if --dir is given, repoint the board root
	// before the command runs.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if boardDir == "" {
			return nil
		}
		abs, err := filepath.Abs(boardDir)
		if err != nil {
			return err
		}
		config.SetRoot(abs)
		return nil
	},
}

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
