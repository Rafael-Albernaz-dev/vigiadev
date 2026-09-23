package application

import (
	"context"
	"fmt"
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

	return orch, nil
}

// SetDockerManager permite injetar um DockerManager customizado ou mockado para testes.
func (o *Orchestrator) SetDockerManager(dm *docker.DockerManager) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.Docker = dm
}

// Run executa as ondas do DAG, aguarda cancelamento e faz teardown gracioso.
func (o *Orchestrator) Run(ctx context.Context) error {
	// 1. Garante trava exclusiva de execução
	if err := manifest.AcquireLock(o.WorkDir); err != nil {
		return err
	}
	defer func() {
		if o.Watcher != nil {
			_ = o.Watcher.Close()
		}
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
		lat, err := o.Health.WaitUntilHealthy(ctx, svc.HealthCheck, "127.0.0.1")
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
		lat, err := o.Health.WaitUntilHealthy(ctx, svc.HealthCheck, "127.0.0.1")
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
