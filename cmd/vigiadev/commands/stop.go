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
	stopAll     bool
	stopJSON    bool
	stopTimeout time.Duration
)

var stopCmd = &cobra.Command{
	Use:          "stop [services...]",
	Short:        "Stop one or more services in the active vigiaDev session via supervisor IPC",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		defer func() {
			stopAll = false
			stopJSON = false
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
			if stopJSON {
				outBytes, _ := json.Marshal(CommandResponseJSON{
					OK:     false,
					Action: "stop",
					Error:  "no active vigiaDev session found",
				})
				cmd.Println(string(outBytes))
				return fmt.Errorf("no active vigiaDev session found")
			}
			return fmt.Errorf("no active vigiaDev session found for project in %s", workDir)
		}

		socketPath := ipc.ResolveSocketPath(workDir)
		if m, err := manifest.ReadManifest(workDir); err == nil && m != nil && m.SocketPath != "" {
			socketPath = m.SocketPath
		}

		client := ipc.NewClient(socketPath)

		ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()

		if stopAll {
			resp, err := client.SendCommand(ctx, ipc.Request{
				Action:  ipc.ActionStop,
				Service: "all",
				Timeout: stopTimeout,
			})
			if err != nil {
				if stopJSON {
					outBytes, _ := json.Marshal(CommandResponseJSON{
						OK:     false,
						Action: "stop",
						Error:  err.Error(),
					})
					cmd.Println(string(outBytes))
					return err
				}
				return fmt.Errorf("failed to communicate with supervisor: %w", err)
			}

			if stopJSON {
				outBytes, _ := json.Marshal(CommandResponseJSON{
					OK:      resp.OK,
					Action:  "stop",
					Message: resp.Message,
					Error:   resp.Error,
				})
				cmd.Println(string(outBytes))
				if !resp.OK {
					return fmt.Errorf("stop all failed: %s", resp.Error)
				}
				return nil
			}

			if !resp.OK {
				cmd.Printf("❌ Failed to stop all services: %s\n", resp.Error)
				return fmt.Errorf("%s", resp.Error)
			}
			cmd.Println("✅ All services stopped successfully.")
			return nil
		}

		if len(args) == 0 {
			if stopJSON {
				outBytes, _ := json.Marshal(CommandResponseJSON{
					OK:     false,
					Action: "stop",
					Error:  "please specify at least one service to stop, or pass --all",
				})
				cmd.Println(string(outBytes))
				return fmt.Errorf("no service specified")
			}
			return fmt.Errorf("please specify at least one service to stop, or pass --all")
		}

		var results []ServiceActionResult
		var hasFailure bool

		for _, svcName := range args {
			svcName = strings.TrimSpace(svcName)
			if svcName == "" {
				continue
			}

			if !stopJSON {
				cmd.Printf("⏹️ Stopping service '%s'...\n", svcName)
			}

			resp, err := client.SendCommand(ctx, ipc.Request{
				Action:  ipc.ActionStop,
				Service: svcName,
				Timeout: stopTimeout,
			})

			if err != nil {
				hasFailure = true
				results = append(results, ServiceActionResult{
					Service: svcName,
					OK:      false,
					Error:   err.Error(),
				})
				if !stopJSON {
					cmd.Printf("❌ Failed to stop '%s': %v\n", svcName, err)
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
				if !stopJSON {
					cmd.Printf("❌ Failed to stop '%s': %s (state: %s)\n", svcName, resp.Error, resp.State)
				}
			} else {
				results = append(results, ServiceActionResult{
					Service: svcName,
					OK:      true,
					State:   resp.State,
					Message: resp.Message,
				})
				if !stopJSON {
					cmd.Printf("✅ Service '%s' stopped successfully\n", svcName)
				}
			}
		}

		if stopJSON {
			outBytes, _ := json.Marshal(CommandResponseJSON{
				OK:       !hasFailure,
				Action:   "stop",
				Services: results,
			})
			cmd.Println(string(outBytes))
		}

		if hasFailure {
			return fmt.Errorf("one or more services failed to stop")
		}
		return nil
	},
}

func init() {
	stopCmd.Flags().BoolVarP(&stopAll, "all", "a", false, "Stop all services in the active session")
	stopCmd.Flags().BoolVar(&stopJSON, "json", false, "Output results in JSON format")
	stopCmd.Flags().DurationVarP(&stopTimeout, "timeout", "t", 30*time.Second, "Timeout duration for stop operation")
	rootCmd.AddCommand(stopCmd)
}
