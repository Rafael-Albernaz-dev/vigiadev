package commands

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

var (
	Version   = "1.1.0"
	Commit    = "none"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version and build information for vigiadev",
	Run: func(cmd *cobra.Command, args []string) {
		info, _ := debug.ReadBuildInfo()
		commit, buildDate := resolveBuildMetadata(Commit, BuildDate, info)
		fmt.Printf("vigiadev %s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
		fmt.Printf("  Commit:   %s\n", commit)
		fmt.Printf("  BuildDate:%s\n", buildDate)
		fmt.Printf("  Compiler: %s\n", runtime.Version())
	},
}

// Explicit release metadata takes precedence over Go's embedded VCS settings.
func resolveBuildMetadata(commit, buildDate string, info *debug.BuildInfo) (string, string) {
	if info == nil {
		return commit, buildDate
	}
	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if commit == "none" && setting.Value != "" {
				commit = setting.Value
			}
		case "vcs.time":
			if buildDate == "unknown" && setting.Value != "" {
				buildDate = setting.Value
			}
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if modified && commit != "none" && !strings.HasSuffix(commit, "-dirty") {
		commit += "-dirty"
	}
	return commit, buildDate
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
