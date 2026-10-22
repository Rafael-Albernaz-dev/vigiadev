package commands_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/cmd/vigiadev/commands"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ipc"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
)

type dummyIPCHandler struct {
	restarted []string
	stopped   []string
	started   []string
}

func (d *dummyIPCHandler) HandleRequest(ctx context.Context, req ipc.Request) ipc.Response {
	switch req.Action {
	case ipc.ActionPing:
		return ipc.Response{OK: true, Message: "pong"}
	case ipc.ActionRestart:
		if req.Service == "nonexistent" {
			return ipc.Response{OK: false, Service: req.Service, Error: "service 'nonexistent' not found", State: "failed"}
		}
		d.restarted = append(d.restarted, req.Service)
		return ipc.Response{OK: true, Service: req.Service, State: "healthy", Message: "restarted successfully"}
	case ipc.ActionStop:
		d.stopped = append(d.stopped, req.Service)
		return ipc.Response{OK: true, Service: req.Service, State: "stopped", Message: "stopped successfully"}
	case ipc.ActionStart:
		d.started = append(d.started, req.Service)
		return ipc.Response{OK: true, Service: req.Service, State: "healthy", Message: "started successfully"}
	case ipc.ActionStatus:
		return ipc.Response{
			OK: true,
			Services: map[string]ipc.ServiceStatusInfo{
				"worker": {Name: "worker", State: "healthy", Port: 8080, PID: os.Getpid()},
			},
		}
	default:
		return ipc.Response{OK: false, Error: "unknown action"}
	}
}

func TestRestartCommand_NoActiveSession(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	// Execute restart without active session
	rootCmd := commands.GetRootCmd()
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs([]string{"restart", "worker"})

	err = rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error when no session is active, got nil")
	}
	if !strings.Contains(err.Error(), "no active vigiaDev session found") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestRestartCommand_NoActiveSession_JSON(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	rootCmd := commands.GetRootCmd()
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs([]string{"restart", "worker", "--json"})

	_ = rootCmd.Execute()
	var resp commands.CommandResponseJSON
	if err := json.Unmarshal(outBuf.Bytes(), &resp); err != nil {
		t.Fatalf("expected JSON output, got: %s (err: %v)", outBuf.String(), err)
	}
	if resp.OK {
		t.Fatalf("expected ok: false in JSON response, got: %+v", resp)
	}
}

func TestRestartCommand_WithActiveSession(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	// Setup fake active session with IPC
	sockPath := filepath.Join(dir, manifest.VigiaDir, "ipc.sock")
	handler := &dummyIPCHandler{}
	srv := ipc.NewServer(sockPath, handler)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start mock IPC: %v", err)
	}
	defer srv.Close()

	m := &manifest.RunManifest{
		RunID:       "test-run",
		ProjectName: "test-proj",
		SocketPath:  sockPath,
		Services: map[string]manifest.ServiceManifest{
			"worker": {Name: "worker", PID: os.Getpid(), PGID: os.Getpid()},
		},
	}
	if err := manifest.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.VigiaDir, manifest.LockFile), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}

	rootCmd := commands.GetRootCmd()

	// 1. Restart worker
	var outBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetArgs([]string{"restart", "worker"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	if !strings.Contains(outBuf.String(), "restarted successfully") {
		t.Fatalf("expected success message, got: %s", outBuf.String())
	}
	if len(handler.restarted) != 1 || handler.restarted[0] != "worker" {
		t.Fatalf("handler did not record restart: %+v", handler.restarted)
	}

	// 2. Restart worker with --json
	outBuf.Reset()
	rootCmd.SetArgs([]string{"restart", "worker", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("restart --json failed: %v", err)
	}
	var jsonResp commands.CommandResponseJSON
	if err := json.Unmarshal(outBuf.Bytes(), &jsonResp); err != nil {
		t.Fatalf("failed to parse JSON response: %v\nOutput: %s", err, outBuf.String())
	}
	if !jsonResp.OK || len(jsonResp.Services) == 0 || !jsonResp.Services[0].OK {
		t.Fatalf("unexpected JSON response: %+v", jsonResp)
	}

	// 3. Restart nonexistent service returns error
	outBuf.Reset()
	rootCmd.SetArgs([]string{"restart", "nonexistent"})
	err = rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error restarting nonexistent service, got nil")
	}

	// 4. Restart --all
	outBuf.Reset()
	rootCmd.SetArgs([]string{"restart", "--all"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("restart --all failed: %v", err)
	}
	if !strings.Contains(outBuf.String(), "All services restarted successfully") {
		t.Fatalf("expected all restarted message, got: %s", outBuf.String())
	}
}
