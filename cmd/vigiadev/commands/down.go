package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/spf13/cobra"
)

// StopSession stops all services and containers in the active session and cleans up resources.
func StopSession(cwd string) error {
	m, err := manifest.ReadManifest(cwd)
	lockPID, lockAlive := manifest.ReadLockPID(cwd)
	if err != nil && !lockAlive {
		return nil
	}

	if m != nil {
		fmt.Printf("⏹️ Stopping session '%s' for project '%s'...\n", m.RunID, m.ProjectName)
	} else {
		fmt.Printf("⏹️ Stopping active vigiaDev supervisor (PID %d)...\n", lockPID)
	}

	// 0. Signal the vigiaDev supervisor process if another session holds the lock
	if lockAlive && lockPID > 1 && lockPID != os.Getpid() {
		fmt.Printf("  • Stopping vigiaDev supervisor (PID %d)...\n", lockPID)
		_ = syscall.Kill(lockPID, syscall.SIGTERM)
	}

	if m != nil {
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
	}

	time.Sleep(500 * time.Millisecond)

	// Force SIGKILL if lingering processes remain
	if m != nil {
		for _, svc := range m.Services {
			if svc.PGID > 0 {
				_ = syscall.Kill(-svc.PGID, syscall.SIGKILL)
			} else if svc.PID > 0 {
				_ = syscall.Kill(svc.PID, syscall.SIGKILL)
			}
		}
	}
	if lockPID > 1 && lockPID != os.Getpid() && manifest.IsPIDAlive(lockPID) {
		_ = syscall.Kill(lockPID, syscall.SIGKILL)
		for i := 0; i < 10 && manifest.IsPIDAlive(lockPID); i++ {
			time.Sleep(20 * time.Millisecond)
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

		targetDir := cwd
		if cfgPath, err := config.FindConfigFile(cwd, cfgFile); err == nil {
			targetDir = filepath.Dir(cfgPath)
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

		_, errManifest := manifest.ReadManifest(targetDir)
		_, lockAlive := manifest.ReadLockPID(targetDir)
		if errManifest != nil && !lockAlive {
			fmt.Println("ℹ️ No active vigiaDev session found (.vigiadev/run.json missing).")
			return nil
		}

		return StopSession(targetDir)
	},
}

func init() {
	rootCmd.AddCommand(downCmd)
}
