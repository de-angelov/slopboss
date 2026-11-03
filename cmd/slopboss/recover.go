package main

import (
	"fmt"
	"strings"

	"github.com/de-angelov/slopboss/internal/orchestrator"
	"github.com/spf13/cobra"
)

var recoverConfirmed bool
var recoverCmd = &cobra.Command{
	Use:         "recover ROLE",
	Short:       "Resolve a worker lease after verifying the worker has stopped",
	Long:        "Record a stopped worker for an unresolved lease. First verify the worker and all its children have stopped; then supply --confirm-stopped. The controller must not be running.",
	Annotations: needsBoard,
	Args:        cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !recoverConfirmed {
			return fmt.Errorf("verify the worker and its children have stopped, then pass --confirm-stopped")
		}
		return orchestrator.RecoverStoppedRole(strings.TrimSpace(args[0]))
	},
}

func init() {
	recoverCmd.Flags().BoolVar(&recoverConfirmed, "confirm-stopped", false, "confirm the worker and its children have stopped")
	rootCmd.AddCommand(recoverCmd)
}
