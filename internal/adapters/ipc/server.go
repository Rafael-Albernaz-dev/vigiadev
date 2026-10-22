package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ControlHandler defines the handler for IPC requests in the supervisor.
type ControlHandler interface {
	HandleRequest(ctx context.Context, req Request) Response
}

// Server provides Unix Domain Socket IPC for controlling the vigiaDev supervisor.
type Server struct {
	socketPath string
	handler    ControlHandler
	listener   net.Listener
	mu         sync.Mutex
	closed     bool
	wg         sync.WaitGroup
}

// NewServer creates a new IPC Server.
func NewServer(socketPath string, handler ControlHandler) *Server {
	return &Server{
		socketPath: socketPath,
		handler:    handler,
	}
}

// Start binds to the Unix socket and begins serving connections in background.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errors.New("server is closed")
	}

	// Ensure parent directory exists
	dir := filepath.Dir(s.socketPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for IPC socket: %w", err)
	}

	// Remove stale socket if not active
	if _, err := os.Stat(s.socketPath); err == nil {
		conn, dialErr := net.DialTimeout("unix", s.socketPath, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return fmt.Errorf("active IPC socket already in use at %s", s.socketPath)
		}
		_ = os.Remove(s.socketPath)
	}

	l, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on IPC socket %s: %w", s.socketPath, err)
	}
	_ = os.Chmod(s.socketPath, 0600)

	s.listener = l

	s.wg.Add(1)
	go s.serve(ctx)

	return nil
}

func (s *Server) serve(ctx context.Context) {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.Lock()
			isClosed := s.closed
			s.mu.Unlock()
			if isClosed || errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			defer c.Close()
			s.handleConnection(ctx, c)
		}(conn)
	}
}

func (s *Server) handleConnection(ctx context.Context, c net.Conn) {
	scanner := bufio.NewScanner(c)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			resp := Response{
				OK:    false,
				Error: fmt.Sprintf("invalid JSON request: %v", err),
			}
			respData, _ := json.Marshal(resp)
			_, _ = c.Write(append(respData, '\n'))
			return
		}

		reqCtx := ctx
		if req.Timeout > 0 {
			var cancel context.CancelFunc
			reqCtx, cancel = context.WithTimeout(ctx, req.Timeout)
			defer cancel()
		}

		resp := s.handler.HandleRequest(reqCtx, req)
		respData, err := json.Marshal(resp)
		if err != nil {
			respData = []byte(fmt.Sprintf(`{"ok":false,"error":%q}`, err.Error()))
		}
		_, _ = c.Write(append(respData, '\n'))
	}
}

// Close gracefully stops the IPC server and removes the socket.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}
	s.mu.Unlock()

	s.wg.Wait()
	_ = os.Remove(s.socketPath)
	return err
}

// SocketPath returns the configured socket path.
func (s *Server) SocketPath() string {
	return s.socketPath
}
