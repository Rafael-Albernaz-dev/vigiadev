package commands

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/spf13/cobra"
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Interrompe todos os serviços da sessão ativa e limpa os recursos",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		m, err := manifest.ReadManifest(cwd)
		if err != nil {
			fmt.Println("ℹ️ Nenhuma sessão ativa do vigiaDev encontrada (.vigiadev/run.json ausente).")
			return nil
		}

		fmt.Printf("⏹️ Encerrando sessão '%s' do projeto '%s'...\n", m.RunID, m.ProjectName)

		for name, svc := range m.Services {
			if svc.PGID > 0 {
				fmt.Printf("  • Parando serviço '%s' (PGID %d)...\n", name, svc.PGID)
				_ = syscall.Kill(-svc.PGID, syscall.SIGTERM)
			} else if svc.PID > 0 {
				_ = syscall.Kill(svc.PID, syscall.SIGTERM)
			}
		}

		time.Sleep(500 * time.Millisecond)

		// Força SIGKILL se ainda houver resíduo
		for _, svc := range m.Services {
			if svc.PGID > 0 {
				_ = syscall.Kill(-svc.PGID, syscall.SIGKILL)
			}
		}

		_ = manifest.RemoveManifest(cwd)
		_ = manifest.ReleaseLock(cwd)

		fmt.Println("✨ Todos os serviços foram encerrados e recursos liberados.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(downCmd)
}
