package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// DockerClient abstrai o cliente oficial da Engine Docker para viabilizar testes desacoplados.
type DockerClient interface {
	ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error)
	ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error)
	ContainerLogs(ctx context.Context, container string, options container.LogsOptions) (io.ReadCloser, error)
	ContainerStats(ctx context.Context, containerID string, stream bool) (container.StatsResponseReader, error)
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerRestart(ctx context.Context, containerID string, options container.StopOptions) error
	Close() error
}

// ComposeRunner abstrai as chamadas CLI do Docker Compose.
type ComposeRunner interface {
	Up(ctx context.Context, service string, workDir string) error
	Stop(ctx context.Context, service string, workDir string) error
	Restart(ctx context.Context, service string, workDir string) error
}

// DefaultComposeRunner executa comandos reais de 'docker compose'.
type DefaultComposeRunner struct{}

func (d *DefaultComposeRunner) Up(ctx context.Context, service string, workDir string) error {
	cmd := exec.CommandContext(ctx, "docker", "compose", "up", "-d", service)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose up -d falhou: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (d *DefaultComposeRunner) Stop(ctx context.Context, service string, workDir string) error {
	cmd := exec.CommandContext(ctx, "docker", "compose", "stop", service)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose stop falhou: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (d *DefaultComposeRunner) Restart(ctx context.Context, service string, workDir string) error {
	cmd := exec.CommandContext(ctx, "docker", "compose", "restart", service)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose restart falhou: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// ContainerInfo armazena metadados de container ativo descoberto.
type ContainerInfo struct {
	ID          string
	Name        string
	Image       string
	ServiceName string
}

// DockerManager gerencia containers Docker Compose, logs e telemetria.
type DockerManager struct {
	client     DockerClient
	runner     ComposeRunner
	bus        domain.EventPublisher
	workDir    string
	containers map[string]*ContainerInfo
	cancels    map[string]context.CancelFunc
	mu         sync.Mutex
}

// NewDockerManager conecta ao Docker Engine no host local.
func NewDockerManager(bus domain.EventPublisher, workDir string) (*DockerManager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("falha ao conectar ao Docker Engine: %w", err)
	}
	return NewDockerManagerWithClient(bus, workDir, cli, &DefaultComposeRunner{}), nil
}

// NewDockerManagerWithClient instancia o DockerManager com dependências injetadas.
func NewDockerManagerWithClient(bus domain.EventPublisher, workDir string, cli DockerClient, runner ComposeRunner) *DockerManager {
	return &DockerManager{
		client:     cli,
		runner:     runner,
		bus:        bus,
		workDir:    workDir,
		containers: make(map[string]*ContainerInfo),
		cancels:    make(map[string]context.CancelFunc),
	}
}

// StartComposeService inicia o serviço via docker compose e inicia logs/telemetria.
func (dm *DockerManager) StartComposeService(ctx context.Context, serviceName string, workDir string) (*ContainerInfo, error) {
	if workDir == "" {
		workDir = dm.workDir
	}

	// 1. Sobe o container sem derrubar redes ou volumes existentes (DEC-005)
	if err := dm.runner.Up(ctx, serviceName, workDir); err != nil {
		return nil, err
	}

	// 2. Descobre deterministicamente o ID e nome do container
	info, err := dm.FindContainer(ctx, serviceName, workDir)
	if err != nil {
		return nil, err
	}

	dm.mu.Lock()
	dm.containers[serviceName] = info
	if oldCancel, ok := dm.cancels[serviceName]; ok {
		oldCancel()
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	dm.cancels[serviceName] = cancel
	dm.mu.Unlock()

	// 3. Inicia streaming contínuo de logs e métricas
	_ = dm.StreamLogs(streamCtx, info.ID, serviceName)
	_ = dm.StreamTelemetry(streamCtx, info.ID, serviceName)

	return info, nil
}

// FindContainer busca o container correspondente ao serviço do compose.
func (dm *DockerManager) FindContainer(ctx context.Context, serviceName string, workDir string) (*ContainerInfo, error) {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		absWorkDir = workDir
	}
	projectName := filepath.Base(absWorkDir)

	args := filters.NewArgs()
	args.Add("label", fmt.Sprintf("com.docker.compose.service=%s", serviceName))

	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		containers, err := dm.client.ContainerList(ctx, container.ListOptions{
			All:     true,
			Filters: args,
		})
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}

		for _, c := range containers {
			cProject := c.Labels["com.docker.compose.project"]
			cDir := c.Labels["com.docker.compose.project.working_dir"]

			matchesProject := strings.EqualFold(cProject, projectName)
			matchesDir := cDir == absWorkDir || cDir == workDir

			if matchesProject || matchesDir || len(containers) == 1 {
				name := serviceName
				if len(c.Names) > 0 {
					name = strings.TrimPrefix(c.Names[0], "/")
				}
				return &ContainerInfo{
					ID:          c.ID,
					Name:        name,
					Image:       c.Image,
					ServiceName: serviceName,
				}, nil
			}
		}

		time.Sleep(100 * time.Millisecond)
	}

	if lastErr != nil {
		return nil, fmt.Errorf("erro ao listar containers para '%s': %w", serviceName, lastErr)
	}
	return nil, fmt.Errorf("container para o serviço compose '%s' não encontrado", serviceName)
}

