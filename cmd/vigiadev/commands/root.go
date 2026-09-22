package commands

import (
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	rootCmd = &cobra.Command{
		Use:   "vigiadev",
		Short: "vigiadev — Orquestrador e supervisor local determinístico para times modernos",
		Long: `vigiadev é um orquestrador de desenvolvimento local de alto desempenho.
Ele gerencia processos locais, Docker Compose, dependências via DAG,
remapeamento determinístico de portas e fornece telemetria em tempo real via TUI.`,
	}
)

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "Caminho do arquivo de configuração (ex: vigia.yaml)")
}
