package docker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/docker"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// mockDockerClient implementa docker.DockerClient para testes unitários.
type mockDockerClient struct {
	containers []types.Container
	inspectMap map[string]types.ContainerJSON
	logsMap    map[string]io.ReadCloser
	statsMap   map[string]container.StatsResponseReader
	stopped    []string
	restarted  []string
	mu         sync.Mutex
}

func (m *mockDockerClient) ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.containers, nil
}

func (m *mockDockerClient) ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.inspectMap[containerID]; ok {
		return c, nil
	}
	return types.ContainerJSON{}, nil
}

func (m *mockDockerClient) ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.logsMap[containerID]; ok {
		return r, nil
	}
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func (m *mockDockerClient) ContainerStats(ctx context.Context, containerID string, stream bool) (container.StatsResponseReader, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.statsMap[containerID]; ok {
		return r, nil
	}
	return container.StatsResponseReader{Body: io.NopCloser(bytes.NewReader(nil))}, nil
}

func (m *mockDockerClient) ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = append(m.stopped, containerID)
	return nil
}

func (m *mockDockerClient) ContainerRestart(ctx context.Context, containerID string, options container.StopOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restarted = append(m.restarted, containerID)
	return nil
}

func (m *mockDockerClient) Close() error {
	return nil
}

// mockComposeRunner implementa docker.ComposeRunner para interceptar comandos CLI.
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

func TestDockerManager_StartAndFindContainer(t *testing.T) {
	bus := domain.NewEventBus()
	runner := &mockComposeRunner{}
	client := &mockDockerClient{
		containers: []types.Container{
			{
				ID:    "c1234567890ab",
				Names: []string{"/myproject-redis-1"},
				Image: "redis:7-alpine",
				Labels: map[string]string{
					"com.docker.compose.project": "myproject",
					"com.docker.compose.service": "redis",
				},
				State: "running",
			},
		},
	}

	dm := docker.NewDockerManagerWithClient(bus, "/path/to/myproject", client, runner)

	info, err := dm.StartComposeService(context.Background(), "redis", "/path/to/myproject")
	if err != nil {
		t.Fatalf("StartComposeService falhou inesperadamente: %v", err)
	}

	if info.ID != "c1234567890ab" {
		t.Errorf("esperava container ID 'c1234567890ab', obteve '%s'", info.ID)
	}
	if info.Name != "myproject-redis-1" {
		t.Errorf("esperava container name 'myproject-redis-1', obteve '%s'", info.Name)
	}

	if len(runner.upCalls) != 1 || runner.upCalls[0] != "redis" {
		t.Errorf("esperava chamada Up('redis'), obteve: %v", runner.upCalls)
	}
}

