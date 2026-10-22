package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ipc"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/spf13/cobra"
)

var (
	restartAll     bool
	restartJSON    bool
	restartTimeout time.Duration
)

type ServiceActionResult struct {
	Service string `json:"service"`
	OK      bool   `json:"ok"`
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

type CommandResponseJSON struct {
	OK       bool                  `json:"ok"`
	Action   string                `json:"action"`
	Services []ServiceActionResult `json:"services,omitempty"`
	Error    string                `json:"error,omitempty"`
	Message  string                `json:"message,omitempty"`
}

var restartCmd = &cobra.Command{
	Use:          "restart [services...]",
	Short:        "Restart one or more services in the active vigiaDev session via supervisor IPC",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		defer func() {
			restartAll = false
			restartJSON = false
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

		lockPID, lockAlive := manifest.ReadLockPID(workDir)
		if !lockAlive || lockPID <= 1 {
			if restartJSON {
				outBytes, _ := json.Marshal(CommandResponseJSON{
					OK:     false,
					Action: "restart",
					Error:  "no active vigiaDev session found (run 'vigiadev up' to start the environment)",
				})
				cmd.Println(string(outBytes))
				return fmt.Errorf("no active vigiaDev session found")
			}
			return fmt.Errorf("no active vigiaDev session found for project in %s\nRun 'vigiadev up' to start the environment", workDir)
		}

		socketPath := ipc.ResolveSocketPath(workDir)
		if m, err := manifest.ReadManifest(workDir); err == nil && m != nil && m.SocketPath != "" {
			socketPath = m.SocketPath
		}

		client := ipc.NewClient(socketPath)

		ctx, cancel := context.WithTimeout(context.Background(), restartTimeout)
		defer cancel()

		if restartAll {
			resp, err := client.SendCommand(ctx, ipc.Request{
				Action:  ipc.ActionRestart,
				Service: "all",
				Timeout: restartTimeout,
			})
			if err != nil {
				if restartJSON {
					outBytes, _ := json.Marshal(CommandResponseJSON{
						OK:     false,
						Action: "restart",
						Error:  err.Error(),
					})
					cmd.Println(string(outBytes))
					return err
				}
				return fmt.Errorf("failed to communicate with supervisor: %w", err)
			}

			if restartJSON {
				outBytes, _ := json.Marshal(CommandResponseJSON{
					OK:      resp.OK,
					Action:  "restart",
					Message: resp.Message,
					Error:   resp.Error,
				})
				cmd.Println(string(outBytes))
				if !resp.OK {
					return fmt.Errorf("restart all failed: %s", resp.Error)
				}
				return nil
			}

			if !resp.OK {
				cmd.Printf("❌ Failed to restart all services: %s\n", resp.Error)
				return fmt.Errorf("%s", resp.Error)
			}
			cmd.Println("✅ All services restarted successfully.")
			return nil
		}

		if len(args) == 0 {
			if restartJSON {
				outBytes, _ := json.Marshal(CommandResponseJSON{
					OK:     false,
					Action: "restart",
					Error:  "please specify at least one service to restart, or pass --all",
				})
				cmd.Println(string(outBytes))
				return fmt.Errorf("no service specified")
			}
			return fmt.Errorf("please specify at least one service to restart, or pass --all")
		}

		var results []ServiceActionResult
		var hasFailure bool

		for _, svcName := range args {
			svcName = strings.TrimSpace(svcName)
			if svcName == "" {
				continue
			}

			if !restartJSON {
				cmd.Printf("🔄 Restarting service '%s'...\n", svcName)
			}

			resp, err := client.SendCommand(ctx, ipc.Request{
				Action:  ipc.ActionRestart,
				Service: svcName,
				Timeout: restartTimeout,
			})

			if err != nil {
				hasFailure = true
				results = append(results, ServiceActionResult{
					Service: svcName,
					OK:      false,
					Error:   err.Error(),
				})
				if !restartJSON {
					cmd.Printf("❌ Failed to restart '%s': %v\n", svcName, err)
				}
				continue
			}

			if !resp.OK {
				hasFailure = true
				results = append(results, ServiceActionResult{
					Service: svcName,
					OK:      false,
					State:   resp.State,
					Error:   resp.Error,
				})
				if !restartJSON {
					cmd.Printf("❌ Failed to restart '%s': %s (state: %s)\n", svcName, resp.Error, resp.State)
				}
			} else {
				results = append(results, ServiceActionResult{
					Service: svcName,
					OK:      true,
					State:   resp.State,
					Message: resp.Message,
				})
				if !restartJSON {
					cmd.Printf("✅ Service '%s' restarted successfully (state: %s)\n", svcName, resp.State)
				}
			}
		}

		if restartJSON {
			outBytes, _ := json.Marshal(CommandResponseJSON{
				OK:       !hasFailure,
				Action:   "restart",
				Services: results,
			})
			cmd.Println(string(outBytes))
		}

		if hasFailure {
			return fmt.Errorf("one or more services failed to restart")
		}
		return nil
	},
}

func init() {
	restartCmd.Flags().BoolVarP(&restartAll, "all", "a", false, "Restart all services in the active session")
	restartCmd.Flags().BoolVar(&restartJSON, "json", false, "Output results in JSON format")
	restartCmd.Flags().DurationVarP(&restartTimeout, "timeout", "t", 60*time.Second, "Timeout duration for restart operation")
	rootCmd.AddCommand(restartCmd)
}
