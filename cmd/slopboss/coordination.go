package main

import (
	"github.com/de-angelov/slopboss/internal/coordination"
	"github.com/spf13/cobra"
	"os"
)

var coordinationStatus, coordinationSummary string
var coordinationCmd = &cobra.Command{Use: "coordination", Short: "Report a Team Lead coordination result"}
var coordinationReportCmd = &cobra.Command{Use: "report", Short: "Record the outcome of the current coordination session", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
	return coordination.WriteReport(os.Getenv(coordination.ReportPathEnv), coordination.Report{Status: coordinationStatus, Summary: coordinationSummary})
}}

func init() {
	coordinationReportCmd.Flags().StringVar(&coordinationStatus, "status", "", "acted, idle, or blocked")
	coordinationReportCmd.Flags().StringVar(&coordinationSummary, "summary", "", "brief outcome or failure evidence")
	coordinationCmd.AddCommand(coordinationReportCmd)
	rootCmd.AddCommand(coordinationCmd)
}
