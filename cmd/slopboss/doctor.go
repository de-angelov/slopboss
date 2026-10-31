package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/de-angelov/slopboss/internal/config"
	"github.com/de-angelov/slopboss/internal/doctor"
)

var doctorCmd = &cobra.Command{
	Use:         "doctor",
	Short:       "Check whether this board can be operated safely",
	Annotations: needsBoard,
	RunE: func(cmd *cobra.Command, args []string) error {
		findings := doctor.Check(doctor.Options{Root: config.RepoRoot, Provider: config.Provider, DevAgents: config.DevAgentCount})
		failed := false
		for _, f := range findings {
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s: %s\n", f.Level, f.Check, f.Message)
			if f.Level == "FAIL" {
				failed = true
			}
		}
		if failed {
			return fmt.Errorf("doctor found required checks that need attention")
		}
		return nil
	},
}

func init() { rootCmd.AddCommand(doctorCmd) }
