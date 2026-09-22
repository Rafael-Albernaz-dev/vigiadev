package telemetry

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

// ProcessSampler armazena as amostras anteriores de um processo para cálculo de delta de CPU.
type ProcessSampler struct {
	LastTicks uint64
	LastTime  time.Time
}

// Collector realiza a amostragem contínua e não-invasiva de CPU e RAM dos processos via /proc.
type Collector struct {
	publisher domain.EventPublisher
	samplers  map[string]*ProcessSampler
	mu        sync.Mutex
	interval  time.Duration
}

// NewCollector instancia o coletor de telemetria.
func NewCollector(publisher domain.EventPublisher, interval time.Duration) *Collector {
	if interval <= 0 {
		interval = 1 * time.Second
	}
	return &Collector{
		publisher: publisher,
		samplers:  make(map[string]*ProcessSampler),
		interval:  interval,
	}
}

// ReadProcessMemory obtém o Resident Set Size (RSS) em bytes a partir de /proc/<pid>/statm.
func ReadProcessMemory(pid int) (uint64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("PID inválido: %d", pid)
	}

	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return 0, err
	}

	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, fmt.Errorf("formato inesperado em /proc/%d/statm", pid)
	}

	residentPages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}

	pageSize := uint64(os.Getpagesize())
	return residentPages * pageSize, nil
}

// ReadProcessCPUTicks lê os ticks de CPU do processo (utime + stime) a partir de /proc/<pid>/stat.
func ReadProcessCPUTicks(pid int) (uint64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("PID inválido: %d", pid)
	}

	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}

	content := string(data)
	lastParen := strings.LastIndex(content, ")")
	if lastParen == -1 || lastParen+2 >= len(content) {
		return 0, fmt.Errorf("formato inesperado em /proc/%d/stat", pid)
	}

	// Campos após ") " começam no índice 0 como 'state' (campo 3 do stat)
	fields := strings.Fields(content[lastParen+2:])
	// utime é campo 14 (índice 11), stime é campo 15 (índice 12)
	if len(fields) < 13 {
		return 0, fmt.Errorf("campos insuficientes em /proc/%d/stat", pid)
	}

	utime, err1 := strconv.ParseUint(fields[11], 10, 64)
	stime, err2 := strconv.ParseUint(fields[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, fmt.Errorf("falha ao converter ticks de CPU")
	}

	return utime + stime, nil
}

// ReadProcessDiskIO lê os bytes lidos e gravados em disco pelo processo a partir de /proc/<pid>/io.
func ReadProcessDiskIO(pid int) (uint64, uint64, error) {
	if pid <= 0 {
		return 0, 0, nil
	}

	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/io", pid))
	if err != nil {
		return 0, 0, nil // Fallback gracioso caso permissões de kernel restrinjam
	}

	var readBytes, writeBytes uint64
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val, _ := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
			if key == "read_bytes" {
				readBytes = val
			} else if key == "write_bytes" {
				writeBytes = val
			}
		}
	}

	return readBytes, writeBytes, nil
}

// ProcessMetrics agrupa as medições de um processo no SO.
type ProcessMetrics struct {
	CPUPercent     float64
	MemoryBytes    uint64
	DiskReadBytes  uint64
	DiskWriteBytes uint64
}

// SampleService coleta uma amostra instantânea de CPU, RAM e Disco para o serviço.
func (c *Collector) SampleService(service string, pid int) (*ProcessMetrics, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	mem, _ := ReadProcessMemory(pid)
	readBytes, writeBytes, _ := ReadProcessDiskIO(pid)
	ticks, err := ReadProcessCPUTicks(pid)
	if err != nil {
		return &ProcessMetrics{
			MemoryBytes:    mem,
			DiskReadBytes:  readBytes,
			DiskWriteBytes: writeBytes,
		}, err
	}

	now := time.Now()
	sampler, exists := c.samplers[service]

	var cpuPercent float64
	if exists {
		deltaTicks := float64(ticks - sampler.LastTicks)
		deltaTime := now.Sub(sampler.LastTime).Seconds()

		if deltaTime > 0 && deltaTicks >= 0 {
			cpuSeconds := deltaTicks / 100.0
			cpuPercent = (cpuSeconds / deltaTime) * 100.0
		}
	} else {
		c.samplers[service] = &ProcessSampler{}
	}

	c.samplers[service].LastTicks = ticks
	c.samplers[service].LastTime = now

	return &ProcessMetrics{
		CPUPercent:     cpuPercent,
		MemoryBytes:    mem,
		DiskReadBytes:  readBytes,
		DiskWriteBytes: writeBytes,
	}, nil
}

// Start inicia o loop periódico de amostragem em segundo plano.
func (c *Collector) Start(ctx context.Context, getProcesses func() map[string]int) {
	ticker := time.NewTicker(c.interval)

	go func() {
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				processes := getProcesses()
				for service, pid := range processes {
					if pid <= 0 {
						continue
					}
					metrics, err := c.SampleService(service, pid)
					if err == nil && c.publisher != nil {
						c.publisher.Publish(domain.TelemetryUpdated{
							BaseEvent:      domain.NewBaseEvent(),
							Service:        service,
							CPUPercent:     metrics.CPUPercent,
							MemoryBytes:    metrics.MemoryBytes,
							DiskReadBytes:  metrics.DiskReadBytes,
							DiskWriteBytes: metrics.DiskWriteBytes,
						})
					}
				}
			}
		}
	}()
}

// FormatBytes converte bytes para formato legível (KB, MB, GB).
func FormatBytes(bytes uint64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)

	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.0f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// FormatCPU formata o percentual de uso de processador.
func FormatCPU(percent float64) string {
	if percent < 0.1 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", percent)
}
