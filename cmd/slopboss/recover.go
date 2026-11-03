package main

import (
	"fmt"
	"strings"

	"github.com/de-angelov/slopboss/internal/orchestrator"
	"github.com/spf13/cobra"
)

var recoverConfirmed bool
var recoverRetry bool
var recoverInspect bool
var recoverReason string
var recoverCmd = &cobra.Command{
	Use:         "recover ROLE",
	Short:       "Resolve a worker lease after verifying the worker has stopped",
	Long:        "Record a stopped worker for an unresolved lease. First verify the worker and all its children have stopped; then supply --confirm-stopped. The controller must not be running.",
	Annotations: needsBoard,
	Args:        cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if recoverInspect {
			if recoverRetry || recoverConfirmed {
				return fmt.Errorf("--inspect cannot be combined with recovery options")
			}
			inspection, err := orchestrator.InspectWorkerLease(strings.TrimSpace(args[0]))
			if err != nil {
				return err
			}
			return writeJSON(inspection)
		}
		if recoverRetry {
			if recoverConfirmed {
				return fmt.Errorf("--retry and --confirm-stopped cannot be combined")
			}
			return orchestrator.ApproveTerminalRetry(strings.TrimSpace(args[0]), recoverReason)
		}
		if !recoverConfirmed {
			return fmt.Errorf("verify the worker and its children have stopped, then pass --confirm-stopped")
		}
		return orchestrator.RecoverStoppedRole(strings.TrimSpace(args[0]))
	},
}

func init() {
	recoverCmd.Flags().BoolVar(&recoverInspect, "inspect", false, "show recorded lease and process evidence without changing it")
	recoverCmd.Flags().BoolVar(&recoverRetry, "retry", false, "approve one retry of a blocked or failed terminal lease")
	recoverCmd.Flags().StringVar(&recoverReason, "reason", "", "reason for approving a terminal retry")
	recoverCmd.Flags().BoolVar(&recoverConfirmed, "confirm-stopped", false, "confirm the worker and its children have stopped")
	rootCmd.AddCommand(recoverCmd)
}
