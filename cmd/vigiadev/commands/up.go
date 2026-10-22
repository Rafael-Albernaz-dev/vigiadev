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
	noTUI       bool
	killPorts   bool
	forceUp     bool
	profileFlag string
	allFlag     bool
)

var upCmd = &cobra.Command{
	Use:          "up [services...]",
	Short:        "Start and supervise local environment services with interactive TUI",
	SilenceUsage: true,
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

		projectRoot := filepath.Dir(cfgPath)

		cfg, err := config.LoadConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("failed to load configuration: %w", err)
		}
		input, isFile := cmd.InOrStdin().(*os.File)
		interactive := isFile && isatty.IsTerminal(input.Fd()) && os.Getenv("CI") == ""

		proceed, err := HandleOrReapPriorSession(projectRoot, cfg.ProjectName, cmd.InOrStdin(), cmd.OutOrStdout(), interactive, forceUp)
		if err != nil {
			return err
		}
		if !proceed {
			return nil
		}

		cfg, err = ResolveProfilesAndServicesWithAll(cfg, profileFlag, args, cmd.InOrStdin(), cmd.OutOrStdout(), interactive && !forceUp, allFlag)
		if err != nil {
			return err
		}

		remaps, err := PreflightPorts(cfg, cmd.InOrStdin(), cmd.OutOrStdout(), interactive, forceUp, killPorts)
		if err != nil {
			return err
		}
		bus := domain.NewEventBus()

		orch, err := application.NewOrchestrator(cfg, projectRoot, bus)
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
			streamView.PrintLinks(cfg.Links)

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
		toggleFunc := func(service string) error {
			return orch.ToggleService(ctx, service)
		}

		// Attach the existing TUI event bridge before starting the orchestrator,
		// so preflight remap events cannot be lost during TUI startup.
		servicePorts := make(map[string]int)
		for name, svc := range cfg.Services {
			if len(svc.Ports) > 0 {
				servicePorts[name] = svc.Ports[0]
			}
		}
		for _, remap := range remaps {
			servicePorts[remap.Service] = remap.TargetPort
		}

		startAllFunc := func() error {
			return orch.StartAllServices(ctx)
		}

		appModel := tui.NewAppModel(cfg.ProjectName, serviceNames, servicePorts, bus, cancel, restartFunc)
		appModel.SetNavigation(cfg.Services, cfg.Links)
		appModel.SetToggleFunc(toggleFunc)
		appModel.SetStartAllFunc(startAllFunc)
		program := tea.NewProgram(appModel, tea.WithAltScreen(), tea.WithMouseCellMotion())
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
	upCmd.Flags().StringVarP(&profileFlag, "profile", "p", "", "Execution profile to run (filters services)")
	upCmd.Flags().BoolVarP(&allFlag, "all", "a", false, "Start all services declared in configuration (bypasses profile prompt)")
	upCmd.Flags().BoolVarP(&allFlag, "ALL", "A", false, "Start all services declared in configuration (bypasses profile prompt)")
	_ = upCmd.Flags().MarkHidden("ALL")
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

func isWithinDir(target, base string) bool {
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		realBase = filepath.Clean(base)
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		realTarget = filepath.Clean(target)
	}
	rel, err := filepath.Rel(realBase, realTarget)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// readSingleLine reads one line byte-by-byte without lookahead buffering so multiple sequential prompts can share io.Reader.
func readSingleLine(in io.Reader) (string, bool) {
	if in == nil {
		return "", false
	}
	var buf []byte
	b := make([]byte, 1)
	for {
		n, err := in.Read(b)
		if n > 0 {
			if b[0] == '\n' {
				return strings.TrimRight(string(buf), "\r"), true
			}
			buf = append(buf, b[0])
		}
		if err != nil {
			if len(buf) > 0 {
				return strings.TrimRight(string(buf), "\r"), true
			}
			return "", false
		}
	}
}

// HandleOrReapPriorSession checks whether an active vigiadev session is already running BEFORE prompting for profiles.
// If an active session exists:
//   - With force=true: automatically stops the active session and proceeds.
//   - With interactive=true: warns the user and prompts whether to stop and restart it [y/N].
//   - Otherwise: returns a clear actionable error explaining how to inspect (`vigiadev status`) or stop (`vigiadev down`) it.
func HandleOrReapPriorSession(workDir, projectName string, in io.Reader, out io.Writer, interactive, force bool) (bool, error) {
	if out == nil {
		out = io.Discard
	}
	ownerPID, alive := manifest.ReadLockPID(workDir)
	if alive && ownerPID > 1 {
		if force {
			fmt.Fprintf(out, "⚠️ Active vigiadev session detected (PID %d). Stopping existing session (--force)...\n", ownerPID)
			if err := StopSession(workDir); err != nil {
				return false, fmt.Errorf("failed to stop active session (PID %d): %w", ownerPID, err)
			}
			return true, nil
		}
		if interactive {
			fmt.Fprintf(out, "⚠️ An active vigiadev session (PID %d) is already running for project %q.\n", ownerPID, projectName)
			fmt.Fprint(out, "Stop existing session and restart now? [y/N]: ")
			line, ok := readSingleLine(in)
			choice := strings.ToLower(strings.TrimSpace(line))
			if ok && (choice == "y" || choice == "yes" || choice == "s" || choice == "sim") {
				if err := StopSession(workDir); err != nil {
					return false, fmt.Errorf("failed to stop active session (PID %d): %w", ownerPID, err)
				}
				return true, nil
			}
			fmt.Fprintln(out, "Operation cancelled. Run 'vigiadev status' to inspect or 'vigiadev down' to stop the active session.")
			return false, nil
		}
		return false, fmt.Errorf("prior vigiadev session with PID %d is still active (run 'vigiadev status' to inspect, 'vigiadev restart <service>' to restart a single service, 'vigiadev down' to stop it, or 'vigiadev up --force' to replace it)", ownerPID)
	}
	if err := ReapOrphanedProcesses(workDir, projectName); err != nil {
		return false, err
	}
	return true, nil
}

