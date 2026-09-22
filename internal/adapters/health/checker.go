package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

// Checker executa healthchecks semânticos de prontidão (HTTP, TCP, Command).
type Checker struct {
	client *http.Client
}

// NewChecker instancia o verificador de saúde.
func NewChecker() *Checker {
	return &Checker{
		client: &http.Client{
			// Timeout por requisição individual
			Timeout: 2 * time.Second,
		},
	}
}

// CheckSingle executa uma única sondagem conforme a estratégia declarada.
func (c *Checker) CheckSingle(ctx context.Context, cfg *domain.HealthCheckConfig, defaultHost string) error {
	if cfg == nil {
		return nil
	}

	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch cfg.Type {
	case domain.HealthCheckHTTP:
		req, err := http.NewRequestWithContext(subCtx, http.MethodGet, cfg.URL, nil)
		if err != nil {
			return err
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		expected := cfg.ExpectedStatus
		if expected <= 0 {
			expected = 200
		}
		if resp.StatusCode != expected {
			return fmt.Errorf("status HTTP retornado %d (esperado: %d)", resp.StatusCode, expected)
		}
		return nil

	case domain.HealthCheckTCP:
		host := defaultHost
		if host == "" {
			host = "127.0.0.1"
		}
		addr := net.JoinHostPort(host, strconv.Itoa(cfg.Port))
		d := net.Dialer{}
		conn, err := d.DialContext(subCtx, "tcp", addr)
		if err != nil {
			return err
		}
		_ = conn.Close()
		return nil

	case domain.HealthCheckCommand:
		if len(cfg.Command) == 0 {
			return fmt.Errorf("comando de healthcheck vazio")
		}
		cmd := exec.CommandContext(subCtx, cfg.Command[0], cfg.Command[1:]...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("comando falhou: %w", err)
		}
		return nil

	default:
		return fmt.Errorf("tipo de healthcheck não suportado: %s", cfg.Type)
	}
}

// WaitUntilHealthy repete a sondagem até obter sucesso ou esgotar retries.
// Retorna a latência da última sondagem bem-sucedida.
func (c *Checker) WaitUntilHealthy(ctx context.Context, cfg *domain.HealthCheckConfig, defaultHost string) (time.Duration, error) {
	if cfg == nil {
		return 0, nil
	}

	retries := cfg.Retries
	if retries <= 0 {
		retries = 30
	}

	interval := time.Duration(cfg.IntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}

	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}

		startProbe := time.Now()
		err := c.CheckSingle(ctx, cfg, defaultHost)
		if err == nil {
			return time.Since(startProbe), nil
		}
		lastErr = err

		time.Sleep(interval)
	}

	return 0, fmt.Errorf("healthcheck esgotou %d tentativas sem sucesso: %w", retries, lastErr)
}
