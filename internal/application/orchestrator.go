package application

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/docker"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/health"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ports"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/process"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/telemetry"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/watcher"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

// Orchestrator coordena todo o ciclo de vida do vigiaDev: DAG, portas, processos e saúde.
type Orchestrator struct {
	Config     *domain.VigiaConfig
	DAG        *DAGPlan
	Bus        *domain.EventBus
	Ports      *ports.PortResolver
	Supervisor *process.Supervisor
	Health     *health.Checker
	Telemetry  *telemetry.Collector
	Docker     *docker.DockerManager
	Watcher    *watcher.ServiceWatcher
	WorkDir    string
	RunID      string
	manifest   *manifest.RunManifest
	mu         sync.Mutex
	closeLogs  func()
	closeOnce  sync.Once
}

// NewOrchestrator cria e valida as dependências do orquestrador.
func NewOrchestrator(cfg *domain.VigiaConfig, workDir string, bus *domain.EventBus) (*Orchestrator, error) {
	dag, err := BuildDAGPlan(cfg.Services)
	if err != nil {
		return nil, err
	}

	if bus == nil {
		bus = domain.NewEventBus()
	}

	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())

	var dockerMgr *docker.DockerManager
	var hasCompose bool
	for _, svc := range cfg.Services {
		if svc.ComposeService != "" {
			hasCompose = true
			break
		}
	}

	if hasCompose {
		dm, err := docker.NewDockerManager(bus, workDir)
		if err != nil {
			return nil, fmt.Errorf("falha ao inicializar integração Docker: %w", err)
		}
		dockerMgr = dm
	}

	// Log events are queued without waiting for filesystem I/O on producer goroutines.
	var logMu sync.Mutex
	var logQueue []domain.LogLineProduced
	logsClosed := false
	logWake := make(chan struct{}, 1)
	stopLog := make(chan struct{})
	logDone := make(chan struct{})
	bus.Subscribe(func(event domain.Event) {
		if line, ok := event.(domain.LogLineProduced); ok {
			logMu.Lock()
			if logsClosed {
				logMu.Unlock()
				return
			}
			logQueue = append(logQueue, line)
			logMu.Unlock()
			select {
			case logWake <- struct{}{}:
			default:
			}
		}
	})
	go func() {
		defer close(logDone)
		writePending := func() bool {
			logMu.Lock()
			if len(logQueue) == 0 {
				logMu.Unlock()
				return false
			}
			line := logQueue[0]
			logQueue[0] = domain.LogLineProduced{}
			logQueue = logQueue[1:]
			logMu.Unlock()
			if _, err := os.Stat(workDir); err != nil {
				return true
			}
			timestamp := line.OccurredAt()
			if timestamp.IsZero() {
				timestamp = time.Now()
			}
			if err := os.MkdirAll(filepath.Join(workDir, ".vigiadev", "logs"), 0755); err != nil {
				return true
			}
			record := fmt.Sprintf("%s\t%s\t%s\t%s\n", timestamp.Format(time.RFC3339Nano), line.Service, map[bool]string{true: "stderr", false: "stdout"}[line.IsError], line.Line)
			for _, name := range []string{"all.log", line.Service + ".log"} {
				f, err := os.OpenFile(filepath.Join(workDir, ".vigiadev", "logs", name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
				if err == nil {
					_, _ = f.WriteString(record)
					_ = f.Close()
				}
			}
			return true
		}
		for {
			select {
			case <-logWake:
			case <-stopLog:
				for writePending() {
				}
				return
			}
			for {
				if !writePending() {
					break
				}
			}
		}
	}()
	closeLogs := func() {
		logMu.Lock()
		if !logsClosed {
			logsClosed = true
			close(stopLog)
		}
		logMu.Unlock()
		<-logDone
	}
	constructionComplete := false
	defer func() {
		if !constructionComplete {
			closeLogs()
		}
	}()

	orch := &Orchestrator{
		Config:     cfg,
		DAG:        dag,
		Bus:        bus,
		Ports:      ports.NewPortResolver("127.0.0.1"),
		Supervisor: process.NewSupervisor(bus),
		Health:     health.NewChecker(),
		Telemetry:  telemetry.NewCollector(bus, 1*time.Second),
		Docker:     dockerMgr,
		WorkDir:    workDir,
		RunID:      runID,
		closeLogs:  closeLogs,
		manifest: &manifest.RunManifest{
			RunID:       runID,
			ProjectName: cfg.ProjectName,
			StartTime:   time.Now(),
			Services:    make(map[string]manifest.ServiceManifest),
		},
	}

	// Inicializa File Watching se algum serviço habilitou watch
	var hasWatch bool
	for _, svc := range cfg.Services {
		if svc.Watch || len(svc.WatchPaths) > 0 {
			hasWatch = true
			break
		}
	}

	if hasWatch {
		w, err := watcher.NewServiceWatcher(workDir, func(serviceName string, changedPath string) {
			relPath, err := filepath.Rel(workDir, changedPath)
			if err != nil || relPath == "" {
				relPath = changedPath
			}

			orch.Bus.Publish(domain.LogLineProduced{
				BaseEvent: domain.NewBaseEvent(),
				Service:   serviceName,
				Line:      fmt.Sprintf("[vigiadev] File change detected in '%s' ➔ reloading...", relPath),
				IsError:   false,
			})

			_ = orch.RestartService(context.Background(), serviceName)
		})
		if err != nil {
			return nil, fmt.Errorf("falha ao inicializar observador de arquivos (fsnotify): %w", err)
		}

		for name, svc := range cfg.Services {
			if svc.Watch || len(svc.WatchPaths) > 0 {
				if err := w.WatchService(name, svc); err != nil {
					return nil, fmt.Errorf("falha ao registrar watch no serviço '%s': %w", name, err)
				}
			}
		}
		orch.Watcher = w
	}

	constructionComplete = true
	return orch, nil
}

func (o *Orchestrator) waitUntilHealthy(ctx context.Context, service string, cfg *domain.HealthCheckConfig) (time.Duration, error) {
	retries := cfg.Retries
	if retries <= 0 {
		retries = 30
	}
	interval := time.Duration(cfg.IntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	target := cfg.URL
	if target == "" && cfg.Port > 0 {
		host := cfg.Host
		if host == "" {
			host = "127.0.0.1"
		}
		target = net.JoinHostPort(host, fmt.Sprint(cfg.Port))
	}
	if target == "" {
		target = fmt.Sprint(cfg.Command)
	}
	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		start := time.Now()
		err := o.Health.CheckSingle(ctx, cfg, "127.0.0.1")
		latency := time.Since(start)
		probe := domain.HealthCheckProbed{BaseEvent: domain.NewBaseEvent(), Service: service, Type: cfg.Type, Target: target, Latency: latency, Success: err == nil}
		if err != nil {
			probe.Error = err.Error()
		}
		o.Bus.Publish(probe)
		if err == nil {
			return latency, nil
		}
		lastErr = err
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return latency, ctx.Err()
		case <-timer.C:
		}
	}
	return 0, fmt.Errorf("healthcheck esgotou %d tentativas sem sucesso: %w", retries, lastErr)
}

