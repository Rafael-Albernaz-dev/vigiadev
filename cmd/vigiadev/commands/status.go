package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/docker"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ipc"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ports"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var statusJSON bool

var statusCmd = &cobra.Command{
	Use:          "status",
	Aliases:      []string{"ps", "st"},
	Short:        "Inspect active vigiadev session, service processes, and port conflicts",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		defer func() {
			statusJSON = false
		}()
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		workDir := cwd
		cfgPath, err := config.FindConfigFile(cwd, cfgFile)
		if err == nil && cfgPath != "" {
			workDir = filepath.Dir(cfgPath)
		}
		if statusJSON {
			return InspectProjectStatusJSON(workDir, cfgPath, cmd.OutOrStdout())
		}
		return InspectProjectStatus(workDir, cfgPath, cmd.OutOrStdout())
	},
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output status in JSON format")
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

	var composeServices map[string]docker.ComposeServiceStatus
	if cfg != nil {
		composeFile := cfg.ComposeFile
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		composeServices, _ = docker.QueryComposeServices(ctx, workDir, composeFile)
		cancel()
	}

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
		} else if hasCfg && svcCfg.ComposeService != "" {
			if st, ok := composeServices[svcCfg.ComposeService]; ok {
				healthSuffix := ""
				if st.Health != "" {
					healthSuffix = fmt.Sprintf(", %s", st.Health)
				}
				if st.State == "running" {
					procState = fmt.Sprintf("🐳 CONTAINER (%s, running%s)", st.Name, healthSuffix)
				} else if st.State != "" {
					procState = fmt.Sprintf("⏹️ CONTAINER (%s, %s)", st.Name, st.State)
				}
			}
		}

		portDetails := []string{}
		serviceUntracked := []string{}
		unknownUntracked := false
		isComposeRunning := strings.HasPrefix(procState, "🐳 CONTAINER")

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
				if isComposeRunning {
					containerLabel := "docker container"
					if st, ok := composeServices[svcCfg.ComposeService]; ok && st.Name != "" {
						containerLabel = fmt.Sprintf("docker: %s", st.Name)
					}
					portDetails = append(portDetails, fmt.Sprintf(":%d active (%s)", port, containerLabel))
					continue
				}
				owners, err := ports.FindProcessesByPort(port)
				if err != nil {
					if policy == domain.PortPolicyReuse || svcCfg.ComposeService != "" {
						containerLabel := "docker container"
						if st, ok := composeServices[svcCfg.ComposeService]; ok && st.Name != "" {
							containerLabel = fmt.Sprintf("docker: %s", st.Name)
						}
						portDetails = append(portDetails, fmt.Sprintf(":%d active (%s)", port, containerLabel))
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
						if procState == "STOPPED" {
							procState = "EXTERNAL / REUSED"
						}
					} else {
						externalConflicts++
						serviceUntracked = append(serviceUntracked, untracked...)
					}
				}
			}
		}

		if len(serviceUntracked) > 0 {
			procState = fmt.Sprintf("🟡 UNTRACKED / ORPHAN (PID %s listening)", strings.Join(serviceUntracked, ", "))
		} else if unknownUntracked && !isComposeRunning {
			procState = "🟡 UNTRACKED (owner unavailable)"
		}

		urlSnippet := ""
		if hasCfg && svcCfg.URL != "" {
			urlSnippet = fmt.Sprintf(" | URL: %s", svcCfg.URL)
		}
		if len(portDetails) > 0 {
			fmt.Fprintf(out, "  • %-22s %-32s | Ports: %s%s\n", name, procState, strings.Join(portDetails, ", "), urlSnippet)
		} else {
			fmt.Fprintf(out, "  • %-22s %-32s%s\n", name, procState, urlSnippet)
		}
	}

	if cfg != nil && len(cfg.Links) > 0 {
		fmt.Fprintln(out, "\n🔗 Navigation Shortcuts:")
		for _, link := range cfg.Links {
			fmt.Fprintf(out, "  • %-36s %s\n", link.Name, link.URL)
		}
	}

	if lockAlive {
		fmt.Fprintf(out, "\n💡 Active session is running (PID %d). Use 'vigiadev restart <service>' to reload a service, 'vigiadev down' to stop it, or 'vigiadev up --force' to restart everything.\n", lockPID)
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

type ProjectStatusJSON struct {
	Project    string                       `json:"project"`
	WorkDir    string                       `json:"work_dir"`
	ConfigPath string                       `json:"config_path,omitempty"`
	Session    SessionStatusJSON            `json:"session"`
	Services   map[string]ServiceStatusJSON `json:"services"`
	Links      map[string]string            `json:"links,omitempty"`
}

type SessionStatusJSON struct {
	State         string `json:"state"`
	SupervisorPID int    `json:"supervisor_pid,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	SocketPath    string `json:"socket_path,omitempty"`
}

type ServiceStatusJSON struct {
	Name         string   `json:"name"`
	State        string   `json:"state"`
	Type         string   `json:"type"`
	PID          int      `json:"pid,omitempty"`
	PGID         int      `json:"pgid,omitempty"`
	ContainerID  string   `json:"container_id,omitempty"`
	Port         int      `json:"port,omitempty"`
	Ports        []int    `json:"ports,omitempty"`
	OriginalPort int      `json:"original_port,omitempty"`
	URL          string   `json:"url,omitempty"`
	Command      []string `json:"command,omitempty"`
	Detail       string   `json:"detail,omitempty"`
}

// InspectProjectStatusJSON returns structured JSON status of the project and running session.
func InspectProjectStatusJSON(workDir, cfgPath string, out io.Writer) error {
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

	m, _ := manifest.ReadManifest(workDir)
	lockPID, lockAlive := manifest.ReadLockPID(workDir)

	projectName := filepath.Base(workDir)
	if cfg != nil && cfg.ProjectName != "" {
		projectName = cfg.ProjectName
	} else if m != nil && m.ProjectName != "" {
		projectName = m.ProjectName
	}

	sessionState := "STOPPED"
	runID := ""
	if m != nil && m.RunID != "" {
		runID = m.RunID
	}

	aliveManifestSvcs := 0
	if m != nil {
		for _, entry := range m.Services {
			if !entry.IsContainer && entry.PID > 1 && manifest.IsPIDAlive(entry.PID) {
				aliveManifestSvcs++
			}
		}
	}

	switch {
	case lockAlive:
		sessionState = "ACTIVE"
	case m != nil && aliveManifestSvcs > 0:
		sessionState = "ORPHANED"
	case m != nil || lockPID > 0:
		sessionState = "STALE"
	default:
		sessionState = "STOPPED"
	}

	socketPath := ipc.ResolveSocketPath(workDir)
	if m != nil && m.SocketPath != "" {
		socketPath = m.SocketPath
	}

	statusObj := ProjectStatusJSON{
		Project:    projectName,
		WorkDir:    workDir,
		ConfigPath: cfgPath,
		Session: SessionStatusJSON{
			State:         sessionState,
			SupervisorPID: lockPID,
			RunID:         runID,
			SocketPath:    socketPath,
		},
		Services: make(map[string]ServiceStatusJSON),
	}

	if cfg != nil && len(cfg.Links) > 0 {
		statusObj.Links = make(map[string]string)
		for _, l := range cfg.Links {
			statusObj.Links[l.Name] = l.URL
		}
	}

	// If session is active, try to query live status from IPC
	var liveServices map[string]ipc.ServiceStatusInfo
	if lockAlive {
		client := ipc.NewClient(socketPath)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		resp, err := client.SendCommand(ctx, ipc.Request{Action: ipc.ActionStatus})
		cancel()
		if err == nil && resp != nil && resp.OK {
			liveServices = resp.Services
		}
	}

	// Build service set
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
	for name := range liveServices {
		serviceSet[name] = true
	}

	for name := range serviceSet {
		svcJSON := ServiceStatusJSON{
			Name:  name,
			State: "stopped",
			Type:  "process",
		}

		if cfg != nil {
			if svcCfg, ok := cfg.Services[name]; ok {
				svcJSON.URL = svcCfg.URL
				svcJSON.Ports = svcCfg.Ports
				svcJSON.Command = svcCfg.Command
				if len(svcCfg.Ports) > 0 {
					svcJSON.Port = svcCfg.Ports[0]
					svcJSON.OriginalPort = svcCfg.Ports[0]
				}
				if svcCfg.ComposeService != "" {
					svcJSON.Type = "container"
				}
			}
		}

		if m != nil {
			if entry, ok := m.Services[name]; ok {
				if entry.Port > 0 {
					svcJSON.Port = entry.Port
				}
				svcJSON.PID = entry.PID
				svcJSON.PGID = entry.PGID
				svcJSON.ContainerID = entry.ContainerID
				if entry.IsContainer {
					svcJSON.Type = "container"
				}
				if entry.PID > 1 && manifest.IsPIDAlive(entry.PID) {
					svcJSON.State = "running"
				}
			}
		}

		if liveInfo, ok := liveServices[name]; ok {
			svcJSON.State = liveInfo.State
			if liveInfo.Port > 0 {
				svcJSON.Port = liveInfo.Port
			}
			if liveInfo.PID > 0 {
				svcJSON.PID = liveInfo.PID
			}
			if liveInfo.PGID > 0 {
				svcJSON.PGID = liveInfo.PGID
			}
			if liveInfo.ContainerID != "" {
				svcJSON.ContainerID = liveInfo.ContainerID
			}
			if liveInfo.IsContainer {
				svcJSON.Type = "container"
			}
			if liveInfo.Detail != "" {
				svcJSON.Detail = liveInfo.Detail
			}
		}

		statusObj.Services[name] = svcJSON
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(statusObj)
}
