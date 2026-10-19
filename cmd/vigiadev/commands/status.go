package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ports"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:          "status",
	Aliases:      []string{"ps", "st"},
	Short:        "Inspect active vigiadev session, service processes, and port conflicts",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		workDir := cwd
		cfgPath, err := config.FindConfigFile(cwd, cfgFile)
		if err == nil && cfgPath != "" {
			workDir = filepath.Dir(cfgPath)
		}
		return InspectProjectStatus(workDir, cfgPath, cmd.OutOrStdout())
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

// InspectProjectStatus inspects the current vigiadev session state, running processes, and port availability/conflicts.
func InspectProjectStatus(workDir, cfgPath string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}

	var cfg *domain.VigiaConfig
	if cfgPath != "" {
		loaded, err := config.LoadConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("failed to load configuration %q: %w", cfgPath, err)
		}
		cfg = loaded
	}

	m, manifestErr := manifest.ReadManifest(workDir)
	if manifestErr != nil && !errors.Is(manifestErr, os.ErrNotExist) {
		return fmt.Errorf("failed to read session manifest: %w", manifestErr)
	}

	lockPID, lockAlive := manifest.ReadLockPID(workDir)

	projectName := filepath.Base(workDir)
	if cfg != nil && cfg.ProjectName != "" {
		projectName = cfg.ProjectName
	} else if m != nil && m.ProjectName != "" {
		projectName = m.ProjectName
	}

	fmt.Fprintf(out, "Project: %s (%s)\n", projectName, workDir)
	if cfgPath != "" {
		fmt.Fprintf(out, "Config:  %s\n", cfgPath)
	}

	sessionPIDs := make(map[int]string)
	sessionPGIDs := make(map[int]string)
	if lockAlive {
		sessionPIDs[lockPID] = "vigiadev-supervisor"
	}

	aliveManifestSvcs := 0
	if m != nil {
		for name, entry := range m.Services {
			if !entry.IsContainer && entry.PID > 1 && manifest.IsPIDAlive(entry.PID) {
				aliveManifestSvcs++
				sessionPIDs[entry.PID] = name
				if entry.PGID > 1 {
					sessionPGIDs[entry.PGID] = name
				}
			}
		}
	}

	switch {
	case lockAlive:
		runID := "unknown"
		if m != nil && m.RunID != "" {
			runID = m.RunID
		}
		fmt.Fprintf(out, "Session: ACTIVE (Supervisor PID %d, RunID: %s)\n", lockPID, runID)
	case m != nil && aliveManifestSvcs > 0:
		fmt.Fprintf(out, "Session: ORPHANED (%d service process(es) still alive without supervisor; RunID: %s)\n", aliveManifestSvcs, m.RunID)
	case m != nil || lockPID > 0:
		fmt.Fprintf(out, "Session: STALE (prior manifest/lock left behind, no active supervisor)\n")
	default:
		fmt.Fprintf(out, "Session: STOPPED (no active vigiadev session)\n")
	}

	// Collect all service names from config and/or manifest
	serviceSet := make(map[string]bool)
	if cfg != nil {
		for name := range cfg.Services {
			serviceSet[name] = true
		}
	}
	if m != nil {
		for name := range m.Services {
			serviceSet[name] = true
		}
	}

	if len(serviceSet) == 0 {
		return nil
	}

	var serviceNames []string
	for name := range serviceSet {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)

	resolver := ports.NewPortResolver("")
	externalConflicts := 0

	fmt.Fprintln(out, "\nServices & Ports:")
	for _, name := range serviceNames {
		var svcCfg domain.ServiceConfig
		hasCfg := false
		if cfg != nil {
			svcCfg, hasCfg = cfg.Services[name]
		}

		var entry manifest.ServiceManifest
		hasManifest := false
		if m != nil {
			entry, hasManifest = m.Services[name]
		}

		procState := "STOPPED"
		if hasManifest {
			if entry.IsContainer {
				procState = fmt.Sprintf("CONTAINER (%s)", entry.ContainerID)
			} else if entry.PID > 1 && manifest.IsPIDAlive(entry.PID) {
				procState = fmt.Sprintf("RUNNING (PID %d, PGID %d)", entry.PID, entry.PGID)
			} else if entry.PID > 1 {
				procState = fmt.Sprintf("EXITED (stale PID %d)", entry.PID)
			}
		}

		portDetails := []string{}
		serviceUntracked := []string{}
		unknownUntracked := false
		if hasCfg && len(svcCfg.Ports) > 0 {
			policy := svcCfg.PortPolicy
			if policy == "" {
				policy = domain.PortPolicyFail
			}
			for _, port := range svcCfg.Ports {
				if resolver.IsPortAvailable(port) {
					portDetails = append(portDetails, fmt.Sprintf(":%d free", port))
					continue
				}
				owners, err := ports.FindProcessesByPort(port)
				if err != nil {
					if policy == domain.PortPolicyReuse || svcCfg.ComposeService != "" {
						procState = "EXTERNAL / UNVERIFIED"
						portDetails = append(portDetails, fmt.Sprintf(":%d active (external/docker, policy=%s; owner unavailable)", port, policy))
					} else {
						unknownUntracked = true
						externalConflicts++
						portDetails = append(portDetails, fmt.Sprintf(":%d occupied [policy=%s; %v]", port, policy, err))
					}
					continue
				}
				untracked := []string{}
				for _, info := range owners {
					ownedBySession := false
					if lockAlive {
						if _, ok := sessionPIDs[info.PID]; ok {
							ownedBySession = true
						} else if pgid, pgErr := syscall.Getpgid(info.PID); pgErr == nil {
							_, ownedBySession = sessionPGIDs[pgid]
						}
					}
					if ownedBySession {
						portDetails = append(portDetails, fmt.Sprintf(":%d active (session PID %d %s)", port, info.PID, info.Name))
					} else {
						portDetails = append(portDetails, fmt.Sprintf(":%d occupied by PID %d (%s) [policy=%s]", port, info.PID, info.Name, policy))
						untracked = append(untracked, fmt.Sprintf("%d", info.PID))
					}
				}
				if len(untracked) > 0 {
					if policy == domain.PortPolicyReuse || svcCfg.ComposeService != "" {
						procState = "EXTERNAL / REUSED"
					} else {
						externalConflicts++
						serviceUntracked = append(serviceUntracked, untracked...)
					}
				}
			}
		}

		if len(serviceUntracked) > 0 {
			procState = fmt.Sprintf("🟡 UNTRACKED / ORPHAN (PID %s listening)", strings.Join(serviceUntracked, ", "))
		} else if unknownUntracked {
			procState = "🟡 UNTRACKED (owner unavailable)"
		}
		if len(portDetails) > 0 {
			fmt.Fprintf(out, "  • %-22s %-30s | Ports: %s\n", name, procState, strings.Join(portDetails, ", "))
		} else {
			fmt.Fprintf(out, "  • %-22s %s\n", name, procState)
		}
	}

	if lockAlive {
		fmt.Fprintf(out, "\n💡 Active session is running (PID %d). Run 'vigiadev down' to stop it, or 'vigiadev up --force' to restart.\n", lockPID)
	}
	if externalConflicts > 0 {
		alert := strings.Join([]string{
			"⚠️  UNTRACKED PROCESSES DETECTED ON PROJECT PORTS",
			fmt.Sprintf("%d occupied port(s) outside active session management.", externalConflicts),
			"Active listeners do not prove application health or session ownership.",
			"",
			"👉 Run 'vigiadev resolve' to terminate host listeners and free ports.",
			"👉 Or run 'vigiadev down' to stop resources and clear host ports.",
			"Reusable infrastructure is preserved.",
		}, "\n")
		fmt.Fprintln(out, "\n"+lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Render(alert))
	}

	return nil
}