// ReapOrphanedProcesses removes only local PIDs recorded by a stale run of this project.
func ReapOrphanedProcesses(workDir, projectName string) error {
	m, err := manifest.ReadManifest(workDir)
	if errors.Is(err, os.ErrNotExist) {
		_ = manifest.ReleaseLock(workDir)
		return nil
	}
	if err != nil {
		return fmt.Errorf("read prior run manifest: %w", err)
	}
	if m.ProjectName != projectName || m.RunID == "" {
		return fmt.Errorf("prior run manifest does not match this project")
	}
	owner, alive := manifest.ReadLockPID(workDir)
	if alive && owner > 1 {
		return fmt.Errorf("prior vigiadev session with PID %d is still active", owner)
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
		dirMatches := isWithinDir(cwd, workDir) || (svc.Dir != "" && isWithinDir(cwd, svc.Dir))
		if err != nil || group != svc.PGID || !dirMatches {
			return fmt.Errorf("refusing to terminate PID %d: project or process group no longer matches", svc.PID)
		}
		if err := ports.KillProcessByPID(svc.PID, 500*time.Millisecond); err != nil {
			return fmt.Errorf("reap orphan %q: %w", name, err)
		}
		if svc.PGID > 0 {
			_ = syscall.Kill(-svc.PGID, syscall.SIGKILL)
		}
	}
	_ = manifest.ReleaseLock(workDir)
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

// PromptProfileSelection apresenta o menu interativo para seleção de perfil conforme DEC-033.
func PromptProfileSelection(cfg *domain.VigiaConfig, in io.Reader, out io.Writer) (string, error) {
	if len(cfg.Profiles) == 0 {
		return "", nil
	}

	var profileNames []string
	for name := range cfg.Profiles {
		profileNames = append(profileNames, name)
	}
	sort.Strings(profileNames)

	fmt.Fprintln(out, "Select execution profile:")
	for i, name := range profileNames {
		fmt.Fprintf(out, "  [%d] %s (%d services)\n", i+1, name, len(cfg.Profiles[name]))
	}
	fmt.Fprintln(out, "  [A] All services (default)")
	fmt.Fprint(out, "Select [A]: ")

	if line, ok := readSingleLine(in); ok {
		choice := strings.TrimSpace(line)
		if choice == "" || strings.EqualFold(choice, "a") || strings.EqualFold(choice, "all") {
			return "all", nil
		}

		for i, name := range profileNames {
			if choice == fmt.Sprintf("%d", i+1) || strings.EqualFold(choice, name) {
				return name, nil
			}
		}

		fmt.Fprintf(out, "Invalid selection %q, proceeding with All services.\n", choice)
		return "all", nil
	}
	return "all", nil
}

// ResolveProfilesAndServices aplica as regras de filtragem por profile e/ou argumentos posicionais.
func ResolveProfilesAndServices(cfg *domain.VigiaConfig, profile string, args []string, in io.Reader, out io.Writer, interactive bool) (*domain.VigiaConfig, error) {
	return ResolveProfilesAndServicesWithAll(cfg, profile, args, in, out, interactive, false)
}

// ResolveProfilesAndServicesWithAll aplica as regras de filtragem considerando a opção allServices (flag -a/-A/--all).
func ResolveProfilesAndServicesWithAll(cfg *domain.VigiaConfig, profile string, args []string, in io.Reader, out io.Writer, interactive bool, allServices bool) (*domain.VigiaConfig, error) {
	if allServices {
		var filteredArgs []string
		for _, arg := range args {
			if !strings.EqualFold(arg, "all") && arg != "-a" && arg != "-A" && arg != "--all" {
				filteredArgs = append(filteredArgs, arg)
			}
		}
		if len(filteredArgs) > 0 {
			return FilterServices(cfg, filteredArgs)
		}
		return cfg, nil
	}

	if len(args) > 0 {
		var filteredArgs []string
		hasAllArg := false
		for _, arg := range args {
			if strings.EqualFold(arg, "all") || arg == "-a" || arg == "-A" || arg == "--all" {
				hasAllArg = true
			} else {
				filteredArgs = append(filteredArgs, arg)
			}
		}
		if hasAllArg && len(filteredArgs) == 0 {
			return cfg, nil
		}
		if len(filteredArgs) > 0 {
			return FilterServices(cfg, filteredArgs)
		}
	}

	if profile != "" {
		svcs, exists := cfg.Profiles[profile]
		if !exists {
			var available []string
			for p := range cfg.Profiles {
				available = append(available, p)
			}
			sort.Strings(available)
			return nil, fmt.Errorf("profile %q not found. Available profiles: %s", profile, strings.Join(available, ", "))
		}
		targets := append([]string{}, svcs...)
		if len(args) > 0 {
			targets = append(targets, args...)
		}
		return FilterServices(cfg, targets)
	}

	if len(cfg.Profiles) > 0 && interactive {
		selected, err := PromptProfileSelection(cfg, in, out)
		if err != nil {
			return nil, err
		}
		if selected != "" && selected != "all" {
			return FilterServices(cfg, cfg.Profiles[selected])
		}
	}

	return cfg, nil
}