func TestDockerManager_StreamLogs_StdCopyDemux(t *testing.T) {
	bus := domain.NewEventBus()
	runner := &mockComposeRunner{}

	var muxBuf bytes.Buffer
	outWriter := stdcopy.NewStdWriter(&muxBuf, stdcopy.Stdout)
	errWriter := stdcopy.NewStdWriter(&muxBuf, stdcopy.Stderr)

	_, _ = outWriter.Write([]byte("Ready to accept connections\n"))
	_, _ = errWriter.Write([]byte("Warning: memory overcommit disabled\n"))

	client := &mockDockerClient{
		inspectMap: map[string]types.ContainerJSON{
			"c123": {
				Config: &container.Config{Tty: false},
			},
		},
		logsMap: map[string]io.ReadCloser{
			"c123": io.NopCloser(&muxBuf),
		},
	}

	var receivedStdout, receivedStderr bool
	var mu sync.Mutex

	bus.Subscribe(func(e domain.Event) {
		logEvt, ok := e.(domain.LogLineProduced)
		if !ok || logEvt.Service != "redis" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if !logEvt.IsError && logEvt.Line == "Ready to accept connections" {
			receivedStdout = true
		}
		if logEvt.IsError && logEvt.Line == "Warning: memory overcommit disabled" {
			receivedStderr = true
		}
	})

	dm := docker.NewDockerManagerWithClient(bus, "/path/to/project", client, runner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := dm.StreamLogs(ctx, "c123", "redis")
	if err != nil {
		t.Fatalf("StreamLogs falhou: %v", err)
	}

	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if !receivedStdout {
		t.Errorf("esperava receber linha de stdout demultiplexada")
	}
	if !receivedStderr {
		t.Errorf("esperava receber linha de stderr demultiplexada com IsError=true")
	}
}

func TestDockerManager_StreamLogs_TTY(t *testing.T) {
	bus := domain.NewEventBus()
	runner := &mockComposeRunner{}

	rawLogs := bytes.NewBufferString("Node server listening on port 3000\n")

	client := &mockDockerClient{
		inspectMap: map[string]types.ContainerJSON{
			"c456": {
				Config: &container.Config{Tty: true},
			},
		},
		logsMap: map[string]io.ReadCloser{
			"c456": io.NopCloser(rawLogs),
		},
	}

	var receivedLine string
	var mu sync.Mutex

	bus.Subscribe(func(e domain.Event) {
		logEvt, ok := e.(domain.LogLineProduced)
		if ok && logEvt.Service == "web" {
			mu.Lock()
			receivedLine = logEvt.Line
			mu.Unlock()
		}
	})

	dm := docker.NewDockerManagerWithClient(bus, "/path/to/project", client, runner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := dm.StreamLogs(ctx, "c456", "web")
	if err != nil {
		t.Fatalf("StreamLogs com TTY falhou: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if receivedLine != "Node server listening on port 3000" {
		t.Errorf("esperava linha de log '%s', obteve '%s'", "Node server listening on port 3000", receivedLine)
	}
}

func TestDockerManager_TelemetryCalculations(t *testing.T) {
	stats := &container.StatsResponse{
		Stats: container.Stats{
			CPUStats: container.CPUStats{
				CPUUsage: container.CPUUsage{
					TotalUsage: 200000000,
				},
				SystemUsage: 1000000000,
				OnlineCPUs:  2,
			},
			PreCPUStats: container.CPUStats{
				CPUUsage: container.CPUUsage{
					TotalUsage: 100000000,
				},
				SystemUsage: 500000000,
			},
			MemoryStats: container.MemoryStats{
				Usage: 104857600, // 100 MB
				Stats: map[string]uint64{
					"inactive_file": 20971520, // 20 MB cache
				},
			},
			BlkioStats: container.BlkioStats{
				IoServiceBytesRecursive: []container.BlkioStatEntry{
					{Op: "Read", Value: 1024},
					{Op: "Write", Value: 2048},
				},
			},
		},
	}

	// 1. CPU %: deltaCPU = 100000000, deltaSys = 500000000 -> (100000000 / 500000000) * 2 * 100 = 40.0%
	cpu := docker.CalculateCPUPercent(stats, nil)
	if cpu < 39.9 || cpu > 40.1 {
		t.Errorf("esperava CPU ~40.0%%, obteve %.2f%%", cpu)
	}

	// 2. RAM: 100 MB - 20 MB = 80 MB (83886080 bytes)
	mem := docker.CalculateMemoryUsage(stats)
	expectedMem := uint64(104857600 - 20971520)
	if mem != expectedMem {
		t.Errorf("esperava memória %d bytes, obteve %d", expectedMem, mem)
	}

	// 3. Disco: Read 1024, Write 2048
	readB, writeB := docker.CalculateDiskIO(stats)
	if readB != 1024 || writeB != 2048 {
		t.Errorf("esperava read 1024 e write 2048, obteve %d e %d", readB, writeB)
	}
}

func TestDockerManager_StreamTelemetry(t *testing.T) {
	bus := domain.NewEventBus()
	runner := &mockComposeRunner{}

	stats := container.StatsResponse{
		Stats: container.Stats{
			CPUStats: container.CPUStats{
				CPUUsage:    container.CPUUsage{TotalUsage: 200000000},
				SystemUsage: 1000000000,
				OnlineCPUs:  1,
			},
			PreCPUStats: container.CPUStats{
				CPUUsage:    container.CPUUsage{TotalUsage: 100000000},
				SystemUsage: 500000000,
			},
			MemoryStats: container.MemoryStats{
				Usage: 52428800,
			},
		},
	}

	data, _ := json.Marshal(stats)
	reader := container.StatsResponseReader{
		Body: io.NopCloser(bytes.NewReader(data)),
	}

	client := &mockDockerClient{
		statsMap: map[string]container.StatsResponseReader{
			"c789": reader,
		},
	}

	var receivedTelemetry bool
	var mu sync.Mutex

	bus.Subscribe(func(e domain.Event) {
		tEvt, ok := e.(domain.TelemetryUpdated)
		if ok && tEvt.Service == "postgres" && tEvt.MemoryBytes == 52428800 {
			mu.Lock()
			receivedTelemetry = true
			mu.Unlock()
		}
	})

	dm := docker.NewDockerManagerWithClient(bus, "/path/to/project", client, runner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = dm.StreamTelemetry(ctx, "c789", "postgres")

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if !receivedTelemetry {
		t.Errorf("esperava receber evento TelemetryUpdated via Docker API")
	}
}

func TestDockerManager_RestartAndStop(t *testing.T) {
	bus := domain.NewEventBus()
	runner := &mockComposeRunner{}
	client := &mockDockerClient{
		containers: []types.Container{
			{
				ID:    "c999",
				Names: []string{"/app-redis"},
				Labels: map[string]string{
					"com.docker.compose.service": "redis",
				},
			},
		},
	}

	dm := docker.NewDockerManagerWithClient(bus, "/app", client, runner)

	_, err := dm.StartComposeService(context.Background(), "redis", "/app")
	if err != nil {
		t.Fatalf("erro ao iniciar: %v", err)
	}

	// Testa Restart
	if err := dm.RestartComposeService(context.Background(), "redis", "/app"); err != nil {
		t.Fatalf("Restart falhou: %v", err)
	}
	if len(runner.restartCalls) != 1 || runner.restartCalls[0] != "redis" {
		t.Errorf("esperava restart('redis'), obteve %v", runner.restartCalls)
	}

	// Testa StopAll
	if err := dm.StopAll(context.Background()); err != nil {
		t.Fatalf("StopAll falhou: %v", err)
	}
	if len(runner.stopCalls) != 1 || runner.stopCalls[0] != "redis" {
		t.Errorf("esperava stop('redis'), obteve %v", runner.stopCalls)
	}
}

func TestFindComposeFile(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Sem nenhum arquivo compose
	if f := docker.FindComposeFile(tempDir); f != "" {
		t.Errorf("esperava string vazia quando não há compose, obteve '%s'", f)
	}

	// 2. Com arquivo não-convencional (ex: docker-compose.viabilidade.yml)
	customFile := filepath.Join(tempDir, "docker-compose.viabilidade.yml")
	_ = os.WriteFile(customFile, []byte("services: {}"), 0644)

	if f := docker.FindComposeFile(tempDir); f != "docker-compose.viabilidade.yml" {
		t.Errorf("esperava 'docker-compose.viabilidade.yml', obteve '%s'", f)
	}

	// 3. Com arquivo canônico (compose.yaml deve ter precedência máxima)
	canonicFile := filepath.Join(tempDir, "compose.yaml")
	_ = os.WriteFile(canonicFile, []byte("services: {}"), 0644)

	if f := docker.FindComposeFile(tempDir); f != "compose.yaml" {
		t.Errorf("esperava precedência de 'compose.yaml', obteve '%s'", f)
	}
}

