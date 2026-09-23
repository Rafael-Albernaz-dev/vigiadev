package application_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/docker"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/application"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
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

func TestOrchestrator_RestartService(t *testing.T) {
	tempDir := t.TempDir()

	cfg := &domain.VigiaConfig{
		Version:     1,
		ProjectName: "test-restart",
		Services: map[string]domain.ServiceConfig{
			"worker": {
				Command: []string{"sh", "-c", "sleep 10"},
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
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = orch.Run(ctx)
	}()

	time.Sleep(300 * time.Millisecond)

	m1, err := manifest.ReadManifest(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	initialPID := m1.Services["worker"].PID

	// Reinicia o serviço individual
	if err := orch.RestartService(ctx, "worker"); err != nil {
		t.Fatalf("falha ao reiniciar serviço: %v", err)
	}

	m2, err := manifest.ReadManifest(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	newPID := m2.Services["worker"].PID

	if newPID == initialPID || newPID <= 0 {
		t.Fatalf("esperava novo PID diferente do anterior (%d), obteve %d", initialPID, newPID)
	}
}

type mockDockerClient struct {
	containers []types.Container
}

func (m *mockDockerClient) ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error) {
	return m.containers, nil
}
func (m *mockDockerClient) ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	return types.ContainerJSON{}, nil
}
func (m *mockDockerClient) ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (m *mockDockerClient) ContainerStats(ctx context.Context, containerID string, stream bool) (container.StatsResponseReader, error) {
	return container.StatsResponseReader{Body: io.NopCloser(bytes.NewReader(nil))}, nil
}
func (m *mockDockerClient) ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error {
	return nil
}
func (m *mockDockerClient) ContainerRestart(ctx context.Context, containerID string, options container.StopOptions) error {
	return nil
}
func (m *mockDockerClient) Close() error {
	return nil
}

type mockComposeRunner struct {
	upCalls      []string
	stopCalls    []string
	restartCalls []string
	mu           sync.Mutex
}

func (m *mockComposeRunner) Up(ctx context.Context, service string, workDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upCalls = append(m.upCalls, service)
	return nil
}

func (m *mockComposeRunner) Stop(ctx context.Context, service string, workDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCalls = append(m.stopCalls, service)
	return nil
}

func (m *mockComposeRunner) Restart(ctx context.Context, service string, workDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restartCalls = append(m.restartCalls, service)
	return nil
}

func TestOrchestrator_DockerComposeLifecycle(t *testing.T) {
	tempDir := t.TempDir()

	cfg := &domain.VigiaConfig{
		Version:     1,
		ProjectName: "compose-orch",
		Services: map[string]domain.ServiceConfig{
			"redis": {
				ComposeService: "redis",
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
				DependsOn: []string{"redis"},
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

	runner := &mockComposeRunner{}
	client := &mockDockerClient{
		containers: []types.Container{
			{
				ID:    "cid-redis-999",
				Names: []string{"/compose-orch-redis-1"},
				Image: "redis:7-alpine",
				Labels: map[string]string{
					"com.docker.compose.service": "redis",
				},
			},
		},
	}
	dm := docker.NewDockerManagerWithClient(bus, tempDir, client, runner)
	orch.SetDockerManager(dm)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- orch.Run(ctx)
	}()

	time.Sleep(400 * time.Millisecond)

	m, err := manifest.ReadManifest(tempDir)
	if err != nil {
		t.Fatalf("esperava manifesto gravado no disco: %v", err)
	}

	redisSvc, ok := m.Services["redis"]
	if !ok {
		t.Fatalf("serviço redis esperado no manifesto")
	}
	if !redisSvc.IsContainer {
		t.Errorf("esperava IsContainer=true para redis")
	}
	if redisSvc.ContainerID != "cid-redis-999" {
		t.Errorf("esperava ContainerID 'cid-redis-999', obteve '%s'", redisSvc.ContainerID)
	}

	if len(m.Containers) != 1 || m.Containers[0] != "cid-redis-999" {
		t.Errorf("esperava m.Containers conter 'cid-redis-999', obteve: %v", m.Containers)
	}

	cancel()

	select {
	case err := <-errChan:
		if err != nil && err != context.Canceled {
			t.Fatalf("erro inesperado no orquestrador: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("orquestrador demorou demais para parar no teardown")
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.stopCalls) != 1 || runner.stopCalls[0] != "redis" {
		t.Errorf("esperava Stop('redis') no teardown, obteve: %v", runner.stopCalls)
	}
}

func TestOrchestrator_DockerComposeRestart(t *testing.T) {
	tempDir := t.TempDir()

	cfg := &domain.VigiaConfig{
		Version:     1,
		ProjectName: "compose-restart",
		Services: map[string]domain.ServiceConfig{
			"db": {
				ComposeService: "db",
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
		t.Fatal(err)
	}

	runner := &mockComposeRunner{}
	client := &mockDockerClient{
		containers: []types.Container{
			{
				ID:    "cid-db-1",
				Names: []string{"/db-1"},
				Labels: map[string]string{
					"com.docker.compose.service": "db",
				},
			},
		},
	}
	dm := docker.NewDockerManagerWithClient(bus, tempDir, client, runner)
	orch.SetDockerManager(dm)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = orch.Run(ctx)
	}()

	time.Sleep(300 * time.Millisecond)

	if err := orch.RestartService(ctx, "db"); err != nil {
		t.Fatalf("falha ao reiniciar compose service: %v", err)
	}

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.restartCalls) != 1 || runner.restartCalls[0] != "db" {
		t.Errorf("esperava Restart('db'), obteve: %v", runner.restartCalls)
	}
}

