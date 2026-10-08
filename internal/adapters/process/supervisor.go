package process

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

// ManagedProcess armazena o estado de baixo nível do processo no SO.
type ManagedProcess struct {
	Name       string
	Cmd        *exec.Cmd
	PID        int
	PGID       int
	StartTime  time.Time
	Done       chan struct{}
	ExitCode   int
	ExitErr    error
	recentLogs []string
	logMu      sync.Mutex
}

// Supervisor gerencia o ciclo de vida dos processos POSIX com isolamento de grupos (PGID).
type Supervisor struct {
	publisher domain.EventPublisher
	processes map[string]*ManagedProcess
	mu        sync.Mutex
}

// NewSupervisor instancia o supervisor de processos conectado ao EventPublisher.
func NewSupervisor(publisher domain.EventPublisher) *Supervisor {
	return &Supervisor{
		publisher: publisher,
		processes: make(map[string]*ManagedProcess),
	}
}

// StartProcess inicia um comando POSIX com seu próprio grupo de processos (Setpgid).
func (s *Supervisor) StartProcess(name string, command []string, env map[string]string, workDir string) (*domain.ServiceRuntimeInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(command) == 0 {
		return nil, fmt.Errorf("comando vazio para o serviço '%s'", name)
	}

	cmd := exec.Command(command[0], command[1:]...)
	if workDir != "" {
		cmd.Dir = workDir
	}

	// 1. Herda o ambiente atual e injeta variáveis customizadas
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// 2. Isolamento estrito por grupo de processos (Setpgid)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// 3. Captura de pipes de saída
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("falha ao criar stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("falha ao criar stderr pipe: %w", err)
	}

	// 4. Executa o processo
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("falha ao iniciar processo: %w", err)
	}

	pid := cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		pgid = pid // fallback
	}

	proc := &ManagedProcess{
		Name:      name,
		Cmd:       cmd,
		PID:       pid,
		PGID:      pgid,
		StartTime: time.Now(),
		Done:      make(chan struct{}),
	}
	s.processes[name] = proc

	// 5. Goroutines concorrentes para streaming de logs
	var logWg sync.WaitGroup
	logWg.Add(2)

	go func() {
		defer logWg.Done()
		s.streamPipe(name, stdoutPipe, false)
	}()

	go func() {
		defer logWg.Done()
		s.streamPipe(name, stderrPipe, true)
	}()

	// 6. Goroutine de monitoramento de saída
	go func() {
		logWg.Wait() // Garante consumo integral dos buffers antes de chamar cmd.Wait()
		waitErr := cmd.Wait()
		s.mu.Lock()
		proc.ExitErr = waitErr
		if cmd.ProcessState != nil {
			proc.ExitCode = cmd.ProcessState.ExitCode()
		}
		close(proc.Done)
		s.mu.Unlock()

		newState := domain.StateStopped
		detail := "processo finalizado com sucesso"
		if waitErr != nil {
			newState = domain.StateFailed
			detail = waitErr.Error()
		}

		if s.publisher != nil {
			s.publisher.Publish(domain.ServiceStateChanged{
				BaseEvent: domain.NewBaseEvent(),
				Service:   name,
				OldState:  domain.StateStarting,
				NewState:  newState,
				Detail:    detail,
			})
		}
	}()

	if s.publisher != nil {
		s.publisher.Publish(domain.ServiceStateChanged{
			BaseEvent: domain.NewBaseEvent(),
			Service:   name,
			OldState:  domain.StatePending,
			NewState:  domain.StateStarting,
			Detail:    fmt.Sprintf("iniciado com PID %d e PGID %d", pid, pgid),
		})
	}

	return &domain.ServiceRuntimeInfo{
		Name:      name,
		State:     domain.StateStarting,
		PID:       pid,
		PGID:      pgid,
		StartTime: proc.StartTime,
	}, nil
}

// streamPipe lê continuamente as linhas emitidas no pipe e publica no EventBus.
func (s *Supervisor) streamPipe(service string, r io.Reader, isError bool) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		s.recordLog(service, line)
		if s.publisher != nil {
			s.publisher.Publish(domain.LogLineProduced{
				BaseEvent: domain.NewBaseEvent(),
				Service:   service,
				Line:      line,
				IsError:   isError,
			})
		}
	}
}

