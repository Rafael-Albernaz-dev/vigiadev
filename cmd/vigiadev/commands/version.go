package commands

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	Version   = "1.0.0"
	Commit    = "none"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version and build information for vigiadev",
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
