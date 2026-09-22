package commands

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	Version   = "0.3.0-dev"
	Commit    = "none"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Exibe a versão e informações de build do vigiadev",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("vigiadev %s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
		fmt.Printf("  Commit:   %s\n", Commit)
		fmt.Printf("  BuildDate:%s\n", BuildDate)
		fmt.Printf("  Compiler: %s\n", runtime.Version())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
