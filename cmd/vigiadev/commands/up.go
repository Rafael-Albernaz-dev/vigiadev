package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/detector"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ports"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/application"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/ui/stream"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/ui/tui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	noTUI     bool
	killPorts bool
	forceUp   bool
)

var upCmd = &cobra.Command{
	Use:   "up [services...]",
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
		if len(args) > 0 {
			cfg, err = FilterServices(cfg, args)
			if err != nil {
				return err
			}
		}
		if err := ReapOrphanedProcesses(cwd, cfg.ProjectName); err != nil {
			return err
		}

		input, isFile := cmd.InOrStdin().(*os.File)
		interactive := isFile && isatty.IsTerminal(input.Fd()) && os.Getenv("CI") == ""
		remaps, err := PreflightPorts(cfg, cmd.InOrStdin(), cmd.OutOrStdout(), interactive, forceUp, killPorts)
		if err != nil {
			return err
		}
		bus := domain.NewEventBus()

		orch, err := application.NewOrchestrator(cfg, cwd, bus)
		if err != nil {
			return fmt.Errorf("failed to initialize orchestrator: %w", err)
		}

		defer orch.Close()
		orch.AllowPortKill = killPorts && (forceUp || !interactive)
		orch.InitialPortRemaps = remaps

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Intercept Ctrl+C and SIGTERM for graceful teardown
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigChan)
		go func() {
			select {
			case <-sigChan:
				cancel()
			case <-ctx.Done():
			}
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

		serviceNames := orch.DAG.LinearOrder

		restartFunc := func(service string) error {
			return orch.RestartService(ctx, service)
		}

		// Attach the existing TUI event bridge before starting the orchestrator,
		// so preflight remap events cannot be lost during TUI startup.
		program := tea.NewProgram(tui.NewAppModel(cfg.ProjectName, serviceNames, bus, cancel, restartFunc), tea.WithAltScreen(), tea.WithMouseCellMotion())
		bus.Subscribe(func(event domain.Event) { program.Send(event) })
		go func() { orchErrChan <- orch.Run(ctx) }()
		go func() { <-ctx.Done(); program.Quit() }()
		if _, err := program.Run(); err != nil {
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
	upCmd.Flags().BoolVarP(&killPorts, "kill-ports", "k", false, "Resolve port conflicts by terminating listeners (confirmation in interactive terminals)")
	upCmd.Flags().BoolVarP(&forceUp, "force", "f", false, "Authorize configured port kills without prompting")
	rootCmd.AddCommand(upCmd)
}

// PreflightPorts resolves conflicts before starting the orchestrator or TUI.
// Input is read one line per decision; EOF and unknown answers never grant consent.
func PreflightPorts(cfg *domain.VigiaConfig, in io.Reader, out io.Writer, interactive, force, kill bool) ([]domain.PortRemapped, error) {
	if in == nil {
		in = strings.NewReader("")
	}
	if out == nil {
		out = io.Discard
	}
	reader := bufio.NewReader(in)
	resolver := ports.NewPortResolver("")
	names := make([]string, 0, len(cfg.Services))
	reserved := make(map[int]bool)
	for name, svc := range cfg.Services {
		names = append(names, name)
		for _, p := range svc.Ports {
			reserved[p] = true
		}
	}
	sort.Strings(names)
	var remaps []domain.PortRemapped
	for _, name := range names {
		svc := cfg.Services[name]
		if svc.PortPolicy == domain.PortPolicyReuse {
			continue
		}
		for index, port := range svc.Ports {
			if resolver.IsPortAvailable(port) {
				continue
			}
			if svc.PortPolicy == domain.PortPolicyRemap {
				if svc.ComposeService != "" {
					return nil, fmt.Errorf("cannot remap compose service %q without changing its published port mapping", name)
				}
				assigned := 0
				for candidate := port + 1; candidate <= 65535 && candidate <= port+ports.MaxScanAttempts; candidate++ {
					if !reserved[candidate] && resolver.IsPortAvailable(candidate) {
						assigned = candidate
						break
					}
				}
				if assigned == 0 {
					return nil, fmt.Errorf("no available port within %d attempts after %d", ports.MaxScanAttempts, port)
				}
				reserved[assigned] = true
				svc = ports.RemapService(svc, index, assigned)
				remaps = append(remaps, domain.PortRemapped{BaseEvent: domain.NewBaseEvent(), Service: name, OriginalPort: port, TargetPort: assigned})
				continue
			}
			if !kill {
				_, err := ports.FindProcessByPort(port)
				if errors.Is(err, ports.ErrProcessInaccessible) {
					return nil, fmt.Errorf("port %d is occupied by an external system process; cannot bind", port)
				}
				return nil, fmt.Errorf("port %d is occupied; cannot bind service %q", port, name)
			}
			action := ""
			if force || !interactive {
				action = "y"
			}
			var info *ports.ProcessInfo
			var err error
			info, err = ports.FindProcessByPort(port)
			if errors.Is(err, ports.ErrProcessInaccessible) {
				return nil, fmt.Errorf("port %d is occupied by an external system process; cannot bind", port)
			}
			if err != nil {
				return nil, fmt.Errorf("port %d: %w", port, err)
			}
			if action == "" {
				fmt.Fprintf(out, "Port %d is in use by PID %d (%s). Proceed to kill? [y/N]: ", port, info.PID, info.Name)
				answer, err := reader.ReadString('\n')
				if err == nil {
					action = strings.ToLower(strings.TrimSpace(answer))
				}
			}
			switch action {
			case "y", "yes":
				if err := ports.KillProcessOnPort(port, info, 500*time.Millisecond); err != nil {
					return nil, err
				}
			case "r", "remap":
				if svc.ComposeService != "" {
					return nil, fmt.Errorf("cannot remap compose service %q without changing its published port mapping", name)
				}
				assigned := 0
				for candidate := port + 1; candidate <= 65535 && candidate <= port+ports.MaxScanAttempts; candidate++ {
					if !reserved[candidate] && resolver.IsPortAvailable(candidate) {
						assigned = candidate
						break
					}
				}
				if assigned == 0 {
					return nil, fmt.Errorf("no available port within %d attempts after %d", ports.MaxScanAttempts, port)
				}
				reserved[assigned] = true
				svc = ports.RemapService(svc, index, assigned)
				remaps = append(remaps, domain.PortRemapped{BaseEvent: domain.NewBaseEvent(), Service: name, OriginalPort: port, TargetPort: assigned})
			default:
				return nil, fmt.Errorf("Operation cancelled due to port conflict.")
			}
		}
		cfg.Services[name] = svc
	}
	return remaps, nil
}

// ReapOrphanedProcesses removes only local PIDs recorded by a stale run of this project.
func ReapOrphanedProcesses(workDir, projectName string) error {
	m, err := manifest.ReadManifest(workDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read prior run manifest: %w", err)
	}
	if m.ProjectName != projectName || m.RunID == "" {
		return fmt.Errorf("prior run manifest does not match this project")
	}
	lock, err := os.ReadFile(filepath.Join(workDir, manifest.VigiaDir, manifest.LockFile))
	if err == nil {
		var owner int
		if _, scanErr := fmt.Sscanf(string(lock), "%d", &owner); scanErr == nil && owner > 1 {
			if err := syscall.Kill(owner, 0); err == nil || errors.Is(err, syscall.EPERM) {
				return fmt.Errorf("prior vigiadev session with PID %d is still active", owner)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for name, svc := range m.Services {
		if svc.IsContainer || svc.PID <= 1 || svc.PGID != svc.PID {
			continue
		}
		cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", svc.PID))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect orphan %q: %w", name, err)
		}
		group, err := syscall.Getpgid(svc.PID)
		if err != nil || group != svc.PGID || filepath.Clean(cwd) != filepath.Clean(workDir) {
			return fmt.Errorf("refusing to terminate PID %d: project or process group no longer matches", svc.PID)
		}
		if err := ports.KillProcessByPID(svc.PID, 500*time.Millisecond); err != nil {
			return fmt.Errorf("reap orphan %q: %w", name, err)
		}
	}
	return manifest.RemoveManifest(workDir)
}

// FilterServices resolves the requested services and their transitive dependencies.
func FilterServices(cfg *domain.VigiaConfig, targets []string) (*domain.VigiaConfig, error) {
	if len(targets) == 0 {
		return cfg, nil
	}
	selected := make(map[string]bool)
	var queue []string

	for _, target := range targets {
		if _, exists := cfg.Services[target]; !exists {
			var available []string
			for k := range cfg.Services {
				available = append(available, k)
			}
			sort.Strings(available)
			return nil, fmt.Errorf("service %q not found. Available services: %s", target, strings.Join(available, ", "))
		}
		if !selected[target] {
			selected[target] = true
			queue = append(queue, target)
		}
	}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		svc := cfg.Services[curr]
		for _, dep := range svc.DependsOn {
			if !selected[dep] {
				selected[dep] = true
				queue = append(queue, dep)
			}
		}
	}

	filtered := *cfg
	filtered.Services = make(map[string]domain.ServiceConfig)
	for name := range selected {
		filtered.Services[name] = cfg.Services[name]
	}
	return &filtered, nil
}
