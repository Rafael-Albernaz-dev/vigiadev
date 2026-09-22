package commands

import (
	"fmt"
	"os"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/application"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Valida a sintaxe e a semântica do arquivo de configuração e o grafo DAG",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		cfgPath, err := config.FindConfigFile(cwd, cfgFile)
		if err != nil {
			return fmt.Errorf("❌ Erro de configuração: %w", err)
		}

		cfg, err := config.LoadConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("❌ Configuração inválida: %w", err)
		}

		plan, err := application.BuildDAGPlan(cfg.Services)
		if err != nil {
			return fmt.Errorf("❌ Grafo inválido (ciclo ou dependência inexistente): %w", err)
		}

		fmt.Printf("✅ Configuração válida! (%s)\n", cfgPath)
		fmt.Printf("📦 Projeto: %s (versão %d)\n", cfg.ProjectName, cfg.Version)
		fmt.Printf("📊 Serviços declarados: %d\n", len(cfg.Services))
		fmt.Printf("🌊 Ondas de execução (%d):\n", len(plan.Waves))
		for i, wave := range plan.Waves {
			fmt.Printf("  Onda %d (paralelo): %v\n", i+1, wave)
		}
		fmt.Printf("➡️ Sequência topológica linear: %v\n", plan.LinearOrder)

		return nil
	},
}

func init() {
	rootCmd.AddCommand(validateCmd)
}
