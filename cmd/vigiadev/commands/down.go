package commands

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/spf13/cobra"
)

// StopSession stops all services and containers in the active session and cleans up resources.
func StopSession(cwd string) error {
	m, err := manifest.ReadManifest(cwd)
	if err != nil {
		return nil
	}

	fmt.Printf("⏹️ Stopping session '%s' for project '%s'...\n", m.RunID, m.ProjectName)

	// 1. Stop containers tracked in session manifest without destroying volumes or networks (DEC-005, DEC-018)
	stoppedContainers := make(map[string]bool)
	for _, cID := range m.Containers {
		if !stoppedContainers[cID] {
			fmt.Printf("  • Stopping container '%s'...\n", cID)
			_ = exec.Command("docker", "stop", cID).Run()
			stoppedContainers[cID] = true
		}
	}
	for name, svc := range m.Services {
		if svc.IsContainer && svc.ContainerID != "" && !stoppedContainers[svc.ContainerID] {
			fmt.Printf("  • Stopping service container '%s' (%s)...\n", name, svc.ContainerID)
			_ = exec.Command("docker", "stop", svc.ContainerID).Run()
			stoppedContainers[svc.ContainerID] = true
		}
	}

	// 2. Stop native POSIX processes belonging to the session
	for name, svc := range m.Services {
		if svc.PGID > 0 {
			fmt.Printf("  • Stopping service '%s' (PGID %d)...\n", name, svc.PGID)
			_ = syscall.Kill(-svc.PGID, syscall.SIGTERM)
		} else if svc.PID > 0 {
			_ = syscall.Kill(svc.PID, syscall.SIGTERM)
		}
	}

	time.Sleep(500 * time.Millisecond)

	// Force SIGKILL if lingering processes remain
	for _, svc := range m.Services {
		if svc.PGID > 0 {
			_ = syscall.Kill(-svc.PGID, syscall.SIGKILL)
		}
	}

	_ = manifest.RemoveManifest(cwd)
	_ = manifest.ReleaseLock(cwd)

	fmt.Println("✨ All session services stopped and resources released.")
	return nil
}

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop all services in the active session and clean up resources",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		if _, err := manifest.ReadManifest(cwd); err != nil {
			fmt.Println("ℹ️ No active vigiaDev session found (.vigiadev/run.json missing).")
			return nil
		}

		return StopSession(cwd)
	},
}

func init() {
	rootCmd.AddCommand(downCmd)
}
