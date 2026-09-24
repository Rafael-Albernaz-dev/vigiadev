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
	Short: "Detect repository stack and generate contextual vigiadev.yaml",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		targetFile := filepath.Join(cwd, "vigiadev.yaml")
		if _, err := os.Stat(targetFile); err == nil && !forceInit {
			return fmt.Errorf("configuration file 'vigiadev.yaml' already exists (use --force to overwrite)")
		}

		fmt.Println("🔍 Analyzing local repository...")
		d := detector.NewStackDetector(cwd)
		result, err := d.Detect()
		if err != nil {
			return fmt.Errorf("failed to detect project stack: %w", err)
		}

		yamlContent, err := detector.GenerateYAML(result.Config, result.Markers)
		if err != nil {
			return fmt.Errorf("failed to generate YAML syntax: %w", err)
		}

		if err := os.WriteFile(targetFile, []byte(yamlContent), 0644); err != nil {
			return fmt.Errorf("failed to save 'vigiadev.yaml': %w", err)
		}

		fmt.Println("✨ 'vigiadev.yaml' generated successfully!")
		if len(result.Markers) > 0 {
			fmt.Printf("📦 Detected stacks: %s\n", fmt.Sprintf("%v", result.Markers))
		}
		fmt.Printf("📊 Configured services (%d):\n", len(result.Config.Services))
		for name, svc := range result.Config.Services {
			if len(svc.Command) > 0 {
				fmt.Printf("  • %s: command %v | ports %v\n", name, svc.Command, svc.Ports)
			} else if svc.ComposeService != "" {
				fmt.Printf("  • %s: compose_service '%s' | ports %v\n", name, svc.ComposeService, svc.Ports)
			}
		}

		printAvailableTasks(cmd, result.Config)

		fmt.Println("\n🚀 Run 'vigiadev up' to start and monitor the environment!")
		return nil
	},
}

func init() {
	initCmd.Flags().BoolVarP(&forceInit, "force", "f", false, "Overwrite vigiadev.yaml if it already exists")
	rootCmd.AddCommand(initCmd)
}
