package ipc

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
)

// Action represents an IPC operation request.
type Action string

const (
	ActionPing    Action = "ping"
	ActionRestart Action = "restart"
	ActionStop    Action = "stop"
	ActionStart   Action = "start"
	ActionStatus  Action = "status"
)

// Request defines the JSON structure sent over the IPC Unix domain socket.
type Request struct {
	Action  Action        `json:"action"`
	Service string        `json:"service,omitempty"`
	Timeout time.Duration `json:"timeout,omitempty"`
	Args    []string      `json:"args,omitempty"`
}

// ServiceStatusInfo describes the live runtime and configuration state of a service.
type ServiceStatusInfo struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Port         int    `json:"port,omitempty"`
	OriginalPort int    `json:"original_port,omitempty"`
	URL          string `json:"url,omitempty"`
	PID          int    `json:"pid,omitempty"`
	PGID         int    `json:"pgid,omitempty"`
	ContainerID  string `json:"container_id,omitempty"`
	IsContainer  bool   `json:"is_container,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

// Response defines the JSON structure returned by the supervisor over IPC.
type Response struct {
	OK       bool                         `json:"ok"`
	Message  string                       `json:"message,omitempty"`
	Error    string                       `json:"error,omitempty"`
	Service  string                       `json:"service,omitempty"`
	State    string                       `json:"state,omitempty"`
	Services map[string]ServiceStatusInfo `json:"services,omitempty"`
	Data     map[string]interface{}       `json:"data,omitempty"`
}

// ResolveSocketPath calculates a safe, POSIX-compliant Unix domain socket path.
// If the path within .vigiadev/ is <= 100 bytes, it uses .vigiadev/ipc.sock.
// Otherwise, it falls back to /tmp with a hash to prevent sockaddr_un overflow (104/108 bytes limit).
func ResolveSocketPath(workDir string) string {
	stdPath := filepath.Join(workDir, manifest.VigiaDir, "ipc.sock")
	if len(stdPath) <= 100 {
		return stdPath
	}
	h := sha256.Sum256([]byte(workDir))
	return filepath.Join("/tmp", fmt.Sprintf("vigiadev-%x.sock", h[:8]))
}
