package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/de-angelov/slopboss/internal/config"
)

var viewSession string

var viewCmd = &cobra.Command{
	Use:         "view",
	Short:       "Attach to the running Slopboss dashboard (read-only)",
	Annotations: needsBoard,
	Args:        cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		session := viewSession
		if session == "" {
			session = defaultViewSession(config.RepoRoot)
		}
		attach := exec.CommandContext(cmd.Context(), "tmux", "attach-session", "-t", session)
		attach.Stdin = os.Stdin
		attach.Stdout = os.Stdout
		attach.Stderr = os.Stderr
		if err := attach.Run(); err != nil {
			return fmt.Errorf("attach to dashboard session %q: %w (start or recreate it with 'slopboss run'; use 'slopboss run --detach' to start it without attaching)", session, err)
		}
		return nil
	},
}

func defaultViewSession(boardRoot string) string {
	name := strings.TrimSuffix(filepath.Base(boardRoot), "-board")
	return "slopboss-" + name
}

func init() {
	viewCmd.Flags().StringVar(&viewSession, "session", "", "tmux session to view (default: slopboss-<board>)")
	rootCmd.AddCommand(viewCmd)
}
