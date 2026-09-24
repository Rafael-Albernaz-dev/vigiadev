package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

type rawConfig struct {
	Version     int                             `yaml:"version"`
	ProjectName string                          `yaml:"project_name"`
	ComposeFile string                          `yaml:"compose_file,omitempty"`
	Services    map[string]domain.ServiceConfig `yaml:"services,omitempty"`
	Tasks       map[string]rawTaskConfig        `yaml:"tasks,omitempty"`
}

type rawTaskConfig struct {
	Command   interface{}       `yaml:"command"`
	DependsOn []string          `yaml:"depends_on,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
}

// LoadConfig carrega, faz o parse de YAML e valida as regras de negócio de um arquivo vigiadev.yaml.
func LoadConfig(filePath string) (*domain.VigiaConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("falha ao ler arquivo de configuração: %w", err)
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: erro de sintaxe YAML: %s", ErrInvalidConfig, err.Error())
	}

	cfg := domain.VigiaConfig{
		Version:     raw.Version,
		ProjectName: raw.ProjectName,
		ComposeFile: raw.ComposeFile,
		Services:    raw.Services,
		Tasks:       make(map[string]domain.TaskConfig),
	}

	for name, rt := range raw.Tasks {
		var cmdParts []string
		switch v := rt.Command.(type) {
		case string:
			cmdParts = strings.Fields(v)
		case []interface{}:
			for _, item := range v {
				cmdParts = append(cmdParts, fmt.Sprint(item))
			}
		case []string:
			cmdParts = v
		}

		cfg.Tasks[name] = domain.TaskConfig{
			Command:   cmdParts,
			DependsOn: rt.DependsOn,
			Env:       rt.Env,
		}
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

	for name, task := range cfg.Tasks {
		if len(task.Command) == 0 {
			return fmt.Errorf("%w: task '%s' must declare a non-empty 'command'", ErrInvalidConfig, name)
		}
		for _, dep := range task.DependsOn {
			if _, exists := cfg.Services[dep]; !exists {
				return fmt.Errorf("%w: task '%s' depends on undefined service '%s'", ErrInvalidConfig, name, dep)
			}
		}
	}

	return nil
}

// SaveDiscoveredTasks merges only new tasks and atomically replaces the selected
// config. Bytes outside the tasks section are retained, including service comments.
func SaveDiscoveredTasks(path string, tasks map[string]domain.TaskConfig) error {
	if len(tasks) == 0 {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	path = resolved
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(original))
	if err := decoder.Decode(&doc); err != nil {
		return err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("task persistence requires a single YAML document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode || doc.Content[0].Style&yaml.FlowStyle != 0 {
		return fmt.Errorf("task persistence requires a block mapping at the YAML root")
	}
	root := doc.Content[0]
	lines := strings.SplitAfter(string(original), "\n")
	start, end := len(lines), len(lines)
	// Keep an explicit YAML document terminator after the new section.
	for i, line := range lines {
		if strings.TrimSpace(line) == "..." || strings.HasPrefix(line, "... #") {
			start, end = i, i
			break
		}
	}
	key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "tasks"}
	value := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value != "tasks" {
			continue
		}
		key, value = root.Content[i], root.Content[i+1]
		start = key.Line - 1
		if i+2 < len(root.Content) {
			end = root.Content[i+2].Line - 1
		}
		// Trailing comments and whitespace belong to the untouched suffix.
		for end > start+1 {
			line := strings.TrimSpace(lines[end-1])
			if line != "" && !strings.HasPrefix(lines[end-1], "#") {
				break
			}
			end--
		}
		key.HeadComment = "" // already present in the untouched prefix
		key.FootComment = "" // retained in the untouched suffix
		if value.Tag == "!!null" {
			if value.LineComment != "" {
				key.LineComment = value.LineComment
				value.LineComment = ""
			}
			value.Kind, value.Tag, value.Value = yaml.MappingNode, "!!map", ""
		}
		if value.Kind != yaml.MappingNode {
			return fmt.Errorf("tasks must be a mapping")
		}
		break
	}
	names := make([]string, 0, len(tasks))
	for name := range tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	added := false
	for _, name := range names {
		exists := false
		for i := 0; i < len(value.Content); i += 2 {
			if value.Content[i].Value == name {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		var task yaml.Node
		if err := task.Encode(tasks[name]); err != nil {
			return err
		}
		value.Content = append(value.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, &task)
		added = true
	}
	if !added {
		return nil
	}
	section := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{key, value}}
	var encoded bytes.Buffer
	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(2)
	if err := encoder.Encode(section); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	prefix := strings.Join(lines[:start], "")
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	updated := []byte(prefix + encoded.String() + strings.Join(lines[end:], ""))
	temp, err := os.CreateTemp(filepath.Dir(path), ".vigiadev-tasks-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if _, err := temp.Write(updated); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if _, err := LoadConfig(temp.Name()); err != nil {
		return fmt.Errorf("validate task update: %w", err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original) {
		return fmt.Errorf("configuration changed during task discovery; retry")
	}
	return os.Rename(temp.Name(), path)
}
