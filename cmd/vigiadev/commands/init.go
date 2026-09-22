package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/detector"
	"github.com/spf13/cobra"
)

var (
	forceInit bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Detecta a stack do repositório e gera um vigiadev.yaml contextual",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		targetFile := filepath.Join(cwd, "vigiadev.yaml")
		if _, err := os.Stat(targetFile); err == nil && !forceInit {
			return fmt.Errorf("o arquivo 'vigiadev.yaml' já existe neste diretório (use --force para sobrescrever)")
		}

		fmt.Println("🔍 Analisando o repositório local...")
		d := detector.NewStackDetector(cwd)
		result, err := d.Detect()
		if err != nil {
			return fmt.Errorf("falha ao analisar stack do projeto: %w", err)
		}

		yamlContent, err := detector.GenerateYAML(result.Config, result.Markers)
		if err != nil {
			return fmt.Errorf("falha ao gerar sintaxe YAML: %w", err)
		}

		if err := os.WriteFile(targetFile, []byte(yamlContent), 0644); err != nil {
			return fmt.Errorf("falha ao salvar arquivo 'vigiadev.yaml': %w", err)
		}

		fmt.Println("✨ Arquivo 'vigiadev.yaml' gerado com sucesso!")
		if len(result.Markers) > 0 {
			fmt.Printf("📦 Stacks detectadas: %s\n", fmt.Sprintf("%v", result.Markers))
		}
		fmt.Printf("📊 Serviços configurados (%d):\n", len(result.Config.Services))
		for name, svc := range result.Config.Services {
			if len(svc.Command) > 0 {
				fmt.Printf("  • %s: comando %v | portas %v\n", name, svc.Command, svc.Ports)
			} else if svc.ComposeService != "" {
				fmt.Printf("  • %s: compose_service '%s' | portas %v\n", name, svc.ComposeService, svc.Ports)
			}
		}

		fmt.Println("\n🚀 Execute 'vigiadev up' para iniciar e supervisionar o ambiente!")
		return nil
	},
}

func init() {
	initCmd.Flags().BoolVarP(&forceInit, "force", "f", false, "Sobrescreve vigiadev.yaml caso já exista")
	rootCmd.AddCommand(initCmd)
}