// SetDockerManager permite injetar um DockerManager customizado ou mockado para testes.
func (o *Orchestrator) SetDockerManager(dm *docker.DockerManager) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.Docker = dm
}

// Close stops owned background resources and waits for pending log writes.
func (o *Orchestrator) Close() {
	o.closeOnce.Do(func() {
		if o.Watcher != nil {
			_ = o.Watcher.Close()
		}
		if o.closeLogs != nil {
			o.closeLogs()
		}
	})
}

// Run executa as ondas do DAG, aguarda cancelamento e faz teardown gracioso.
func (o *Orchestrator) Run(ctx context.Context) error {
	defer o.Close()
	// 1. Garante trava exclusiva de execução
	if err := manifest.AcquireLock(o.WorkDir); err != nil {
		return err
	}
	defer func() {
		_ = manifest.ReleaseLock(o.WorkDir)
		_ = manifest.RemoveManifest(o.WorkDir)
	}()

	// Inicia amostragem de telemetria (CPU/RAM)
	o.Telemetry.Start(ctx, func() map[string]int {
		o.mu.Lock()
		defer o.mu.Unlock()
		res := make(map[string]int, len(o.manifest.Services))
		for name, svc := range o.manifest.Services {
			if !svc.IsContainer && svc.PID > 0 {
				res[name] = svc.PID
			}
		}
		return res
	})

	// 2. Executa onda por onda do DAG
	for waveIdx, wave := range o.DAG.Waves {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		errChan := make(chan error, len(wave))
		var wg sync.WaitGroup

		for _, serviceName := range wave {
			svcConfig := o.Config.Services[serviceName]
			wg.Add(1)

			go func(name string, svc domain.ServiceConfig) {
				defer wg.Done()
				if err := o.startService(ctx, name, svc); err != nil {
					errChan <- err
				}
			}(serviceName, svcConfig)
		}

		wg.Wait()
		close(errChan)

		// Se algum serviço da onda falhou, aborta e inicia teardown
		for err := range errChan {
			if err != nil {
				o.Supervisor.StopAll(2 * time.Second)
				if o.Docker != nil {
					_ = o.Docker.StopAll(context.Background())
				}
				return fmt.Errorf("falha ao iniciar onda %d: %w", waveIdx+1, err)
			}
		}

		// Persiste estado intermediário no manifesto
		o.mu.Lock()
		_ = manifest.WriteManifest(o.WorkDir, o.manifest)
		o.mu.Unlock()
	}

	// 3. Todos os serviços saudáveis! Aguarda sinal de encerramento
	<-ctx.Done()

	// 4. Teardown não-destrutivo estrito dos processos sob nossa custódia
	o.Supervisor.StopAll(3 * time.Second)
	if o.Docker != nil {
		_ = o.Docker.StopAll(context.Background())
	}
	return nil
}

