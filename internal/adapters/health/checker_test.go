package health_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/health"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestChecker_HTTP(t *testing.T) {
	checker := health.NewChecker()

	// 1. Servidor HTTP de teste retornando 200 OK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	cfg := &domain.HealthCheckConfig{
		Type:           domain.HealthCheckHTTP,
		URL:            server.URL + "/health",
		ExpectedStatus: 200,
		TimeoutMs:      1000,
	}

	ctx := context.Background()
	if err := checker.CheckSingle(ctx, cfg, "127.0.0.1"); err != nil {
		t.Fatalf("esperava sucesso no healthcheck HTTP, obteve: %v", err)
	}

	// 2. Esperando status 201 mas recebendo 200 deve falhar
	cfg.ExpectedStatus = 201
	if err := checker.CheckSingle(ctx, cfg, "127.0.0.1"); err == nil {
		t.Fatal("esperava erro por status diferente do esperado, mas teve sucesso")
	}
}

func TestChecker_TCP(t *testing.T) {
	checker := health.NewChecker()

	// Abre socket temporário
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	cfg := &domain.HealthCheckConfig{
		Type:      domain.HealthCheckTCP,
		Port:      port,
		TimeoutMs: 500,
	}

	ctx := context.Background()
	if err := checker.CheckSingle(ctx, cfg, "127.0.0.1"); err != nil {
		t.Fatalf("esperava sucesso no TCP handshake, obteve: %v", err)
	}

	// Testando porta fechada
	cfg.Port = 65530 // porta improvável de estar em uso
	if err := checker.CheckSingle(ctx, cfg, "127.0.0.1"); err == nil {
		t.Fatal("esperava falha ao tentar conectar em porta fechada")
	}
}

func TestChecker_Command(t *testing.T) {
	checker := health.NewChecker()
	ctx := context.Background()

	// 1. Comando de sucesso
	cfgOk := &domain.HealthCheckConfig{
		Type:      domain.HealthCheckCommand,
		Command:   []string{"sh", "-c", "exit 0"},
		TimeoutMs: 500,
	}
	if err := checker.CheckSingle(ctx, cfgOk, "127.0.0.1"); err != nil {
		t.Fatalf("esperava sucesso no comando, obteve: %v", err)
	}

	// 2. Comando com falha
	cfgFail := &domain.HealthCheckConfig{
		Type:      domain.HealthCheckCommand,
		Command:   []string{"sh", "-c", "exit 1"},
		TimeoutMs: 500,
	}
	if err := checker.CheckSingle(ctx, cfgFail, "127.0.0.1"); err == nil {
		t.Fatal("esperava erro de execução no comando de saída 1")
	}
}

func TestChecker_WaitUntilHealthy_Timeout(t *testing.T) {
	checker := health.NewChecker()

	cfg := &domain.HealthCheckConfig{
		Type:       domain.HealthCheckTCP,
		Port:       65530,
		TimeoutMs:  50,
		IntervalMs: 50,
		Retries:    2,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := checker.WaitUntilHealthy(ctx, cfg, "127.0.0.1")
	if err == nil {
		t.Fatal("esperava erro ao esgotar tentativas")
	}
}