// StreamLogs faz o streaming contínuo de logs do container para o EventBus.
func (dm *DockerManager) StreamLogs(ctx context.Context, containerID string, serviceName string) error {
	inspect, err := dm.client.ContainerInspect(ctx, containerID)
	isTTY := false
	if err == nil && inspect.Config != nil && inspect.Config.Tty {
		isTTY = true
	}

	rc, err := dm.client.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: false,
	})
	if err != nil {
		return err
	}

	if isTTY {
		go func() {
			defer rc.Close()
			scanner := bufio.NewScanner(rc)
			for scanner.Scan() {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if dm.bus != nil {
					dm.bus.Publish(domain.LogLineProduced{
						BaseEvent: domain.NewBaseEvent(),
						Service:   serviceName,
						Line:      scanner.Text(),
						IsError:   false,
					})
				}
			}
		}()
		return nil
	}

	// Não-TTY: streams multiplexados (stdout/stderr) demultiplexados com stdcopy
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()

	go func() {
		defer stdoutR.Close()
		scanner := bufio.NewScanner(stdoutR)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if dm.bus != nil {
				dm.bus.Publish(domain.LogLineProduced{
					BaseEvent: domain.NewBaseEvent(),
					Service:   serviceName,
					Line:      scanner.Text(),
					IsError:   false,
				})
			}
		}
	}()

	go func() {
		defer stderrR.Close()
		scanner := bufio.NewScanner(stderrR)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if dm.bus != nil {
				dm.bus.Publish(domain.LogLineProduced{
					BaseEvent: domain.NewBaseEvent(),
					Service:   serviceName,
					Line:      scanner.Text(),
					IsError:   true,
				})
			}
		}
	}()

	go func() {
		defer rc.Close()
		defer stdoutW.Close()
		defer stderrW.Close()
		_, _ = stdcopy.StdCopy(stdoutW, stderrW, rc)
	}()

	return nil
}

// StreamTelemetry consome o fluxo de estatísticas de CPU/RAM/Disco via Docker API.
func (dm *DockerManager) StreamTelemetry(ctx context.Context, containerID string, serviceName string) error {
	reader, err := dm.client.ContainerStats(ctx, containerID, true)
	if err != nil {
		return err
	}

	go func() {
		defer reader.Body.Close()
		dec := json.NewDecoder(reader.Body)
		var prevStats *container.StatsResponse

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			var stats container.StatsResponse
			if err := dec.Decode(&stats); err != nil {
				return
			}

			cpuPercent := CalculateCPUPercent(&stats, prevStats)
			memUsage := CalculateMemoryUsage(&stats)
			readBytes, writeBytes := CalculateDiskIO(&stats)

			if dm.bus != nil {
				dm.bus.Publish(domain.TelemetryUpdated{
					BaseEvent:      domain.NewBaseEvent(),
					Service:        serviceName,
					CPUPercent:     cpuPercent,
					MemoryBytes:    memUsage,
					DiskReadBytes:  readBytes,
					DiskWriteBytes: writeBytes,
				})
			}

			prevStats = &stats
		}
	}()

	return nil
}

