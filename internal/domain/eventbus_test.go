package domain_test

import (
	"sync"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestEventBus_PublishSubscribe(t *testing.T) {
	bus := domain.NewEventBus()

	var received []domain.Event
	var mu sync.Mutex

	bus.Subscribe(func(event domain.Event) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, event)
	})

	evt := domain.PortRemapped{
		BaseEvent:    domain.NewBaseEvent(),
		Service:      "web",
		OriginalPort: 3000,
		TargetPort:   3001,
	}

	bus.Publish(evt)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("esperava 1 evento recebido, obteve %d", len(received))
	}

	if remapped, ok := received[0].(domain.PortRemapped); !ok {
		t.Fatalf("esperava tipo PortRemapped, obteve %T", received[0])
	} else if remapped.TargetPort != 3001 {
		t.Errorf("esperava porta 3001, obteve %d", remapped.TargetPort)
	}
}
