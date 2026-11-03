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
	Use:         "recover [ROLE or TASK-ID]",
	Short:       "Resolve or retry a worker lease after failure, quota pause, or crash",
	Long:        "Record a stopped worker or approve a retry for an unresolved, failed, or blocked lease. You can pass either a role (e.g. \"Dev Agent 1\") or a task ID (e.g. \"TASK-078\").",
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
			if err := orchestrator.ApproveTerminalRetry(strings.TrimSpace(args[0]), recoverReason); err != nil {
				return err
			}
			fmt.Printf("Approved retry for %s\n", args[0])
			return nil
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
