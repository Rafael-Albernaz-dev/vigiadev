package commands

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check local environment prerequisites and system health",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("🩺 vigiadev Doctor — Local Environment Diagnostics")
		fmt.Println("--------------------------------------------------")

		// 1. OS & Runtime
		fmt.Printf("✅ OS / Architecture: %s / %s (%d CPUs)\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())

		// 2. Docker daemon
		conn, err := net.DialTimeout("unix", "/var/run/docker.sock", 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			fmt.Println("✅ Docker Engine: Active and responsive via /var/run/docker.sock")
		} else {
			fmt.Println("⚠️ Docker Engine: Could not connect to Unix socket (/var/run/docker.sock)")
		}

		// 3. Docker Compose v2 CLI
		cmdCompose := exec.Command("docker", "compose", "version")
		if out, err := cmdCompose.Output(); err == nil {
			fmt.Printf("✅ Docker Compose v2: %s", string(out))
		} else {
			fmt.Println("⚠️ Docker Compose: 'docker compose' binary did not respond")
		}

		// 4. Git
		cmdGit := exec.Command("git", "version")
		if out, err := cmdGit.Output(); err == nil {
			fmt.Printf("✅ Git: %s", string(out))
		}

		fmt.Println("--------------------------------------------------")
		fmt.Println("✨ Diagnostics complete.")
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
