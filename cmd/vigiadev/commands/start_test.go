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

func TestStartCommand_NoActiveSession(t *testing.T) {
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
	rootCmd.SetArgs([]string{"start", "worker"})

	err = rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error when no session is active, got nil")
	}
	if !strings.Contains(err.Error(), "no active vigiaDev session found") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestStartCommand_WithActiveSession(t *testing.T) {
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

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

	// 1. Start worker
	var outBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetArgs([]string{"start", "worker"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if !strings.Contains(outBuf.String(), "started successfully") {
		t.Fatalf("expected success message, got: %s", outBuf.String())
	}
	if len(handler.started) != 1 || handler.started[0] != "worker" {
		t.Fatalf("handler did not record start: %+v", handler.started)
	}

	// 2. Start worker with --json
	outBuf.Reset()
	rootCmd.SetArgs([]string{"start", "worker", "--json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("start --json failed: %v", err)
	}
	var jsonResp commands.CommandResponseJSON
	if err := json.Unmarshal(outBuf.Bytes(), &jsonResp); err != nil {
		t.Fatalf("failed to parse JSON response: %v\nOutput: %s", err, outBuf.String())
	}
	if !jsonResp.OK || len(jsonResp.Services) == 0 || !jsonResp.Services[0].OK {
		t.Fatalf("unexpected JSON response: %+v", jsonResp)
	}
}
