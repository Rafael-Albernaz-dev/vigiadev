package commands

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/detector"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

const gitignoreHeader = "# vigiaDev runtime, sessions & local configs"

func missingGitignoreRules(repoDir string) ([]string, error) {
	content, err := os.ReadFile(filepath.Join(repoDir, ".gitignore"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to read .gitignore: %w", err)
	}
	lines := make(map[string]bool)
	for _, line := range strings.Split(string(content), "\n") {
		lines[strings.TrimSuffix(line, "\r")] = true
	}
	var missing []string
	for _, rule := range []string{".vigiadev/", "vigiadev.local.yaml"} {
		if !lines[rule] {
			missing = append(missing, rule)
		}
	}
	return missing, nil
}

// EnsureGitignore appends only missing rules, preserving existing bytes and mode.
// The returned bool reports whether the file was changed.
func EnsureGitignore(repoDir string) (bool, error) {
	path := filepath.Join(repoDir, ".gitignore")
	content, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("failed to read .gitignore: %w", err)
	}
	missing, err := missingGitignoreRules(repoDir)
	if err != nil {
		return false, err
	}
	if len(missing) == 0 {
		return false, nil
	}
	lines := make(map[string]bool)
	for _, line := range strings.Split(string(content), "\n") {
		lines[strings.TrimSuffix(line, "\r")] = true
	}
	newline := "\n"
	if strings.Contains(string(content), "\r\n") {
		newline = "\r\n"
	}
	var addition strings.Builder
	if len(content) > 0 && content[len(content)-1] != '\n' {
		addition.WriteString(newline)
	}
	if !lines[gitignoreHeader] {
		addition.WriteString(gitignoreHeader + newline)
	}
	for _, rule := range missing {
		addition.WriteString(rule + newline)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return false, fmt.Errorf("failed to open .gitignore: %w", err)
	}
	_, writeErr := io.WriteString(file, addition.String())
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return false, fmt.Errorf("failed to update .gitignore: %w", err)
	}
	return true, nil
}

func newInitCommand() *cobra.Command {
	var force, local bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Detect repository stack and generate team or local configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			input, ok := cmd.InOrStdin().(*os.File)
			interactive := ok && isatty.IsTerminal(input.Fd())
			return RunInit(cmd, cwd, force, local, interactive)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Overwrite the selected configuration if it already exists")
	cmd.Flags().BoolVarP(&local, "local", "l", false, "Generate git-ignored vigiadev.local.yaml without prompting")
	return cmd
}

// RunInit scaffolds in repoDir using Cobra's input/output streams. Interactive
// is supplied separately so target selection can also be exercised without a TTY.
func RunInit(cmd *cobra.Command, repoDir string, force, local, interactive bool) error {
	cmd.SetOut(cmd.OutOrStdout())
	input := bufio.NewReader(cmd.InOrStdin())
	missing, err := missingGitignoreRules(repoDir)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		update := true
		if interactive && !force {
			cmd.Print("Add '.vigiadev/' and 'vigiadev.local.yaml' to .gitignore? [Y/n]: ")
			for {
				answer, readErr := input.ReadString('\n')
				if readErr != nil && !errors.Is(readErr, io.EOF) {
					return fmt.Errorf("failed to read .gitignore confirmation: %w", readErr)
				}
				switch strings.ToLower(strings.TrimSpace(answer)) {
				case "", "y", "yes":
				case "n", "no":
					update = false
				default:
					if errors.Is(readErr, io.EOF) {
						return fmt.Errorf("invalid .gitignore confirmation: %q (expected yes or no)", strings.TrimSpace(answer))
					}
					cmd.Print("Please answer yes or no [Y/n]: ")
					continue
				}
				break
			}
		}
		if update {
			changed, err := EnsureGitignore(repoDir)
			if err != nil {
				return err
			}
			if changed {
				cmd.Println("🛡️  Updated .gitignore with vigiaDev rules.")
			}
		} else {
			cmd.Println("⚠️  Skipped .gitignore update. Ensure .vigiadev/ is ignored manually.")
		}
	}
	name := "vigiadev.yaml"
	if local {
		name = "vigiadev.local.yaml"
	} else if interactive && !force {
		cmd.Println("Select configuration target:")
		cmd.Println("  [1] Team-shared (vigiadev.yaml - committed to repository) [default]")
		cmd.Println("  [2] Personal/Local (vigiadev.local.yaml - ignored by git)")
		cmd.Print("> ")
		for {
			answer, err := input.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("failed to read configuration target: %w", err)
			}
			switch strings.TrimSpace(answer) {
			case "", "1":
			case "2":
				name = "vigiadev.local.yaml"
			default:
				if errors.Is(err, io.EOF) {
					return fmt.Errorf("invalid configuration target: %q (expected 1 or 2)", strings.TrimSpace(answer))
				}
				cmd.Print("Please select 1 or 2 [default: 1]: ")
				continue
			}
			break
		}
	}
	target := filepath.Join(repoDir, name)
	if _, err := os.Stat(target); err == nil && !force {
		return fmt.Errorf("configuration file '%s' already exists (use --force to overwrite)", name)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to inspect '%s': %w", name, err)
	}
	cmd.Println("🔍 Analyzing local repository...")
	result, err := detector.NewStackDetector(repoDir).Detect()
	if err != nil {
		return fmt.Errorf("failed to detect project stack: %w", err)
	}
	yamlContent, err := detector.GenerateYAML(result.Config, result.Markers)
	if err != nil {
		return fmt.Errorf("failed to generate YAML syntax: %w", err)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	file, err := os.OpenFile(target, flags, 0644)
	if err != nil {
		return fmt.Errorf("failed to save '%s': %w", name, err)
	}
	_, writeErr := io.WriteString(file, yamlContent)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("failed to save '%s': %w", name, err)
	}
	cmd.Printf("✨ '%s' generated successfully!\n", name)
	if len(result.Markers) > 0 {
		cmd.Printf("📦 Detected stacks: %v\n", result.Markers)
	}
	cmd.Printf("📊 Configured services (%d):\n", len(result.Config.Services))
	names := make([]string, 0, len(result.Config.Services))
	for name := range result.Config.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		svc := result.Config.Services[name]
		if len(svc.Command) > 0 {
			cmd.Printf("  • %s: command %v | ports %v\n", name, svc.Command, svc.Ports)
		} else if svc.ComposeService != "" {
			cmd.Printf("  • %s: compose_service '%s' | ports %v\n", name, svc.ComposeService, svc.Ports)
		}
	}
	printAvailableTasks(cmd, result.Config)
	cmd.Println("\n🚀 Run 'vigiadev up' to start and monitor the environment!")
	return nil
}

func init() {
	rootCmd.AddCommand(newInitCommand())
}
