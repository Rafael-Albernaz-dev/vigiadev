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
	startAll     bool
	startJSON    bool
	startTimeout time.Duration
)

var startCmd = &cobra.Command{
	Use:          "start [services...]",
	Short:        "Start one or more services in the active vigiaDev session via supervisor IPC",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		defer func() {
			startAll = false
			startJSON = false
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
			if startJSON {
				outBytes, _ := json.Marshal(CommandResponseJSON{
					OK:     false,
					Action: "start",
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

		ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
		defer cancel()

		if startAll {
			resp, err := client.SendCommand(ctx, ipc.Request{
				Action:  ipc.ActionStart,
				Service: "all",
				Timeout: startTimeout,
			})
			if err != nil {
				if startJSON {
					outBytes, _ := json.Marshal(CommandResponseJSON{
						OK:     false,
						Action: "start",
						Error:  err.Error(),
					})
					cmd.Println(string(outBytes))
					return err
				}
				return fmt.Errorf("failed to communicate with supervisor: %w", err)
			}

			if startJSON {
				outBytes, _ := json.Marshal(CommandResponseJSON{
					OK:      resp.OK,
					Action:  "start",
					Message: resp.Message,
					Error:   resp.Error,
				})
				cmd.Println(string(outBytes))
				if !resp.OK {
					return fmt.Errorf("start all failed: %s", resp.Error)
				}
				return nil
			}

			if !resp.OK {
				cmd.Printf("❌ Failed to start all services: %s\n", resp.Error)
				return fmt.Errorf("%s", resp.Error)
			}
			cmd.Println("✅ All services started successfully.")
			return nil
		}

		if len(args) == 0 {
			if startJSON {
				outBytes, _ := json.Marshal(CommandResponseJSON{
					OK:     false,
					Action: "start",
					Error:  "please specify at least one service to start, or pass --all",
				})
				cmd.Println(string(outBytes))
				return fmt.Errorf("no service specified")
			}
			return fmt.Errorf("please specify at least one service to start, or pass --all")
		}

		var results []ServiceActionResult
		var hasFailure bool

		for _, svcName := range args {
			svcName = strings.TrimSpace(svcName)
			if svcName == "" {
				continue
			}

			if !startJSON {
				cmd.Printf("🚀 Starting service '%s'...\n", svcName)
			}

			resp, err := client.SendCommand(ctx, ipc.Request{
				Action:  ipc.ActionStart,
				Service: svcName,
				Timeout: startTimeout,
			})

			if err != nil {
				hasFailure = true
				results = append(results, ServiceActionResult{
					Service: svcName,
					OK:      false,
					Error:   err.Error(),
				})
				if !startJSON {
					cmd.Printf("❌ Failed to start '%s': %v\n", svcName, err)
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
				if !startJSON {
					cmd.Printf("❌ Failed to start '%s': %s (state: %s)\n", svcName, resp.Error, resp.State)
				}
			} else {
				results = append(results, ServiceActionResult{
					Service: svcName,
					OK:      true,
					State:   resp.State,
					Message: resp.Message,
				})
				if !startJSON {
					cmd.Printf("✅ Service '%s' started successfully (state: %s)\n", svcName, resp.State)
				}
			}
		}

		if startJSON {
			outBytes, _ := json.Marshal(CommandResponseJSON{
				OK:       !hasFailure,
				Action:   "start",
				Services: results,
			})
			cmd.Println(string(outBytes))
		}

		if hasFailure {
			return fmt.Errorf("one or more services failed to start")
		}
		return nil
	},
}

func init() {
	startCmd.Flags().BoolVarP(&startAll, "all", "a", false, "Start all services in the active session")
	startCmd.Flags().BoolVar(&startJSON, "json", false, "Output results in JSON format")
	startCmd.Flags().DurationVarP(&startTimeout, "timeout", "t", 60*time.Second, "Timeout duration for start operation")
	rootCmd.AddCommand(startCmd)
}
