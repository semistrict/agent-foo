package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var flagJSON bool

var rootCmd = &cobra.Command{
	Use:           "af",
	Short:         "af - AI agent toolkit",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "JSON output")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
