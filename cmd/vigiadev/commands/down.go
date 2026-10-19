package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/docker"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ports"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/spf13/cobra"
)

// StopSession stops all services and containers in the active session and cleans up resources.
func StopSession(cwd string) error {
	m, err := manifest.ReadManifest(cwd)
	lockPID, lockAlive := manifest.ReadLockPID(cwd)
	if err != nil && !lockAlive {
		return nil
	}

	var failures []error
	recordSignal := func(pid int, sig syscall.Signal) {
		if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			failures = append(failures, fmt.Errorf("signal %d: %w", pid, err))
		}
	}
	stopContainer := func(id string) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(ctx, "docker", "stop", id).CombinedOutput(); err != nil {
			failures = append(failures, fmt.Errorf("stop container %s: %s: %w", id, output, err))
		}
	}
	if m != nil {
		fmt.Printf("⏹️ Stopping session '%s' for project '%s'...\n", m.RunID, m.ProjectName)
	} else {
		fmt.Printf("⏹️ Stopping active vigiaDev supervisor (PID %d)...\n", lockPID)
	}

	// 0. Signal the vigiaDev supervisor process if another session holds the lock
	if lockAlive && lockPID > 1 && lockPID != os.Getpid() {
		fmt.Printf("  • Stopping vigiaDev supervisor (PID %d)...\n", lockPID)
		recordSignal(lockPID, syscall.SIGTERM)
	}

	if m != nil {
		// 1. Stop containers tracked in session manifest without destroying volumes or networks (DEC-005, DEC-018)
		stoppedContainers := make(map[string]bool)
		for _, cID := range m.Containers {
			if !stoppedContainers[cID] {
				fmt.Printf("  • Stopping container '%s'...\n", cID)
				stopContainer(cID)
				stoppedContainers[cID] = true
			}
		}
		for name, svc := range m.Services {
			if svc.IsContainer && svc.ContainerID != "" && !stoppedContainers[svc.ContainerID] {
				fmt.Printf("  • Stopping service container '%s' (%s)...\n", name, svc.ContainerID)
				stopContainer(svc.ContainerID)
				stoppedContainers[svc.ContainerID] = true
			}
		}

		// 2. Stop native POSIX processes belonging to the session
		for name, svc := range m.Services {
			if svc.PGID > 0 {
				fmt.Printf("  • Stopping service '%s' (PGID %d)...\n", name, svc.PGID)
				recordSignal(-svc.PGID, syscall.SIGTERM)
			} else if svc.PID > 0 {
				recordSignal(svc.PID, syscall.SIGTERM)
			}
		}
	}

	time.Sleep(500 * time.Millisecond)

	// Force SIGKILL if lingering processes remain
	if m != nil {
		for _, svc := range m.Services {
			if svc.PGID > 0 {
				recordSignal(-svc.PGID, syscall.SIGKILL)
			} else if svc.PID > 0 {
				recordSignal(svc.PID, syscall.SIGKILL)
			}
		}
	}
	if lockPID > 1 && lockPID != os.Getpid() && manifest.IsPIDAlive(lockPID) {
		recordSignal(lockPID, syscall.SIGKILL)
		for i := 0; i < 10 && manifest.IsPIDAlive(lockPID); i++ {
			time.Sleep(20 * time.Millisecond)
		}
	}

	if m != nil {
		deadline := time.Now().Add(time.Second)
		for _, svc := range m.Services {
			if svc.IsContainer {
				continue
			}
			for svc.PID > 1 && manifest.IsPIDAlive(svc.PID) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if svc.PID > 1 && manifest.IsPIDAlive(svc.PID) {
				failures = append(failures, fmt.Errorf("PID %d remains alive", svc.PID))
			}
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	if err := manifest.RemoveManifest(cwd); err != nil {
		return err
	}
	if err := manifest.ReleaseLock(cwd); err != nil {
		return err
	}

	fmt.Println("✨ All session services stopped and resources released.")
	return nil
}

var keepContainers bool

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop session resources, configured Compose services and residual host listeners",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		keep, _ := cmd.Flags().GetBool("keep-containers")
		targetDir := cwd
		if _, err := config.FindConfigFile(cwd, cfgFile); err == nil {
			return DownProject(cwd, cfgFile, cmd.OutOrStdout(), keep)
		} else if cfgFile != "" {
			return err
		} else {
			curr := filepath.Clean(cwd)
			for {
				if _, statErr := os.Stat(filepath.Join(curr, manifest.VigiaDir, manifest.RunFile)); statErr == nil {
					targetDir = curr
					break
				}
				if _, statErr := os.Stat(filepath.Join(curr, manifest.VigiaDir, manifest.LockFile)); statErr == nil {
					targetDir = curr
					break
				}
				parent := filepath.Dir(curr)
				if parent == curr {
					break
				}
				curr = parent
			}
		}

		return DownProject(targetDir, cfgFile, cmd.OutOrStdout(), keep)
	},
}

func init() {
	downCmd.Flags().BoolVar(&keepContainers, "keep-containers", false, "Keep reusable Docker Compose containers running")
	rootCmd.AddCommand(downCmd)
}

// DownProject cleans configured resources even without an active manifest/lock.
// StopSession stays scoped to tracked resources for internal restart/remove calls.
func DownProject(cwd, explicit string, out io.Writer, keepContainers ...bool) error {
	if out == nil {
		out = os.Stdout
	}
	keep := len(keepContainers) > 0 && keepContainers[0]
	var cfg *domain.VigiaConfig
	path, err := config.FindConfigFile(cwd, explicit)
	if err == nil {
		cfg, err = config.LoadConfig(path)
		if err != nil {
			return err
		}
		cwd = filepath.Dir(path)
	} else if explicit != "" || !errors.Is(err, config.ErrConfigNotFound) {
		return err
	}
	if _, err := manifest.ReadManifest(cwd); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, alive := manifest.ReadLockPID(cwd)
	if _, err := manifest.ReadManifest(cwd); errors.Is(err, os.ErrNotExist) && !alive {
		fmt.Fprintln(out, "ℹ️ No active vigiaDev session; inspecting configured project resources.")
	}
	var failures []error
	if err := StopSession(cwd); err != nil {
		failures = append(failures, err)
	}
	if cfg == nil {
		fmt.Fprintln(out, "ℹ️ No project configuration found; only tracked session resources were inspected.")
		return errors.Join(failures...)
	}
	// Stop declared Compose services, never networks or volumes.
	var names []string
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := map[string]bool{}
	for _, name := range names {
		svc := cfg.Services[name]
		if svc.ComposeService == "" || (keep && svc.PortPolicy == domain.PortPolicyReuse) {
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
		err := runner.Stop(ctx, svc.ComposeService, file, cwd)
		cancel()
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, port := range svc.Ports {
			available := false
			for i := 0; i < 20; i++ {
				if ports.NewPortResolver("").IsPortAvailable(port) {
					available = true
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !available {
				failures = append(failures, fmt.Errorf("Compose service %s stopped but port %d remains occupied", svc.ComposeService, port))
			}
		}
		fmt.Fprintf(out, "  • Stopped Compose service %s.\n", svc.ComposeService)
	}
	if err := resolveConfiguredPorts(cfg, out, true, false, cwd); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
