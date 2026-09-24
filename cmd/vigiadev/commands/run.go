package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/detector"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/application"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/spf13/cobra"
)

var (
	noDeps bool
)

var runCmd = &cobra.Command{
	Use:   "run [task] [--no-deps] [-- extra-args...]",
	Short: "Run a one-off task declared in tasks: with automatic dependency resolution",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		cfgPath, err := config.FindConfigFile(cwd, cfgFile)
		if err != nil {
			return fmt.Errorf("no configuration file found: %w", err)
		}

		cfg, err := config.LoadConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("failed to load configuration: %w", err)
		}

		if len(args) == 0 {
			if len(cfg.Tasks) == 0 {
				discovered, err := detector.NewStackDetector(cwd).DetectTasks()
				if err != nil {
					return err
				}
				if len(discovered) > 0 {
					cmd.Println("🔍 Discovered repository tasks:")
					printAvailableTasks(cmd, &domain.VigiaConfig{ProjectName: cfg.ProjectName, Tasks: discovered})
					saved, err := offerTaskPersistence(cmd, cfgPath, discovered)
					if err != nil {
						return err
					}
					if !saved {
						return nil
					}
					cfg, err = config.LoadConfig(cfgPath)
					if err != nil {
						return err
					}
				}
			}
			printAvailableTasks(cmd, cfg)
			return nil
		}

		taskName := args[0]
		extraArgs := args[1:]

		task, exists := cfg.Tasks[taskName]
		discovered := !exists
		if !exists {
			tasks, err := detector.NewStackDetector(cwd).DetectTasks()
			if err != nil {
				return err
			}
			task, exists = tasks[taskName]
			if !exists {
				printAvailableTasks(cmd, cfg)
				return fmt.Errorf("task '%s' not found in project '%s'", taskName, cfg.ProjectName)
			}
			cfg.Tasks[taskName] = task
			cmd.Printf("ℹ️ Task '%s' auto-discovered: %s\n", taskName, strings.Join(task.Command, " "))
		}

		bus := domain.NewEventBus()
		orch, err := application.NewOrchestrator(cfg, cwd, bus)
		if err != nil {
			return fmt.Errorf("failed to initialize orchestrator: %w", err)
		}

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		if !noDeps && len(task.DependsOn) > 0 {
			cmd.Printf("📦 Ensuring dependencies for task '%s': %v...\n", taskName, task.DependsOn)
		}

		exitCode, err := orch.RunTask(ctx, taskName, extraArgs, noDeps)
		if err != nil {
			cmd.Printf("❌ Task '%s' failed: %v\n", taskName, err)
			os.Exit(exitCode)
		}

		if discovered {
			if _, saveErr := offerTaskPersistence(cmd, cfgPath, map[string]domain.TaskConfig{taskName: task}); saveErr != nil {
				if exitCode != 0 {
					cmd.PrintErrln(saveErr)
				} else {
					return saveErr
				}
			}
		}

		if exitCode != 0 {
			os.Exit(exitCode)
		}

		cmd.Printf("✨ Task '%s' completed successfully.\n", taskName)
		return nil
	},
}

func printAvailableTasks(cmd *cobra.Command, cfg *domain.VigiaConfig) {
	if len(cfg.Tasks) == 0 {
		cmd.Printf("ℹ️ Project '%s' has no tasks declared under 'tasks:'.\n", cfg.ProjectName)
		cmd.Println("Tip: Add tasks to your vigiadev.yaml, for example:")
		cmd.Println("  tasks:")
		cmd.Println("    migrate:")
		cmd.Println("      command: [\"npx\", \"prisma\", \"migrate\", \"deploy\"]")
		cmd.Println("      depends_on: [database]")
		return
	}

	cmd.Printf("📋 Available tasks in project '%s':\n", cfg.ProjectName)
	names := make([]string, 0, len(cfg.Tasks))
	for name := range cfg.Tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t := cfg.Tasks[name]
		deps := ""
		if len(t.DependsOn) > 0 {
			deps = fmt.Sprintf(" (depends on: %s)", strings.Join(t.DependsOn, ", "))
		}
		cmd.Printf("  • %-16s %s%s\n", name, strings.Join(t.Command, " "), deps)
	}
	cmd.Printf("\nUsage: vigiadev run <task> [--no-deps] [-- extra-args...]\n")
}

func init() {
	runCmd.Flags().BoolVar(&noDeps, "no-deps", false, "Run task directly without checking or starting dependencies")
	rootCmd.AddCommand(runCmd)
}

// EOF and every answer other than y/yes decline persistence.
func offerTaskPersistence(cmd *cobra.Command, path string, tasks map[string]domain.TaskConfig) (bool, error) {
	cmd.Printf("Save discovered tasks to %s? [y/N] ", path)
	answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		return false, nil
	}
	if err := config.SaveDiscoveredTasks(path, tasks); err != nil {
		return false, fmt.Errorf("save discovered tasks: %w", err)
	}
	cmd.Println("✨ Discovered tasks saved.")
	return true, nil
}
