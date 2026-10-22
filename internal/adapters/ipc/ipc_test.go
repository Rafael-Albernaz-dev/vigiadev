package ipc_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ipc"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

type mockHandler struct {
	lastReq ipc.Request
	resp    ipc.Response
}

func (m *mockHandler) HandleRequest(ctx context.Context, req ipc.Request) ipc.Response {
	m.lastReq = req
	if m.resp.OK || m.resp.Error != "" || m.resp.Message != "" {
		return m.resp
	}
	switch req.Action {
	case ipc.ActionPing:
		return ipc.Response{OK: true, Message: "pong"}
	case ipc.ActionRestart:
		return ipc.Response{OK: true, Service: req.Service, State: "healthy", Message: "restarted"}
	case ipc.ActionStop:
		return ipc.Response{OK: true, Service: req.Service, State: "stopped"}
	case ipc.ActionStart:
		return ipc.Response{OK: true, Service: req.Service, State: "healthy"}
	case ipc.ActionStatus:
		return ipc.Response{
			OK: true,
			Services: map[string]ipc.ServiceStatusInfo{
				"backend": {Name: "backend", State: "healthy", Port: 3000, PID: 1234},
			},
		}
	default:
		return ipc.Response{OK: false, Error: "unknown action"}
	}
}

func TestIPC_ServerClient_PingAndLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	sockPath := filepath.Join(tempDir, "ipc.sock")

	handler := &mockHandler{}
	srv := ipc.NewServer(sockPath, handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start IPC server: %v", err)
	}
	defer srv.Close()

	if _, err := os.Stat(sockPath); err != nil {
		t.Fatalf("socket file was not created: %v", err)
	}

	client := ipc.NewClient(sockPath)

	// 1. Ping
	resp, err := client.SendCommand(context.Background(), ipc.Request{Action: ipc.ActionPing})
	if err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	if !resp.OK || resp.Message != "pong" {
		t.Fatalf("unexpected ping response: %+v", resp)
	}

	// 2. Restart single service
	resp, err = client.SendCommand(context.Background(), ipc.Request{
		Action:  ipc.ActionRestart,
		Service: "backend",
	})
	if err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	if !resp.OK || resp.Service != "backend" || resp.State != "healthy" {
		t.Fatalf("unexpected restart response: %+v", resp)
	}
	if handler.lastReq.Service != "backend" {
		t.Fatalf("handler did not receive expected service: %s", handler.lastReq.Service)
	}

	// 3. Status
	resp, err = client.SendCommand(context.Background(), ipc.Request{Action: ipc.ActionStatus})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !resp.OK || len(resp.Services) == 0 || resp.Services["backend"].Port != 3000 {
		t.Fatalf("unexpected status response: %+v", resp)
	}

	// 4. Close server and verify socket removed
	if err := srv.Close(); err != nil {
		t.Fatalf("failed to close server: %v", err)
	}

	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Fatalf("socket file was not removed after close: %v", err)
	}

	// 5. Client should fail gracefully when server is closed
	_, err = client.SendCommand(context.Background(), ipc.Request{Action: ipc.ActionPing})
	if err == nil {
		t.Fatalf("expected error when sending command to closed server, got nil")
	}
	if !strings.Contains(err.Error(), domain.ErrNoActiveSession.Error()) {
		t.Fatalf("expected ErrNoActiveSession, got: %v", err)
	}
}

func TestIPC_StaleSocketRecovery(t *testing.T) {
	tempDir := t.TempDir()
	sockPath := filepath.Join(tempDir, "ipc.sock")

	// Create a dummy file mimicking a stale dead socket left behind
	if err := os.WriteFile(sockPath, []byte("stale"), 0600); err != nil {
		t.Fatalf("failed to create stale socket: %v", err)
	}

	handler := &mockHandler{}
	srv := ipc.NewServer(sockPath, handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start IPC server over stale socket: %v", err)
	}
	defer srv.Close()

	client := ipc.NewClient(sockPath)
	resp, err := client.SendCommand(context.Background(), ipc.Request{Action: ipc.ActionPing})
	if err != nil {
		t.Fatalf("command failed on recovered socket: %v", err)
	}
	if !resp.OK || resp.Message != "pong" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestIPC_ResolveSocketPath(t *testing.T) {
	shortDir := "/home/user/project"
	path := ipc.ResolveSocketPath(shortDir)
	if !strings.HasSuffix(path, ".vigiadev/ipc.sock") {
		t.Fatalf("expected standard path for short dir, got: %s", path)
	}

	longDir := "/home/user/" + strings.Repeat("very_long_path_directory_name/", 5) + "project"
	longPath := ipc.ResolveSocketPath(longDir)
	if !strings.HasPrefix(longPath, "/tmp/vigiadev-") || !strings.HasSuffix(longPath, ".sock") {
		t.Fatalf("expected /tmp fallback for long dir, got: %s", longPath)
	}
	if len(longPath) > 100 {
		t.Fatalf("resolved long socket path exceeds 100 bytes: len=%d (%s)", len(longPath), longPath)
	}
}