// RestartComposeService reinicia exclusivamente o container do serviço focado ([r]).
func (dm *DockerManager) RestartComposeService(ctx context.Context, serviceName string, workDir string) error {
	if workDir == "" {
		workDir = dm.workDir
	}
	return dm.runner.Restart(ctx, serviceName, workDir)
}

// StopComposeService interrompe de forma limpa o container do serviço compose.
func (dm *DockerManager) StopComposeService(ctx context.Context, serviceName string, workDir string) error {
	if workDir == "" {
		workDir = dm.workDir
	}

	dm.mu.Lock()
	if cancel, ok := dm.cancels[serviceName]; ok {
		cancel()
		delete(dm.cancels, serviceName)
	}
	delete(dm.containers, serviceName)
	dm.mu.Unlock()

	return dm.runner.Stop(ctx, serviceName, workDir)
}

// StopAll encerra de forma cirúrgica apenas os containers iniciados nesta sessão.
func (dm *DockerManager) StopAll(ctx context.Context) error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	for _, cancel := range dm.cancels {
		cancel()
	}
	dm.cancels = make(map[string]context.CancelFunc)

	var errs []error
	for serviceName := range dm.containers {
		if err := dm.runner.Stop(ctx, serviceName, dm.workDir); err != nil {
			errs = append(errs, err)
		}
	}
	dm.containers = make(map[string]*ContainerInfo)

	if len(errs) > 0 {
		return fmt.Errorf("erros ao parar containers compose: %v", errs)
	}
	return nil
}

// Close libera recursos do cliente Docker.
func (dm *DockerManager) Close() error {
	if dm.client != nil {
		return dm.client.Close()
	}
	return nil
}

// CalculateCPUPercent calcula a porcentagem de uso de CPU comparando deltas com amostragem anterior.
func CalculateCPUPercent(stats *container.StatsResponse, prev *container.StatsResponse) float64 {
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage) - float64(stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemUsage) - float64(stats.PreCPUStats.SystemUsage)

	if (cpuDelta <= 0 || systemDelta <= 0) && prev != nil {
		cpuDelta = float64(stats.CPUStats.CPUUsage.TotalUsage) - float64(prev.CPUStats.CPUUsage.TotalUsage)
		systemDelta = float64(stats.CPUStats.SystemUsage) - float64(prev.CPUStats.SystemUsage)
	}

	if systemDelta <= 0 || cpuDelta <= 0 {
		return 0.0
	}

	onlineCPUs := float64(stats.CPUStats.OnlineCPUs)
	if onlineCPUs == 0 {
		onlineCPUs = float64(len(stats.CPUStats.CPUUsage.PercpuUsage))
	}
	if onlineCPUs == 0 {
		onlineCPUs = 1
	}

	return (cpuDelta / systemDelta) * onlineCPUs * 100.0
}

// CalculateMemoryUsage calcula a memória em bytes descontando cache inativo do kernel cgroup.
func CalculateMemoryUsage(stats *container.StatsResponse) uint64 {
	mem := stats.MemoryStats.Usage
	if inactive, ok := stats.MemoryStats.Stats["inactive_file"]; ok && inactive < mem {
		mem -= inactive
	} else if totalInactive, ok := stats.MemoryStats.Stats["total_inactive_file"]; ok && totalInactive < mem {
		mem -= totalInactive
	}
	return mem
}

// CalculateDiskIO calcula os bytes transferidos para leitura e gravação em disco via Blkio.
func CalculateDiskIO(stats *container.StatsResponse) (uint64, uint64) {
	var readBytes, writeBytes uint64
	for _, entry := range stats.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(entry.Op) {
		case "read":
			readBytes += entry.Value
		case "write":
			writeBytes += entry.Value
		}
	}
	return readBytes, writeBytes
}
