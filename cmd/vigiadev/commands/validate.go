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
	Short: "Validate syntax and semantics of configuration file and DAG graph",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		cfgPath, err := config.FindConfigFile(cwd, cfgFile)
		if err != nil {
			return fmt.Errorf("❌ Configuration error: %w", err)
		}

		cfg, err := config.LoadConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("❌ Invalid configuration: %w", err)
		}

		plan, err := application.BuildDAGPlan(cfg.Services)
		if err != nil {
			return fmt.Errorf("❌ Invalid graph (cycle or missing dependency): %w", err)
		}

		fmt.Printf("✅ Valid configuration! (%s)\n", cfgPath)
		fmt.Printf("📦 Project: %s (version %d)\n", cfg.ProjectName, cfg.Version)
		fmt.Printf("📊 Configured services: %d\n", len(cfg.Services))
		fmt.Printf("🌊 Execution waves (%d):\n", len(plan.Waves))
		for i, wave := range plan.Waves {
			fmt.Printf("  Wave %d (parallel): %v\n", i+1, wave)
		}
		fmt.Printf("➡️ Linear topological sequence: %v\n", plan.LinearOrder)

		return nil
	},
}

func init() {
	rootCmd.AddCommand(validateCmd)
}
