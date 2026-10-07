package commands

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ports"
	"github.com/spf13/cobra"
)

var (
	forceRemove bool
	keepConfig  bool
)

var knownConfigFiles = []string{
	"vigiadev.yaml",
	"vigiadev.yml",
	"vigiadev.local.yaml",
	"vigiadev.local.yml",
}

// RunRemove executes the removal of vigiaDev runtime state, active sessions, and configuration files.
func RunRemove(cwd string, force bool, keepConf bool, explicitConfig string, inReader io.Reader) error {
	if inReader == nil {
		inReader = os.Stdin
	}
	reader := bufio.NewReader(inReader)
	if !force {
		fmt.Print("⚠️  This will stop any active sessions and delete all vigiaDev files (.vigiadev/ and configs). Proceed? [y/N]: ")
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("\nOperation cancelled.")
			return nil
		}
		input = strings.TrimSpace(strings.ToLower(input))
		if input != "y" && input != "yes" {
			fmt.Println("Operation cancelled.")
			return nil
		}
	}

	// 1. Gracefully stop active session if present
	if _, err := manifest.ReadManifest(cwd); err == nil {
		fmt.Println("⏹️  Stopping active session before removal...")
		if err := StopSession(cwd); err != nil {
			fmt.Printf("⚠️  Warning during session teardown: %v\n", err)
		}
	}

	if err := cleanupResidualPorts(cwd, explicitConfig, force, reader, os.Stdout); err != nil {
		return err
	}

	// 2. Remove .vigiadev runtime directory
	vigiaDir := filepath.Join(cwd, manifest.VigiaDir)
	if _, err := os.Stat(vigiaDir); err == nil {
		if err := manifest.CleanVigiaDir(cwd); err != nil {
			return fmt.Errorf("failed to remove '%s': %w", manifest.VigiaDir, err)
		}
		fmt.Printf("🗑️  Removed '%s' directory.\n", manifest.VigiaDir)
	}

	// 3. Remove configuration files unless keepConf is set
	if !keepConf {
		var targets []string
		if explicitConfig != "" {
			targets = append(targets, explicitConfig)
		}
		targets = append(targets, knownConfigFiles...)

		// Also check any glob matches for vigiadev*.yaml / vigiadev*.yml
		globs, _ := filepath.Glob(filepath.Join(cwd, "vigiadev*.y*ml"))
		for _, g := range globs {
			rel, err := filepath.Rel(cwd, g)
			if err == nil {
				targets = append(targets, rel)
			}
		}

		removedAny := false
		seen := make(map[string]bool)
		for _, file := range targets {
			targetPath := file
			if !filepath.IsAbs(targetPath) {
				targetPath = filepath.Join(cwd, file)
			}
			if seen[targetPath] {
				continue
			}
			seen[targetPath] = true

			if _, err := os.Stat(targetPath); err == nil {
				if err := os.Remove(targetPath); err != nil {
					fmt.Printf("⚠️  Failed to remove '%s': %v\n", file, err)
				} else {
					fmt.Printf("🗑️  Removed configuration file '%s'.\n", filepath.Base(targetPath))
					removedAny = true
				}
			}
		}
		if !removedAny {
			fmt.Println("ℹ️  No configuration files found to remove.")
		}
	} else {
		fmt.Println("ℹ️  Configuration files preserved (--keep-config).")
	}

	fmt.Println("✨ Repository cleaned of vigiaDev configuration and runtime artifacts.")
	return nil
}

var removeCmd = &cobra.Command{
	Use:     "remove",
	Aliases: []string{"rm", "clean"},
	Short:   "Stop active sessions and remove all vigiaDev files and configurations from the repository",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		return RunRemove(cwd, forceRemove, keepConfig, cfgFile, cmd.InOrStdin())
	},
}

func init() {
	removeCmd.Flags().BoolVarP(&forceRemove, "force", "f", false, "Bypass confirmation prompt")
	removeCmd.Flags().BoolVarP(&forceRemove, "yes", "y", false, "Alias for --force")
	removeCmd.Flags().BoolVar(&keepConfig, "keep-config", false, "Remove only runtime session files (.vigiadev/) and keep YAML configuration files")
	rootCmd.AddCommand(removeCmd)
}

// cleanupResidualPorts inspects configuration before it is removed, even with --keep-config.
func cleanupResidualPorts(cwd, explicit string, force bool, reader *bufio.Reader, out io.Writer) error {
	targets := append([]string{}, knownConfigFiles...)
	targets = append(targets, "dev.yaml")
	if explicit != "" {
		targets = append(targets, explicit)
	}
	globs, err := filepath.Glob(filepath.Join(cwd, "vigiadev*.y*ml"))
	if err != nil {
		return err
	}
	targets = append(targets, globs...)
	seenFiles, seenPorts := make(map[string]bool), make(map[int]bool)
	var configured []int
	for _, target := range targets {
		if !filepath.IsAbs(target) {
			target = filepath.Join(cwd, target)
		}
		target = filepath.Clean(target)
		if seenFiles[target] {
			continue
		}
		seenFiles[target] = true
		if _, err := os.Stat(target); os.IsNotExist(err) {
			continue
		}
		cfg, err := config.LoadConfig(target)
		if err != nil {
			fmt.Fprintf(out, "⚠️  Cannot inspect ports in %s: %v\n", filepath.Base(target), err)
			continue
		}
		for _, svc := range cfg.Services {
			if svc.ComposeService != "" {
				continue
			}
			for _, p := range svc.Ports {
				if !seenPorts[p] {
					seenPorts[p] = true
					configured = append(configured, p)
				}
			}
		}
	}
	sort.Ints(configured)
	for _, port := range configured {
		if ports.NewPortResolver("").IsPortAvailable(port) {
			continue
		}
		info, err := ports.FindProcessByPort(port)
		if err != nil {
			fmt.Fprintf(out, "ℹ️  Port %d occupied by external/system process; skipping residual cleanup.\n", port)
			continue
		}
		confirmed := force
		if !force {
			fmt.Fprintf(out, "⚠️  Port %d is still occupied by residual process PID %d (%s). Kill it? [y/N]: ", port, info.PID, info.Name)
			answer, err := reader.ReadString('\n')
			confirmed = err == nil && (strings.EqualFold(strings.TrimSpace(answer), "y") || strings.EqualFold(strings.TrimSpace(answer), "yes"))
		}
		if confirmed {
			if err := ports.KillProcessOnPort(port, info, 500*time.Millisecond); err != nil {
				fmt.Fprintf(out, "⚠️  Could not terminate process on port %d: %v\n", port, err)
			}
		}
	}
	return nil
}
