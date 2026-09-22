package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"gopkg.in/yaml.v3"
)

var (
	ErrConfigNotFound = errors.New("nenhum arquivo de configuração encontrado")
	ErrInvalidConfig  = errors.New("arquivo de configuração inválido")
)

// PrecedenceFiles define a ordem canônica de busca segundo a DEC-002.
var PrecedenceFiles = []string{
	"vigiadev.local.yaml",
	"vigiadev.yaml",
	"dev.yaml",
}

// FindConfigFile busca o arquivo de configuração no diretório informado seguindo a ordem de precedência.
// Se explicitPath for fornecido via flag (--config), ele terá prioridade absoluta.
func FindConfigFile(baseDir string, explicitPath string) (string, error) {
	if explicitPath != "" {
		resolved := explicitPath
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(baseDir, explicitPath)
		}
		if _, err := os.Stat(resolved); err != nil {
			return "", fmt.Errorf("%w: arquivo explicitamente passado não existe: %s", ErrConfigNotFound, explicitPath)
		}
		return resolved, nil
	}

	for _, name := range PrecedenceFiles {
		candidate := filepath.Join(baseDir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", ErrConfigNotFound
}

// LoadConfig carrega, faz o parse de YAML e valida as regras de negócio de um arquivo vigiadev.yaml.
func LoadConfig(filePath string) (*domain.VigiaConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("falha ao ler arquivo de configuração: %w", err)
	}

	var cfg domain.VigiaConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%w: erro de sintaxe YAML: %s", ErrInvalidConfig, err.Error())
	}

	if err := ValidateAndApplyDefaults(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ValidateAndApplyDefaults valida regras estruturais e preenche defaults saudáveis.
func ValidateAndApplyDefaults(cfg *domain.VigiaConfig) error {
	if cfg.Version != 1 {
		return fmt.Errorf("%w: versão suportada é 1 (encontrado: %d)", ErrInvalidConfig, cfg.Version)
	}
	if cfg.ProjectName == "" {
		return fmt.Errorf("%w: 'project_name' é obrigatório", ErrInvalidConfig)
	}

	for name, svc := range cfg.Services {
		if len(svc.Command) == 0 && svc.ComposeService == "" {
			return fmt.Errorf("%w: serviço '%s' deve declarar 'command' ou 'compose_service'", ErrInvalidConfig, name)
		}

		// Defaults de política de porta
		if svc.PortPolicy == "" {
			svc.PortPolicy = domain.PortPolicyReuse
		}

		// Validação de portas
		for _, p := range svc.Ports {
			if p <= 0 || p > 65535 {
				return fmt.Errorf("%w: serviço '%s' possui porta inválida: %d", ErrInvalidConfig, name, p)
			}
		}

		// Defaults de HealthCheck
		if svc.HealthCheck != nil {
			if svc.HealthCheck.Timeout > 0 && svc.HealthCheck.TimeoutMs <= 0 {
				svc.HealthCheck.TimeoutMs = int(svc.HealthCheck.Timeout * 1000)
			}
			if svc.HealthCheck.Interval > 0 && svc.HealthCheck.IntervalMs <= 0 {
				svc.HealthCheck.IntervalMs = int(svc.HealthCheck.Interval * 1000)
			}
			if svc.HealthCheck.IntervalMs <= 0 {
				svc.HealthCheck.IntervalMs = 1000
			}
			if svc.HealthCheck.TimeoutMs <= 0 {
				svc.HealthCheck.TimeoutMs = 2000
			}
			if svc.HealthCheck.Retries <= 0 {
				svc.HealthCheck.Retries = 30
			}

			switch svc.HealthCheck.Type {
			case domain.HealthCheckHTTP:
				if svc.HealthCheck.URL == "" {
					return fmt.Errorf("%w: healthcheck HTTP do serviço '%s' exige 'url'", ErrInvalidConfig, name)
				}
				if svc.HealthCheck.ExpectedStatus <= 0 {
					svc.HealthCheck.ExpectedStatus = 200
				}
			case domain.HealthCheckTCP:
				if svc.HealthCheck.Port <= 0 && len(svc.Ports) > 0 {
					svc.HealthCheck.Port = svc.Ports[0]
				}
				if svc.HealthCheck.Port <= 0 {
					return fmt.Errorf("%w: healthcheck TCP do serviço '%s' exige uma porta válida", ErrInvalidConfig, name)
				}
			case domain.HealthCheckCommand:
				if len(svc.HealthCheck.Command) == 0 {
					return fmt.Errorf("%w: healthcheck command do serviço '%s' exige 'command'", ErrInvalidConfig, name)
				}
			default:
				return fmt.Errorf("%w: tipo de healthcheck desconhecido '%s' no serviço '%s'", ErrInvalidConfig, svc.HealthCheck.Type, name)
			}
		}

		cfg.Services[name] = svc
	}

	return nil
}
