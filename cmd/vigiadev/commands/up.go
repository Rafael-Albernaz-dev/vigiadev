package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
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
			return fmt.Errorf("nenhum arquivo de configuração encontrado: %w\n(Execute 'vigiadev init' ou passe -c <arquivo>)", err)
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
