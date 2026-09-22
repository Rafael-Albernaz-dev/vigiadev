package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/application"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/ui/stream"
	"github.com/spf13/cobra"
)

var (
	noTUI bool
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Inicia e supervisiona os serviços do ambiente local",
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

		// Conecta visualizador em modo streaming (padrão inicial do MVP Go)
		streamView := stream.NewStreamView(os.Stdout)
		streamView.Attach(bus)

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
			sig := <-sigChan
			fmt.Printf("\n🛑 Sinal recebido (%s). Iniciando encerramento gracioso dos processos...\n", sig)
			cancel()
		}()

		fmt.Printf("🚀 vigiadev iniciado para o projeto '%s' [%s]\n", cfg.ProjectName, cfgPath)
		fmt.Printf("💡 Pressione Ctrl+C para encerrar todos os serviços.\n\n")

		if err := orch.Run(ctx); err != nil && err != context.Canceled {
			return fmt.Errorf("execução interrompida com erro: %w", err)
		}

		fmt.Println("✨ Todos os processos foram finalizados e recursos limpos.")
		return nil
	},
}

func init() {
	upCmd.Flags().BoolVar(&noTUI, "no-tui", true, "Executa em modo streaming direto no stdout (sem TUI)")
	rootCmd.AddCommand(upCmd)
}
