package domain

import "time"

// Event é a interface base para qualquer evento de domínio no vigiaDev.
type Event interface {
	EventName() string
	OccurredAt() time.Time
}

// EventPublisher é a porta de saída para publicação de eventos.
type EventPublisher interface {
	Publish(event Event)
}

// BaseEvent fornece campos comuns a todos os eventos.
type BaseEvent struct {
	Timestamp time.Time
}

func (b BaseEvent) OccurredAt() time.Time {
	return b.Timestamp
}

// ServiceStateChanged é publicado sempre que um serviço transita na máquina de estados.
type ServiceStateChanged struct {
	BaseEvent
	Service  string
	OldState ServiceState
	NewState ServiceState
	Detail   string
}

func (e ServiceStateChanged) EventName() string { return "service.state_changed" }

// PortRemapped é publicado quando ocorre colisão e a porta é remapeada com sucesso.
type PortRemapped struct {
	BaseEvent
	Service      string
	OriginalPort int
	TargetPort   int
}

func (e PortRemapped) EventName() string { return "port.remapped" }

// LogLineProduced transporta uma linha de log capturada de stdout ou stderr.
type LogLineProduced struct {
	BaseEvent
	Service string
	Line    string
	IsError bool
}

func (e LogLineProduced) EventName() string { return "log.line_produced" }

// TelemetryUpdated transporta métricas de consumo de CPU, RAM, Disco e latência.
type TelemetryUpdated struct {
	BaseEvent
	Service        string
	CPUPercent     float64
	MemoryBytes    uint64
	DiskReadBytes  uint64
	DiskWriteBytes uint64
	ResponseTime   time.Duration
}

func (e TelemetryUpdated) EventName() string { return "telemetry.updated" }

// Helper para criar eventos com timestamp atual
func NewBaseEvent() BaseEvent {
	return BaseEvent{Timestamp: time.Now()}
}
