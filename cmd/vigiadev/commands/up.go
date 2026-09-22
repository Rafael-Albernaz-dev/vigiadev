package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/detector"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/application"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/ui/stream"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/ui/tui"
	"github.com/spf13/cobra"
)

var (
	noTUI bool
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Inicia e supervisiona os serviços do ambiente local com TUI interativa",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		cfgPath, err := config.FindConfigFile(cwd, cfgFile)
		if err != nil {
			if errors.Is(err, config.ErrConfigNotFound) && cfgFile == "" {
				fmt.Println("⚠️ Nenhum arquivo de configuração encontrado.")
				fmt.Println("🔍 Analisando stack do repositório local...")

				d := detector.NewStackDetector(cwd)
				result, detectErr := d.Detect()
				if detectErr == nil && len(result.Config.Services) > 0 {
					fmt.Println("\n📦 O vigiaDev detectou os seguintes serviços:")
					for name, svc := range result.Config.Services {
						if len(svc.Command) > 0 {
							fmt.Printf("  • %s: %v (portas: %v)\n", name, svc.Command, svc.Ports)
						} else {
							fmt.Printf("  • %s: compose_service '%s'\n", name, svc.ComposeService)
						}
					}

					fmt.Print("\n❓ Deseja salvar 'vigiadev.yaml' e iniciar agora? [S/n]: ")
					var answer string
					_, _ = fmt.Scanln(&answer)
					answer = strings.TrimSpace(strings.ToLower(answer))

					if answer == "" || answer == "s" || answer == "sim" || answer == "y" || answer == "yes" {
						yamlContent, _ := detector.GenerateYAML(result.Config, result.Markers)
						_ = os.WriteFile(filepath.Join(cwd, "vigiadev.yaml"), []byte(yamlContent), 0644)
						cfgPath = filepath.Join(cwd, "vigiadev.yaml")
						fmt.Println("✨ 'vigiadev.yaml' gerado com sucesso! Iniciando ambiente...")
						fmt.Println()
					} else {
						fmt.Println("Operação cancelada.")
						return nil
					}
				} else {
					return fmt.Errorf("nenhum arquivo de configuração encontrado e nenhuma stack padrão detectada.\nExecute 'vigiadev init' para criar um modelo inicial")
				}
			} else {
				return fmt.Errorf("nenhum arquivo de configuração encontrado: %w\n(Execute 'vigiadev init' ou passe -c <arquivo>)", err)
			}
		}

		cfg, err := config.LoadConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("falha ao carregar configuração: %w", err)
		}

		bus := domain.NewEventBus()

		orch, err := application.NewOrchestrator(cfg, cwd, bus)
		if err != nil {
			return fmt.Errorf("falha ao instanciar orquestrador: %w", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Intercepta Ctrl+C e SIGTERM para teardown gracioso
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigChan
			cancel()
		}()

		// Modo Headless / Streaming (CI/CD ou flag explícita --no-tui)
		if noTUI {
			streamView := stream.NewStreamView(os.Stdout)
			streamView.Attach(bus)

			fmt.Printf("🚀 vigiadev iniciado para o projeto '%s' [%s]\n", cfg.ProjectName, cfgPath)
			fmt.Printf("💡 Pressione Ctrl+C para encerrar todos os serviços.\n\n")

			if err := orch.Run(ctx); err != nil && err != context.Canceled {
				return fmt.Errorf("execução interrompida com erro: %w", err)
			}

			fmt.Println("✨ Todos os processos foram finalizados e recursos limpos.")
			return nil
		}

		// Modo TUI Interativo (Padrão)
		orchErrChan := make(chan error, 1)
		go func() {
			orchErrChan <- orch.Run(ctx)
		}()

		serviceNames := orch.DAG.LinearOrder

		restartFunc := func(service string) error {
			return orch.RestartService(ctx, service)
		}

		// Executa a TUI (com suporte a mouse, cliques e teclado)
		if err := tui.RunTUI(ctx, cfg.ProjectName, serviceNames, bus, cancel, restartFunc); err != nil {
			cancel()
			return err
		}

		// Aguarda a finalização limpa do orquestrador
		select {
		case <-orchErrChan:
		case <-time.After(3 * time.Second):
		}

		return nil
	},
}

func init() {
	upCmd.Flags().BoolVar(&noTUI, "no-tui", false, "Executa em modo streaming direto no stdout (sem TUI)")
	rootCmd.AddCommand(upCmd)
}
