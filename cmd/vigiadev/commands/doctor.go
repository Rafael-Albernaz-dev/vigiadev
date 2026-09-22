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
	Short: "Verifica a integridade do ambiente e pré-requisitos locais",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("🩺 vigiadev Doctor — Diagnóstico do Ambiente Local")
		fmt.Println("--------------------------------------------------")

		// 1. SO e Runtime
		fmt.Printf("✅ SO / Arquitetura: %s / %s (%d CPUs)\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())

		// 2. Docker daemon
		conn, err := net.DialTimeout("unix", "/var/run/docker.sock", 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			fmt.Println("✅ Docker Engine: Ativo e receptivo via /var/run/docker.sock")
		} else {
			fmt.Println("⚠️ Docker Engine: Não foi possível conectar ao socket Unix (/var/run/docker.sock)")
		}

		// 3. Docker Compose v2 CLI
		cmdCompose := exec.Command("docker", "compose", "version")
		if out, err := cmdCompose.Output(); err == nil {
			fmt.Printf("✅ Docker Compose v2: %s", string(out))
		} else {
			fmt.Println("⚠️ Docker Compose: Binário 'docker compose' não respondeu")
		}

		// 4. Git
		cmdGit := exec.Command("git", "version")
		if out, err := cmdGit.Output(); err == nil {
			fmt.Printf("✅ Git: %s", string(out))
		}

		fmt.Println("--------------------------------------------------")
		fmt.Println("✨ Diagnóstico concluído.")
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