// startService cuida da alocação de portas, inicialização e verificação de saúde de um serviço.
func (o *Orchestrator) startService(ctx context.Context, name string, svc domain.ServiceConfig) error {
	effectiveCmd := svc.Command
	effectiveEnv := svc.Env
	var assignedPort int

	// Resolução e remapeamento de portas
	if len(svc.Ports) > 0 {
		origPort := svc.Ports[0]
		assignedPort = origPort

		if !o.Ports.IsPortAvailable(origPort) {
			switch svc.PortPolicy {
			case domain.PortPolicyRemap:
				freePort, err := o.Ports.FindAvailablePort(origPort, 50)
				if err != nil {
					return err
				}
				assignedPort = freePort

				// Interpolação declarativa
				effectiveCmd = ports.InterpolateCommand(svc.Command, assignedPort)
				effectiveEnv = ports.InterpolateEnv(svc.Env, assignedPort)

				if svc.HealthCheck != nil {
					if svc.HealthCheck.Port == origPort {
						svc.HealthCheck.Port = assignedPort
					}
					if svc.HealthCheck.URL != "" {
						svc.HealthCheck.URL = ports.InterpolatePort(svc.HealthCheck.URL, assignedPort)
					}
				}

				// Notifica ouvintes
				o.Bus.Publish(domain.PortRemapped{
					BaseEvent:    domain.NewBaseEvent(),
					Service:      name,
					OriginalPort: origPort,
					TargetPort:   assignedPort,
				})

			case domain.PortPolicyFail:
				return &domain.PortConflictError{
					Service: name,
					Port:    origPort,
					Message: "porta já em uso e política configurada para 'fail'",
				}
			case domain.PortPolicyReuse:
				// Adota porta existente
			}
		}
	}

	// Injeção compulsória de TCP Readiness Probe se serviço declara portas sem healthcheck customizado (DEC-021)
	if svc.HealthCheck == nil && len(svc.Ports) > 0 {
		svc.HealthCheck = &domain.HealthCheckConfig{
			Type:       domain.HealthCheckTCP,
			Port:       assignedPort,
			TimeoutMs:  1000,
			IntervalMs: 250,
			Retries:    40,
		}
	}

	// Se for um serviço gerenciado via Docker Compose, delega ao DockerManager
	if svc.ComposeService != "" {
		return o.startComposeService(ctx, name, svc, assignedPort)
	}

	spawnTime := time.Now()

	// Inicia o processo no supervisor POSIX
	info, err := o.Supervisor.StartProcess(name, effectiveCmd, effectiveEnv, o.WorkDir)
	if err != nil {
		return err
	}

	// Registra no manifesto de sessão
	o.mu.Lock()
	o.manifest.Services[name] = manifest.ServiceManifest{
		Name: name,
		PID:  info.PID,
		PGID: info.PGID,
		Port: assignedPort,
	}
	o.mu.Unlock()

	var probeLatency time.Duration
	// Healthcheck de prontidão
	if svc.HealthCheck != nil {
		lat, err := o.waitUntilHealthy(ctx, name, svc.HealthCheck)
		if err != nil {
			o.Bus.Publish(domain.ServiceStateChanged{
				BaseEvent: domain.NewBaseEvent(),
				Service:   name,
				OldState:  domain.StateStarting,
				NewState:  domain.StateFailed,
				Detail:    fmt.Sprintf("healthcheck failed: %v", err),
			})
			o.Bus.Publish(domain.LogLineProduced{
				BaseEvent: domain.NewBaseEvent(),
				Service:   name,
				Line:      fmt.Sprintf("[vigiadev] ERROR: Healthcheck failed for '%s': %v", name, err),
				IsError:   true,
			})
			return fmt.Errorf("service '%s' failed healthcheck: %w", name, err)
		}
		probeLatency = lat
	}

	bootDuration := time.Since(spawnTime)
	detail := fmt.Sprintf("healthy in %dms (probe: %dms)", bootDuration.Milliseconds(), probeLatency.Milliseconds())

	o.Bus.Publish(domain.ServiceStateChanged{
		BaseEvent: domain.NewBaseEvent(),
		Service:   name,
		OldState:  domain.StateStarting,
		NewState:  domain.StateHealthy,
		Detail:    detail,
	})

	if svc.Watch || len(svc.WatchPaths) > 0 {
		debounceMs := svc.DebounceMs
		if debounceMs <= 0 {
			debounceMs = 300
		}
		watchDesc := "all files"
		if len(svc.WatchPaths) > 0 {
			watchDesc = fmt.Sprintf("%v", svc.WatchPaths)
		}
		o.Bus.Publish(domain.LogLineProduced{
			BaseEvent: domain.NewBaseEvent(),
			Service:   name,
			Line:      fmt.Sprintf("[vigiadev] File watch active: %s (debounce: %dms)", watchDesc, debounceMs),
			IsError:   false,
		})
	}

	return nil
}

