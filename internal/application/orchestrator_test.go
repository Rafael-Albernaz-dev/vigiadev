package application_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/application"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestOrchestrator_LifecycleAndManifest(t *testing.T) {
	tempDir := t.TempDir()

	cfg := &domain.VigiaConfig{
		Version:     1,
		ProjectName: "test-orch",
		Services: map[string]domain.ServiceConfig{
			"db": {
				Command: []string{"sh", "-c", "sleep 10"},
				HealthCheck: &domain.HealthCheckConfig{
					Type:       domain.HealthCheckCommand,
					Command:    []string{"sh", "-c", "exit 0"},
					IntervalMs: 50,
					TimeoutMs:  200,
					Retries:    5,
				},
			},
			"web": {
				Command:   []string{"sh", "-c", "sleep 10"},
				DependsOn: []string{"db"},
				HealthCheck: &domain.HealthCheckConfig{
					Type:       domain.HealthCheckCommand,
					Command:    []string{"sh", "-c", "exit 0"},
					IntervalMs: 50,
					TimeoutMs:  200,
					Retries:    5,
				},
			},
		},
	}

	bus := domain.NewEventBus()
	orch, err := application.NewOrchestrator(cfg, tempDir, bus)
	if err != nil {
		t.Fatalf("falha ao criar orquestrador: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- orch.Run(ctx)
	}()

	// Aguarda os serviços subirem e ficarem saudáveis
	time.Sleep(400 * time.Millisecond)

	// Valida se o manifesto foi gravado no disco
	m, err := manifest.ReadManifest(tempDir)
	if err != nil {
		t.Fatalf("esperava manifesto gravado no disco: %v", err)
	}

	if len(m.Services) != 2 {
		t.Fatalf("esperava 2 serviços no manifesto, obteve %d", len(m.Services))
	}

	// Cancela a execução (simula Ctrl+C)
	cancel()

	select {
	case err := <-errChan:
		if err != nil && err != context.Canceled {
			t.Fatalf("erro inesperado no orquestrador: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("orquestrador demorou demais para parar no teardown")
	}

	// Manifesto deve ter sido removido
	if _, err := manifest.ReadManifest(tempDir); err == nil {
		t.Fatal("manifesto deveria ter sido limpo no teardown")
	}
}

func TestOrchestrator_PortRemap(t *testing.T) {
	tempDir := t.TempDir()

	// Ocupa a porta 9100 com um socket de teste
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	busyPort := listener.Addr().(*net.TCPAddr).Port

	cfg := &domain.VigiaConfig{
		Version:     1,
		ProjectName: "test-remap",
		Services: map[string]domain.ServiceConfig{
			"api": {
				Command:    []string{"sh", "-c", "echo porta {port}; sleep 10"},
				Ports:      []int{busyPort},
				PortPolicy: domain.PortPolicyRemap,
			},
		},
	}

	bus := domain.NewEventBus()
	var remappedEvents []domain.PortRemapped
	var mu sync.Mutex

	bus.Subscribe(func(event domain.Event) {
		if remapped, ok := event.(domain.PortRemapped); ok {
			mu.Lock()
			remappedEvents = append(remappedEvents, remapped)
			mu.Unlock()
		}
	})

	orch, err := application.NewOrchestrator(cfg, tempDir, bus)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = orch.Run(ctx)
	}()

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(remappedEvents) != 1 {
		t.Fatalf("esperava 1 evento de remapeamento, obteve %d", len(remappedEvents))
	}

	if remappedEvents[0].OriginalPort != busyPort {
		t.Errorf("esperava porta original %d, obteve %d", busyPort, remappedEvents[0].OriginalPort)
	}

	if remappedEvents[0].TargetPort <= busyPort {
		t.Errorf("esperava porta remapeada maior que %d, obteve %d", busyPort, remappedEvents[0].TargetPort)
	}
}
