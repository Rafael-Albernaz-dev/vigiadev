package process_test

import (
	"sync"
	"testing"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/process"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestSupervisor_StartAndCaptureLogs(t *testing.T) {
	bus := domain.NewEventBus()
	sup := process.NewSupervisor(bus)

	var logs []domain.LogLineProduced
	var mu sync.Mutex

	bus.Subscribe(func(event domain.Event) {
		if logEvt, ok := event.(domain.LogLineProduced); ok {
			mu.Lock()
			logs = append(logs, logEvt)
			mu.Unlock()
		}
	})

	cmd := []string{"sh", "-c", "echo 'linha normal'; echo 'linha erro' >&2"}
	info, err := sup.StartProcess("echo-worker", cmd, nil, "")
	if err != nil {
		t.Fatalf("erro ao iniciar processo: %v", err)
	}

	if info.PID <= 0 || info.PGID <= 0 {
		t.Fatalf("PID (%d) ou PGID (%d) inválidos", info.PID, info.PGID)
	}

	// Aguarda as linhas de log serem emitidas e lidas
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(logs)
		mu.Unlock()
		if count >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(logs) < 2 {
		t.Fatalf("esperava ao menos 2 linhas de log capturadas, obteve %d", len(logs))
	}

	hasStdout := false
	hasStderr := false
	for _, l := range logs {
		if l.Line == "linha normal" && !l.IsError {
			hasStdout = true
		}
		if l.Line == "linha erro" && l.IsError {
			hasStderr = true
		}
	}

	if !hasStdout {
		t.Error("esperava capturar linha de stdout 'linha normal'")
	}
	if !hasStderr {
		t.Error("esperava capturar linha de stderr 'linha erro'")
	}
}

func TestSupervisor_ProcessGroupTeardown(t *testing.T) {
	bus := domain.NewEventBus()
	sup := process.NewSupervisor(bus)

	// Inicia um processo em background de longa duração
	cmd := []string{"sh", "-c", "sleep 30"}
	info, err := sup.StartProcess("sleeper", cmd, nil, "")
	if err != nil {
		t.Fatalf("erro ao iniciar: %v", err)
	}

	// Encerra via SIGTERM no grupo
	start := time.Now()
	err = sup.StopProcess("sleeper", 500*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("erro ao parar processo: %v", err)
	}

	if elapsed > 1*time.Second {
		t.Fatalf("processo demorou demais para morrer: %v", elapsed)
	}

	if info.PGID <= 0 {
		t.Fatalf("esperava PGID positivo, obteve %d", info.PGID)
	}
}
