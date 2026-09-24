package commands

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/spf13/cobra"
)

type portConflict struct {
	Service string
	Port    int
}

func findPortConflicts(cfg *domain.VigiaConfig) []portConflict {
	var conflicts []portConflict
	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, port := range cfg.Services[name].Ports {
			listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				conflicts = append(conflicts, portConflict{Service: name, Port: port})
				continue
			}
			_ = listener.Close()
		}
	}
	return conflicts
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check local environment prerequisites and system health",
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "🩺 vigiadev Doctor — Local Environment Diagnostics")
		fmt.Fprintln(out, "--------------------------------------------------")

		// 1. OS & Runtime
		fmt.Fprintf(out, "✅ OS / Architecture: %s / %s (%d CPUs)\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())

		// 2. Docker daemon
		conn, err := net.DialTimeout("unix", "/var/run/docker.sock", 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			fmt.Fprintln(out, "✅ Docker Engine: Active and responsive via /var/run/docker.sock")
		} else {
			fmt.Fprintln(out, "⚠️ Docker Engine: Could not connect to Unix socket (/var/run/docker.sock)")
		}

		// 3. Docker Compose v2 CLI
		cmdCompose := exec.Command("docker", "compose", "version")
		if composeOut, err := cmdCompose.Output(); err == nil {
			fmt.Fprintf(out, "✅ Docker Compose v2: %s", string(composeOut))
		} else {
			fmt.Fprintln(out, "⚠️ Docker Compose: 'docker compose' binary did not respond")
		}

		// 4. Git
		cmdGit := exec.Command("git", "version")
		if gitOut, err := cmdGit.Output(); err == nil {
			fmt.Fprintf(out, "✅ Git: %s", string(gitOut))
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		cfgPath, cfgErr := config.FindConfigFile(cwd, cfgFile)
		if cfgErr == nil {
			cfg, err := config.LoadConfig(cfgPath)
			if err != nil {
				return err
			}
			conflicts := findPortConflicts(cfg)
			if len(conflicts) == 0 {
				fmt.Fprintln(out, "✅ Configured host ports: available")
			} else {
				for _, conflict := range conflicts {
					fmt.Fprintf(out, "⚠️ Port conflict: service '%s' declares TCP port %d, already occupied on 127.0.0.1\n", conflict.Service, conflict.Port)
				}
			}
		} else {
			fmt.Fprintln(out, "ℹ️ No vigiaDev YAML found; skipped configured port checks.")
		}

		fmt.Fprintln(out, "--------------------------------------------------")
		fmt.Fprintln(out, "✨ Diagnostics complete.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
