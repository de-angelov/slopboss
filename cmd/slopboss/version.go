package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the slopboss version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("slopboss", version)
	},
}