// startComposeService coordena a inicialização, healthcheck e registro de container compose.
func (o *Orchestrator) startComposeService(ctx context.Context, name string, svc domain.ServiceConfig, assignedPort int) error {
	if o.Docker == nil {
		return fmt.Errorf("DockerManager not initialized for service '%s'", name)
	}

	spawnTime := time.Now()

	o.Bus.Publish(domain.ServiceStateChanged{
		BaseEvent: domain.NewBaseEvent(),
		Service:   name,
		OldState:  domain.StatePending,
		NewState:  domain.StateStarting,
		Detail:    "starting container via docker compose...",
	})

	composeFile := svc.ComposeFile
	if composeFile == "" && o.Config.ComposeFile != "" {
		composeFile = o.Config.ComposeFile
	}

	composeSvc := svc.ComposeService
	if composeSvc == "" {
		composeSvc = name
	}

	info, err := o.Docker.StartComposeService(ctx, name, composeSvc, composeFile, o.WorkDir)
	if err != nil {
		o.Bus.Publish(domain.ServiceStateChanged{
			BaseEvent: domain.NewBaseEvent(),
			Service:   name,
			OldState:  domain.StateStarting,
			NewState:  domain.StateFailed,
			Detail:    err.Error(),
		})
		o.Bus.Publish(domain.LogLineProduced{
			BaseEvent: domain.NewBaseEvent(),
			Service:   name,
			Line:      fmt.Sprintf("[vigiadev] ERROR: Failed to start compose service '%s': %v", name, err),
			IsError:   true,
		})
		return fmt.Errorf("failed to start compose service '%s': %w", name, err)
	}

	// Registra no manifesto de sessão
	o.mu.Lock()
	o.manifest.Services[name] = manifest.ServiceManifest{
		Name:        name,
		Port:        assignedPort,
		ContainerID: info.ID,
		IsContainer: true,
	}
	o.manifest.Containers = append(o.manifest.Containers, info.ID)
	o.mu.Unlock()

	var probeLatency time.Duration
	// Healthcheck de prontidão agnóstico (TCP, HTTP ou Command)
	if svc.HealthCheck != nil {
		lat, err := o.waitUntilHealthy(ctx, name, svc.HealthCheck)
		if err != nil {
			o.Bus.Publish(domain.ServiceStateChanged{
				BaseEvent: domain.NewBaseEvent(),
				Service:   name,
				OldState:  domain.StateStarting,
				NewState:  domain.StateFailed,
				Detail:    fmt.Sprintf("healthcheck failed: %v", err),
			})
			o.Bus.Publish(domain.LogLineProduced{
				BaseEvent: domain.NewBaseEvent(),
				Service:   name,
				Line:      fmt.Sprintf("[vigiadev] ERROR: Healthcheck failed for '%s': %v", name, err),
				IsError:   true,
			})
			return fmt.Errorf("service '%s' failed healthcheck: %w", name, err)
		}
		probeLatency = lat
	}

	bootDuration := time.Since(spawnTime)
	detail := fmt.Sprintf("healthy in %dms (container: %s, probe: %dms)", bootDuration.Milliseconds(), info.Name, probeLatency.Milliseconds())

	o.Bus.Publish(domain.ServiceStateChanged{
		BaseEvent: domain.NewBaseEvent(),
		Service:   name,
		OldState:  domain.StateStarting,
		NewState:  domain.StateHealthy,
		Detail:    detail,
	})

	if svc.Watch || len(svc.WatchPaths) > 0 {
		debounceMs := svc.DebounceMs
		if debounceMs <= 0 {
			debounceMs = 300
		}
		watchDesc := "all files"
		if len(svc.WatchPaths) > 0 {
			watchDesc = fmt.Sprintf("%v", svc.WatchPaths)
		}
		o.Bus.Publish(domain.LogLineProduced{
			BaseEvent: domain.NewBaseEvent(),
			Service:   name,
			Line:      fmt.Sprintf("[vigiadev] File watch active: %s (debounce: %dms)", watchDesc, debounceMs),
			IsError:   false,
		})
	}

	return nil
}

