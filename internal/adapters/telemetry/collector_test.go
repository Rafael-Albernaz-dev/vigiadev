package telemetry_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/telemetry"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestReadProcessMemory_CurrentProcess(t *testing.T) {
	pid := os.Getpid()

	mem, err := telemetry.ReadProcessMemory(pid)
	if err != nil {
		t.Fatalf("falha ao ler memória do processo atual: %v", err)
	}

	if mem == 0 {
		t.Fatal("esperava uso de memória maior que 0")
	}
}

func TestReadProcessCPUTicks_CurrentProcess(t *testing.T) {
	pid := os.Getpid()

	ticks, err := telemetry.ReadProcessCPUTicks(pid)
	if err != nil {
		t.Fatalf("falha ao ler ticks de CPU: %v", err)
	}

	if ticks == 0 {
		// Ticks podem ser baixos mas não devem estourar erro
		t.Logf("ticks retornados: %d", ticks)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input uint64
		want  string
	}{
		{500, "500 B"},
		{2048, "2 KB"},
		{52428800, "50.0 MB"},
		{2147483648, "2.00 GB"},
	}

	for _, tc := range tests {
		got := telemetry.FormatBytes(tc.input)
		if got != tc.want {
			t.Errorf("FormatBytes(%d) = %s; want %s", tc.input, got, tc.want)
		}
	}
}

func TestCollector_SampleAndPublish(t *testing.T) {
	bus := domain.NewEventBus()
	collector := telemetry.NewCollector(bus, 50*time.Millisecond)

	var received []domain.TelemetryUpdated
	var mu sync.Mutex

	bus.Subscribe(func(event domain.Event) {
		if telem, ok := event.(domain.TelemetryUpdated); ok {
			mu.Lock()
			received = append(received, telem)
			mu.Unlock()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	collector.Start(ctx, func() map[string]int {
		return map[string]int{
			"self": os.Getpid(),
		}
	})

	<-ctx.Done()

	mu.Lock()
	defer mu.Unlock()

	if len(received) == 0 {
		t.Fatal("esperava ao menos 1 evento de telemetria recebido")
	}

	last := received[len(received)-1]
	if last.Service != "self" || last.MemoryBytes == 0 {
		t.Fatalf("dados inesperados no evento de telemetria: %+v", last)
	}
}
