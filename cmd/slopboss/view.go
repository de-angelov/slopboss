package main

import (
	"crypto/sha256"
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
			return fmt.Errorf("attach to dashboard session %q: %w (start it with 'slopboss run --detach')", session, err)
		}
		return nil
	},
}

func defaultViewSession(boardRoot string) string {
	root, err := filepath.Abs(boardRoot)
	if err != nil {
		root = filepath.Clean(boardRoot)
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	sum := sha256.Sum256([]byte(root))
	return fmt.Sprintf("%s-%x", legacyViewSession(root), sum[:6])
}

func legacyViewSession(boardRoot string) string {
	return "slopboss-" + strings.TrimSuffix(filepath.Base(boardRoot), "-board")
}

func init() {
	viewCmd.Flags().StringVar(&viewSession, "session", "", "tmux session to view (default: slopboss-<board>)")
	rootCmd.AddCommand(viewCmd)
}
