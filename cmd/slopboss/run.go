package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/orchestrator"
	"github.com/de-angelov/slopboss/internal/tui"
)

var runProvider string
var runDetach bool
var runTeamLeadMode string

var runCmd = &cobra.Command{
	Use:         "run",
	Annotations: needsBoard,
	Short:       "Run the orchestrator reconcile loop",
	Long: `Run the orchestrator loop: poll the task board, reconcile it against running
agent sessions, and start/stop backend sessions to match. Runs until
interrupted (SIGINT/SIGTERM), then cancels in-flight sessions and exits.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if runTeamLeadMode != "planner" && runTeamLeadMode != "agent" {
			return fmt.Errorf("invalid team lead mode %q (use planner or agent)", runTeamLeadMode)
		}
		if runDetach {
			return runManaged(cmd.Context(), runProvider, runTeamLeadMode)
		}
		return orchestrator.RunLoopWithMode(cmd.Context(), providerOrConfigured(runProvider), runTeamLeadMode, tui.New())
	},
}

func init() {
	runCmd.Flags().StringVar(&runProvider, "provider", "", "agent backend: codex, claude, opencode, or agy (default: configured provider)")
	runCmd.Flags().StringVar(&runTeamLeadMode, "team-lead-mode", "planner", "team lead coordination: planner or agent (experimental)")
	runCmd.Flags().BoolVar(&runDetach, "detach", false, "start or reuse the managed dashboard without attaching")
}

func runManaged(ctx context.Context, provider, mode string) error {
	session := defaultViewSession(config.RepoRoot)
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux is required to manage the dashboard; install tmux and retry: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate slopboss executable for managed session: %w", err)
	}
	args := managedSessionCommand(executable, config.RepoRoot, provider, mode)
	create := exec.CommandContext(ctx, "tmux", managedSessionArgs(session, config.RepoRoot, args)...)
	create.Stdin, create.Stdout, create.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := create.Run(); err != nil {
		return fmt.Errorf("start managed dashboard session %q with tmux: %w", session, err)
	}
	return nil
}

func managedSessionCommand(executable, boardRoot, provider string, mode ...string) []string {
	args := []string{"--dir", boardRoot, "run"}
	if provider != "" {
		args = append(args, "--provider", provider)
	}
	if len(mode) > 0 && mode[0] != "" && mode[0] != "planner" {
		args = append(args, "--team-lead-mode", mode[0])
	}
	return append([]string{executable}, args...)
}

func managedSessionArgs(session, boardRoot string, command []string) []string {
	args := []string{"new-session", "-Ad", "-s", session, "-c", boardRoot}
	return append(args, command...)
}
