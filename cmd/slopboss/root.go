package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "slopboss",
	Short: "slopboss orchestrates a multi-agent development workflow",
	Long: `slopboss drives a board-based multi-agent development workflow.

It reconciles a markdown task board against running agent sessions (Codex or
Claude Code), prepares the per-agent workspaces, and runs model/prompt
experiments.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command and maps errors to a non-zero exit code.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(runCmd, groomCmd, setupCmd, experimentCmd, versionCmd)
}
