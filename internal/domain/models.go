package domain

import "time"

// PortPolicy define a estratégia adotada quando uma porta já está em uso no host.
type PortPolicy string

const (
	PortPolicyReuse  PortPolicy = "reuse"
	PortPolicyRemap  PortPolicy = "remap"
	PortPolicyPrompt PortPolicy = "prompt"
	PortPolicyFail   PortPolicy = "fail"
	PortPolicyKill   PortPolicy = "kill"
)

// HealthCheckType define o mecanismo de verificação de prontidão.
type HealthCheckType string

const (
	HealthCheckCommand HealthCheckType = "command"
	HealthCheckHTTP    HealthCheckType = "http"
	HealthCheckTCP     HealthCheckType = "tcp"
)

// HealthCheckConfig declara os parâmetros de healthcheck do serviço.
type HealthCheckConfig struct {
	Type           HealthCheckType `yaml:"type"`
	URL            string          `yaml:"url,omitempty"`
	Host           string          `yaml:"host,omitempty"`
	Port           int             `yaml:"port,omitempty"`
	Command        []string        `yaml:"command,omitempty"`
	ExpectedStatus int             `yaml:"expected_status,omitempty"`
	Timeout        float64         `yaml:"timeout,omitempty"`
	Interval       float64         `yaml:"interval,omitempty"`
	IntervalMs     int             `yaml:"interval_ms,omitempty"`
	TimeoutMs      int             `yaml:"timeout_ms,omitempty"`
	Retries        int             `yaml:"retries,omitempty"`
}

// ServiceConfig representa a declaração de um serviço no vigiaDev.
type ServiceConfig struct {
	Command        []string          `yaml:"command,omitempty"`
	Ports          []int             `yaml:"ports,omitempty"`
	PortPolicy     PortPolicy        `yaml:"port_policy,omitempty"`
	DependsOn      []string          `yaml:"depends_on,omitempty"`
	Env            map[string]string `yaml:"env,omitempty"`
	HealthCheck    *HealthCheckConfig `yaml:"healthcheck,omitempty"`
	ComposeService string            `yaml:"compose_service,omitempty"`
}

// TaskConfig representa uma tarefa one-off no DAG (ex: migrations, seeds).
type TaskConfig struct {
	Command   []string          `yaml:"command"`
	DependsOn []string          `yaml:"depends_on,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
}

// VigiaConfig é a estrutura raiz do arquivo vigiadev.yaml.
type VigiaConfig struct {
	Version     int                      `yaml:"version"`
	ProjectName string                   `yaml:"project_name"`
	Services    map[string]ServiceConfig `yaml:"services,omitempty"`
	Tasks       map[string]TaskConfig    `yaml:"tasks,omitempty"`
}

// ServiceState representa a máquina de estados pura de cada serviço.
type ServiceState string

const (
	StatePending   ServiceState = "pending"
	StateStarting  ServiceState = "starting"
	StateHealthy   ServiceState = "healthy"
	StateUnhealthy ServiceState = "unhealthy"
	StateFailed    ServiceState = "failed"
	StateStopped   ServiceState = "stopped"
)

// ServiceRuntimeInfo armazena os dados de execução e telemetria de um serviço ativo.
type ServiceRuntimeInfo struct {
	Name         string
	State        ServiceState
	PID          int
	PGID         int
	AssignedPort int
	OriginalPort int
	IsRemapped   bool
	StartTime    time.Time
	Latency      time.Duration
	BootDuration time.Duration
	CPUPercent   float64
	MemoryBytes  uint64
}
