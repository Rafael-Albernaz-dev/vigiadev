package application

import (
	"sync"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

// EventHandler é a função de callback executada quando um evento é publicado.
type EventHandler func(event domain.Event)

// EventBus gerencia a publicação e subscrição de eventos concorrentes no vigiaDev.
type EventBus struct {
	mu       sync.RWMutex
	handlers []EventHandler
}

// NewEventBus instancia um novo barramento de eventos.
func NewEventBus() *EventBus {
	return &EventBus{
		handlers: make([]EventHandler, 0),
	}
}

// Subscribe registra um novo ouvinte de eventos.
func (eb *EventBus) Subscribe(handler EventHandler) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.handlers = append(eb.handlers, handler)
}

// Publish dispara o evento para todos os ouvintes registrados de forma síncrona/segura.
func (eb *EventBus) Publish(event domain.Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for _, handler := range eb.handlers {
		handler(event)
	}
}
