package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

// Client communicates with the vigiaDev supervisor IPC server.
type Client struct {
	socketPath string
}

// NewClient creates a new Client for the given socket path.
func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath}
}

// SendCommand sends a Request to the supervisor and waits for the Response.
func (c *Client) SendCommand(ctx context.Context, req Request) (*Response, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot connect to supervisor at %s (%v)", domain.ErrNoActiveSession, c.socketPath, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}

	if _, err := conn.Write(append(reqData, '\n')); err != nil {
		return nil, fmt.Errorf("failed to send request to supervisor: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("failed to read supervisor response: %w", err)
		}
		return nil, errors.New("empty response received from supervisor")
	}

	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("failed to decode supervisor response: %w", err)
	}

	return &resp, nil
}