func (s *Supervisor) recordLog(service string, line string) {
	s.mu.Lock()
	proc, exists := s.processes[service]
	s.mu.Unlock()
	if !exists {
		return
	}
	proc.logMu.Lock()
	defer proc.logMu.Unlock()
	proc.recentLogs = append(proc.recentLogs, line)
	if len(proc.recentLogs) > 30 {
		proc.recentLogs = proc.recentLogs[len(proc.recentLogs)-30:]
	}
}

// ProcessStatus retorna se o processo está em execução, código de saída e últimas linhas de log.
func (s *Supervisor) ProcessStatus(name string) (isRunning bool, exitCode int, recentLogs []string) {
	s.mu.Lock()
	proc, exists := s.processes[name]
	s.mu.Unlock()
	if !exists {
		return false, 0, nil
	}
	proc.logMu.Lock()
	defer proc.logMu.Unlock()
	select {
	case <-proc.Done:
		return false, proc.ExitCode, append([]string(nil), proc.recentLogs...)
	default:
		return true, 0, append([]string(nil), proc.recentLogs...)
	}
}

// DiagnoseExit verifica se o processo encerrou prematuramente e identifica causas prováveis (ex: colisão de porta).
func (s *Supervisor) DiagnoseExit(name string) (exited bool, isPortConflict bool, detail string) {
	s.mu.Lock()
	_, exists := s.processes[name]
	s.mu.Unlock()
	if !exists {
		return false, false, ""
	}

	isRunning, exitCode, logs := s.ProcessStatus(name)
	if isRunning {
		return false, false, ""
	}

	for i := len(logs) - 1; i >= 0; i-- {
		line := logs[i]
		lower := strings.ToLower(line)
		if strings.Contains(line, "EADDRINUSE") ||
			strings.Contains(lower, "address already in use") ||
			strings.Contains(lower, "bind: address already in use") ||
			strings.Contains(lower, "port is already in use") ||
			strings.Contains(lower, "port is in use") ||
			strings.Contains(lower, "port already in use") {
			return true, true, fmt.Sprintf("port collision detected: %s", strings.TrimSpace(line))
		}
	}

	lastErr := ""
	for i := len(logs) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(logs[i])
		if trimmed != "" {
			lastErr = trimmed
			break
		}
	}

	if lastErr != "" {
		return true, false, fmt.Sprintf("exited with code %d: %s", exitCode, lastErr)
	}
	return true, false, fmt.Sprintf("exited with code %d", exitCode)
}

// StopProcess encerra graciosamente todo o grupo de processos com SIGTERM e fallback para SIGKILL.
func (s *Supervisor) StopProcess(name string, timeout time.Duration) error {
	s.mu.Lock()
	proc, exists := s.processes[name]
	s.mu.Unlock()

	if !exists {
		return nil
	}

	// Envia SIGTERM para a árvore inteira (-PGID)
	if err := syscall.Kill(-proc.PGID, syscall.SIGTERM); err != nil {
		// Se já morreu, apenas aguarda
		if err != syscall.ESRCH {
			_ = syscall.Kill(proc.PID, syscall.SIGTERM)
		}
	}

	select {
	case <-proc.Done:
		return nil
	case <-time.After(timeout):
		// Timeout estourado: força SIGKILL no grupo inteiro
		_ = syscall.Kill(-proc.PGID, syscall.SIGKILL)
		_ = syscall.Kill(proc.PID, syscall.SIGKILL)
		<-proc.Done
		return nil
	}
}

// StopAll encerra todos os processos ativos em paralelo.
func (s *Supervisor) StopAll(timeout time.Duration) {
	s.mu.Lock()
	names := make([]string, 0, len(s.processes))
	for name := range s.processes {
		names = append(names, name)
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(svc string) {
			defer wg.Done()
			_ = s.StopProcess(svc, timeout)
		}(name)
	}
	wg.Wait()
}