// RestartService reinicia sob demanda um único serviço sem interromper o restante do ambiente.
func (o *Orchestrator) RestartService(ctx context.Context, name string) error {
	svc, exists := o.Config.Services[name]
	if !exists {
		return fmt.Errorf("service '%s' not found", name)
	}

	o.Bus.Publish(domain.ServiceStateChanged{
		BaseEvent: domain.NewBaseEvent(),
		Service:   name,
		OldState:  domain.StateHealthy,
		NewState:  domain.StateStarting,
		Detail:    "restarting on demand...",
	})

	o.Bus.Publish(domain.LogLineProduced{
		BaseEvent: domain.NewBaseEvent(),
		Service:   name,
		Line:      fmt.Sprintf("[vigiadev] Restarting service '%s'...", name),
		IsError:   false,
	})

	// Caso compose: reinicia o container de forma isolada
	if svc.ComposeService != "" {
		if o.Docker == nil {
			return fmt.Errorf("DockerManager not initialized for '%s'", name)
		}
		composeFile := svc.ComposeFile
		if composeFile == "" && o.Config.ComposeFile != "" {
			composeFile = o.Config.ComposeFile
		}
		if err := o.Docker.RestartComposeService(ctx, name, svc.ComposeService, composeFile, o.WorkDir); err != nil {
			o.Bus.Publish(domain.ServiceStateChanged{
				BaseEvent: domain.NewBaseEvent(),
				Service:   name,
				OldState:  domain.StateStarting,
				NewState:  domain.StateFailed,
				Detail:    err.Error(),
			})
			return fmt.Errorf("failed to restart compose service '%s': %w", name, err)
		}
		if svc.HealthCheck != nil {
			if _, err := o.Health.WaitUntilHealthy(ctx, svc.HealthCheck, "127.0.0.1"); err != nil {
				o.Bus.Publish(domain.ServiceStateChanged{
					BaseEvent: domain.NewBaseEvent(),
					Service:   name,
					OldState:  domain.StateStarting,
					NewState:  domain.StateFailed,
					Detail:    fmt.Sprintf("healthcheck failed: %v", err),
				})
				return fmt.Errorf("service '%s' failed healthcheck after restart: %w", name, err)
			}
		}
		o.Bus.Publish(domain.ServiceStateChanged{
			BaseEvent: domain.NewBaseEvent(),
			Service:   name,
			OldState:  domain.StateStarting,
			NewState:  domain.StateHealthy,
			Detail:    "restarted and healthy",
		})
		return nil
	}

	// 1. Encerra o processo atual no supervisor
	_ = o.Supervisor.StopProcess(name, 2*time.Second)

	// 2. Inicia novamente com alocação e healthcheck
	if err := o.startService(ctx, name, svc); err != nil {
		return fmt.Errorf("falha ao reiniciar serviço '%s': %w", name, err)
	}

	// 3. Atualiza o manifesto de sessão
	o.mu.Lock()
	_ = manifest.WriteManifest(o.WorkDir, o.manifest)
	o.mu.Unlock()

	return nil
}

