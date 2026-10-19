package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/docker"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ports"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/spf13/cobra"
)

var resolveAll bool

var resolveCmd = &cobra.Command{
	Use:     "resolve",
	Aliases: []string{"clean-ports", "resolve-ports"},
	Short:   "Terminate listeners on project host ports (preserves reuse and Compose services unless --all is set)",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		all, _ := cmd.Flags().GetBool("all")
		return ResolveProjectPorts(cwd, cfgFile, cmd.OutOrStdout(), all)
	},
}

func init() {
	resolveCmd.Flags().BoolVarP(&resolveAll, "all", "a", false, "Also stop conflicting Docker Compose containers and resolve all project ports")
	rootCmd.AddCommand(resolveCmd)
}

// ResolveProjectPorts terminates all owners of configured, non-reused host ports.
// When all is true, it also stops Docker Compose containers and frees all ports.
func ResolveProjectPorts(cwd, explicit string, out io.Writer, all ...bool) error {
	path, err := config.FindConfigFile(cwd, explicit)
	if err != nil {
		return err
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		return err
	}
	resolveAllOption := len(all) > 0 && all[0]
	return resolveConfiguredPorts(cfg, out, false, resolveAllOption, filepath.Dir(path))
}

func hostCleanupPorts(cfg *domain.VigiaConfig) []int {
	targets, protected := map[int]bool{}, map[int]bool{}
	for _, svc := range cfg.Services {
		for _, port := range svc.Ports {
			if svc.ComposeService != "" || svc.PortPolicy == domain.PortPolicyReuse {
				protected[port] = true
			} else {
				targets[port] = true
			}
		}
	}
	var result []int
	for port := range targets {
		if !protected[port] {
			result = append(result, port)
		}
	}
	sort.Ints(result)
	return result
}

func allProjectPorts(cfg *domain.VigiaConfig) []int {
	targets := map[int]bool{}
	for _, svc := range cfg.Services {
		for _, port := range svc.Ports {
			targets[port] = true
		}
	}
	var result []int
	for port := range targets {
		result = append(result, port)
	}
	sort.Ints(result)
	return result
}

func resolveConfiguredPorts(cfg *domain.VigiaConfig, out io.Writer, down bool, all bool, workDir string) error {
	if out == nil {
		out = os.Stdout
	}
	var failures []error

	if all {
		var names []string
		for name := range cfg.Services {
			names = append(names, name)
		}
		sort.Strings(names)
		seen := map[string]bool{}
		for _, name := range names {
			svc := cfg.Services[name]
			if svc.ComposeService == "" {
				continue
			}
			file := svc.ComposeFile
			if file == "" {
				file = cfg.ComposeFile
			}
			key := file + "\x00" + svc.ComposeService
			if seen[key] {
				continue
			}
			seen[key] = true
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			runner := &docker.DefaultComposeRunner{}
			err := runner.Stop(ctx, svc.ComposeService, file, workDir)
			cancel()
			if err != nil {
				failures = append(failures, err)
				continue
			}
			fmt.Fprintf(out, "  • Stopped Compose service %s.\n", svc.ComposeService)
		}
	}

	resolver := ports.NewPortResolver("")
	var targets []int
	if all {
		targets = allProjectPorts(cfg)
	} else {
		targets = hostCleanupPorts(cfg)
	}

	conflicts := 0
	for _, port := range targets {
		if resolver.IsPortAvailable(port) {
			continue
		}
		conflicts++
		owners, err := ports.FindProcessesByPort(port)
		if err == nil {
			err = ports.KillProcessesOnPort(port, owners, time.Second)
		}
		if err != nil {
			fmt.Fprintf(out, "❌ Port %d was not resolved: %v\n", port, err)
			failures = append(failures, fmt.Errorf("port %d: %w", port, err))
			continue
		}
		for _, owner := range owners {
			if down {
				fmt.Fprintf(out, "  • Freed port %d (terminated PID %d %s)\n", port, owner.PID, owner.Name)
			} else {
				fmt.Fprintf(out, "🔨 Terminated PID %d (%s) on port %d\n", owner.PID, owner.Name, port)
			}
		}
		fmt.Fprintf(out, "✅ Port %d is now free.\n", port)
	}

	// Recheck every target
	for _, port := range targets {
		available := false
		for i := 0; i < 20; i++ {
			if resolver.IsPortAvailable(port) {
				available = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !available {
			failures = append(failures, fmt.Errorf("port %d remains occupied; check for a restarting supervisor", port))
		}
	}

	if err := errors.Join(failures...); err != nil {
		return err
	}

	if all {
		if conflicts == 0 {
			fmt.Fprintln(out, "✨ No port conflicts detected. All project host ports and containers are clean.")
		} else {
			fmt.Fprintln(out, "✨ All conflicting host ports and containers have been resolved! Environment is clean for 'vigiadev up'.")
		}
		return nil
	}

	var activePreservedPorts []int
	for _, svc := range cfg.Services {
		if svc.ComposeService != "" || svc.PortPolicy == domain.PortPolicyReuse {
			for _, port := range svc.Ports {
				if !resolver.IsPortAvailable(port) {
					activePreservedPorts = append(activePreservedPorts, port)
				}
			}
		}
	}
	sort.Ints(activePreservedPorts)
	var uniquePreserved []string
	seenPort := map[int]bool{}
	for _, p := range activePreservedPorts {
		if !seenPort[p] {
			seenPort[p] = true
			uniquePreserved = append(uniquePreserved, fmt.Sprintf(":%d", p))
		}
	}

	if len(uniquePreserved) > 0 {
		fmt.Fprintf(out, "💡 Reusable Docker Compose containers or ports are active: %s. Use 'vigiadev resolve --all' or 'vigiadev down' to stop them.\n", strings.Join(uniquePreserved, ", "))
	}

	if conflicts == 0 {
		fmt.Fprintln(out, "✨ No port conflicts detected. All eligible project host ports are free (reuse and Compose ports preserved).")
	} else {
		fmt.Fprintln(out, "✨ All conflicting host ports have been resolved! Environment is clean for 'vigiadev up' (reuse and Compose ports preserved).")
	}
	return nil
}
