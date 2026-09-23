package commands

import (
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	rootCmd = &cobra.Command{
		Use:   "vigiadev",
		Short: "vigiadev — Deterministic local development orchestrator and supervisor",
		Long: `vigiadev is a high-performance local development orchestrator.
It manages native host processes, Docker Compose, DAG dependencies,
deterministic port remapping, and provides real-time telemetry via TUI.`,
	}
)

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "Path to configuration file (e.g. vigiadev.yaml)")
}