// EnsureServicesHealthy garante que todos os serviços informados (e suas dependências transitivas) estejam rodando e saudáveis.
func (o *Orchestrator) EnsureServicesHealthy(ctx context.Context, serviceNames []string) error {
	required := make(map[string]bool)
	var queue []string
	for _, name := range serviceNames {
		if _, exists := o.Config.Services[name]; exists {
			queue = append(queue, name)
			required[name] = true
		}
	}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if svc, ok := o.Config.Services[curr]; ok {
			for _, dep := range svc.DependsOn {
				if !required[dep] {
					required[dep] = true
					queue = append(queue, dep)
				}
			}
		}
	}

	for _, wave := range o.DAG.Waves {
		for _, svcName := range wave {
			if !required[svcName] {
				continue
			}
			svcConfig := o.Config.Services[svcName]

			if o.IsServiceHealthy(ctx, svcName, svcConfig) {
				o.Bus.Publish(domain.LogLineProduced{
					BaseEvent: domain.NewBaseEvent(),
					Service:   svcName,
					Line:      fmt.Sprintf("[vigiadev] Dependency '%s' is already active and healthy.", svcName),
					IsError:   false,
				})
				continue
			}

			o.Bus.Publish(domain.LogLineProduced{
				BaseEvent: domain.NewBaseEvent(),
				Service:   svcName,
				Line:      fmt.Sprintf("[vigiadev] Starting dependency '%s'...", svcName),
				IsError:   false,
			})

			if err := o.startService(ctx, svcName, svcConfig); err != nil {
				return fmt.Errorf("failed to start dependency '%s': %w", svcName, err)
			}
		}
	}

	// Persiste estado intermediário no manifesto (DEC-021: keep-alive)
	o.mu.Lock()
	_ = manifest.WriteManifest(o.WorkDir, o.manifest)
	o.mu.Unlock()

	return nil
}

// IsServiceHealthy verifica se um serviço já está respondendo ao healthcheck ou socket.
func (o *Orchestrator) IsServiceHealthy(ctx context.Context, name string, svc domain.ServiceConfig) bool {
	// Se tem healthcheck customizado, testa
	if svc.HealthCheck != nil {
		err := o.Health.CheckSingle(ctx, svc.HealthCheck, "127.0.0.1")
		if err == nil {
			return true
		}
	}

	// Se declara portas, testa conexão TCP na porta
	if len(svc.Ports) > 0 {
		port := svc.Ports[0]
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
	}

	// Se é container compose, checa com DockerManager se está rodando
	if svc.ComposeService != "" && o.Docker != nil {
		info, err := o.Docker.FindContainer(ctx, name, o.WorkDir)
		if err == nil && info != nil && info.State == "running" {
			return true
		}
	}

	return false
}

// RunTask executa uma tarefa declarada em tasks: com resolução de dependências e repasse de exit code.
func (o *Orchestrator) RunTask(ctx context.Context, taskName string, extraArgs []string, noDeps bool) (int, error) {
	task, exists := o.Config.Tasks[taskName]
	if !exists {
		return 1, fmt.Errorf("task '%s' not found in configuration", taskName)
	}

	if !noDeps && len(task.DependsOn) > 0 {
		if err := o.EnsureServicesHealthy(ctx, task.DependsOn); err != nil {
			return 1, fmt.Errorf("failed to satisfy dependencies for task '%s': %w", taskName, err)
		}
	}

	cmdParts := append([]string{}, task.Command...)
	cmdParts = append(cmdParts, extraArgs...)
	if len(cmdParts) == 0 {
		return 1, fmt.Errorf("task '%s' has an empty command", taskName)
	}

	env := os.Environ()
	for k, v := range task.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	execCmd := exec.CommandContext(ctx, cmdParts[0], cmdParts[1:]...)
	execCmd.Dir = o.WorkDir
	execCmd.Env = env
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr

	err := execCmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}

	return 0, nil
}
