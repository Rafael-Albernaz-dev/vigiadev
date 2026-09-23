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
	Short: "Start and supervise local environment services with interactive TUI",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		cfgPath, err := config.FindConfigFile(cwd, cfgFile)
		if err != nil {
			if errors.Is(err, config.ErrConfigNotFound) && cfgFile == "" {
				fmt.Println("⚠️ No configuration file found.")
				fmt.Println("🔍 Analyzing local repository stack...")

				d := detector.NewStackDetector(cwd)
				result, detectErr := d.Detect()
				if detectErr == nil && len(result.Config.Services) > 0 {
					fmt.Println("\n📦 vigiaDev detected the following services:")
					for name, svc := range result.Config.Services {
						if len(svc.Command) > 0 {
							fmt.Printf("  • %s: %v (ports: %v)\n", name, svc.Command, svc.Ports)
						} else {
							fmt.Printf("  • %s: compose_service '%s'\n", name, svc.ComposeService)
						}
					}

					fmt.Print("\n❓ Save 'vigiadev.yaml' and start now? [Y/n]: ")
					var answer string
					_, _ = fmt.Scanln(&answer)
					answer = strings.TrimSpace(strings.ToLower(answer))

					if answer == "" || answer == "y" || answer == "yes" || answer == "s" || answer == "sim" {
						yamlContent, _ := detector.GenerateYAML(result.Config, result.Markers)
						_ = os.WriteFile(filepath.Join(cwd, "vigiadev.yaml"), []byte(yamlContent), 0644)
						cfgPath = filepath.Join(cwd, "vigiadev.yaml")
						fmt.Println("✨ 'vigiadev.yaml' generated successfully! Starting environment...")
						fmt.Println()
					} else {
						fmt.Println("Operation cancelled.")
						return nil
					}
				} else {
					return fmt.Errorf("no configuration file found and no default stack detected.\nRun 'vigiadev init' to create a starter configuration")
				}
			} else {
				return fmt.Errorf("no configuration file found: %w\n(Run 'vigiadev init' or pass -c <file>)", err)
			}
		}

		cfg, err := config.LoadConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("failed to load configuration: %w", err)
		}

		bus := domain.NewEventBus()

		orch, err := application.NewOrchestrator(cfg, cwd, bus)
		if err != nil {
			return fmt.Errorf("failed to initialize orchestrator: %w", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Intercept Ctrl+C and SIGTERM for graceful teardown
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigChan
			cancel()
		}()

		// Headless / Streaming Mode (CI/CD or explicit --no-tui flag)
		if noTUI {
			streamView := stream.NewStreamView(os.Stdout)
			streamView.Attach(bus)

			fmt.Printf("🚀 vigiadev started for project '%s' [%s]\n", cfg.ProjectName, cfgPath)
			fmt.Printf("💡 Press Ctrl+C to stop all services.\n\n")

			if err := orch.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("execution stopped with error: %w", err)
			}

			fmt.Println("✨ All processes stopped and resources cleaned up.")
			return nil
		}

		// Interactive TUI Mode (Default)
		orchErrChan := make(chan error, 1)
		go func() {
			orchErrChan <- orch.Run(ctx)
		}()

		serviceNames := orch.DAG.LinearOrder

		restartFunc := func(service string) error {
			return orch.RestartService(ctx, service)
		}

		// Run TUI (with mouse, click, and keyboard navigation)
		if err := tui.RunTUI(ctx, cfg.ProjectName, serviceNames, bus, cancel, restartFunc); err != nil {
			cancel()
			return err
		}

		// Wait for clean orchestrator shutdown
		select {
		case err := <-orchErrChan:
			if err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
		case <-time.After(3 * time.Second):
		}

		return nil
	},
}

func init() {
	upCmd.Flags().BoolVar(&noTUI, "no-tui", false, "Run in direct streaming mode on stdout (headless/no TUI)")
	rootCmd.AddCommand(upCmd)
}
