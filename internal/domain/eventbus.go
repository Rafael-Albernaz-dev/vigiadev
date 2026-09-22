package domain

import "sync"

// EventHandler é a função de callback executada quando um evento é publicado.
type EventHandler func(event Event)

// EventBus gerencia a publicação e subscrição de eventos concorrentes no vigiaDev.
type EventBus struct {
	mu       sync.RWMutex
	handlers []EventHandler
}

// NewEventBus instancia um novo barramento de eventos thread-safe.
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

// Publish dispara o evento para todos os ouvintes registrados de forma thread-safe.
func (eb *EventBus) Publish(event Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for _, handler := range eb.handlers {
		handler(event)
	}
}
