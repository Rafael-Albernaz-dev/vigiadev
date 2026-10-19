package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ports"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/spf13/cobra"
)

var resolveCmd = &cobra.Command{
	Use:     "resolve",
	Aliases: []string{"clean-ports", "resolve-ports"},
	Short:   "Terminate listeners on project host ports (preserves reuse and Compose services)",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		return ResolveProjectPorts(cwd, cfgFile, cmd.OutOrStdout())
	},
}

func init() { rootCmd.AddCommand(resolveCmd) }

// ResolveProjectPorts terminates all owners of configured, non-reused host ports.
// Calling resolve explicitly authorizes this cleanup; no supervisor is started.
func ResolveProjectPorts(cwd, explicit string, out io.Writer) error {
	path, err := config.FindConfigFile(cwd, explicit)
	if err != nil {
		return err
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		return err
	}
	return resolveConfiguredPorts(cfg, out, false)
}

func hostCleanupPorts(cfg *domain.VigiaConfig) []int {
	// If a port is shared with reusable infrastructure, preserve it regardless of
	// map traversal order or how another service declares the same port.
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

func resolveConfiguredPorts(cfg *domain.VigiaConfig, out io.Writer, down bool) error {
	if out == nil {
		out = os.Stdout
	}
	resolver := ports.NewPortResolver("")
	targets := hostCleanupPorts(cfg)
	conflicts := 0
	var failures []error
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
	// Recheck every target: terminating one listener may trigger a supervisor to
	// restart another port that was freed earlier in the same operation.
	for _, port := range targets {
		if !resolver.IsPortAvailable(port) {
			failures = append(failures, fmt.Errorf("port %d remains occupied; check for a restarting supervisor", port))
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	if conflicts == 0 {
		fmt.Fprintln(out, "✨ No port conflicts detected. All eligible project host ports are free (reuse and Compose ports preserved).")
	} else {
		fmt.Fprintln(out, "✨ All conflicting host ports have been resolved! Environment is clean for 'vigiadev up' (reuse and Compose ports preserved).")
	}
	return nil
}
