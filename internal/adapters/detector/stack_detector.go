package detector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/docker"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"gopkg.in/yaml.v3"
)

// StackDetector inspeciona o diretório raiz e sintetiza uma configuração do vigiaDev.
type StackDetector struct {
	RootDir string
}

// NewStackDetector cria um novo detector heurístico apontando para o diretório alvo.
func NewStackDetector(rootDir string) *StackDetector {
	if rootDir == "" {
		rootDir = "."
	}
	return &StackDetector{RootDir: rootDir}
}

// DetectionResult contém a configuração gerada e a lista de marcadores encontrados.
type DetectionResult struct {
	Config  *domain.VigiaConfig
	Markers []string
}

// Detect executa as heurísticas de Docker Compose, Node.js e Python.
func (sd *StackDetector) Detect() (*DetectionResult, error) {
	projectName := filepath.Base(sd.RootDir)
	if projectName == "." || projectName == "/" || projectName == "" {
		projectName = "app"
	}

	cfg := &domain.VigiaConfig{
		Version:     1,
		ProjectName: projectName,
		Services:    make(map[string]domain.ServiceConfig),
		Tasks:       make(map[string]domain.TaskConfig),
	}

	var markers []string

	// 1. Heurística Docker Compose
	composeServices, composeMarker := sd.detectDockerCompose()
	if composeMarker != "" {
		markers = append(markers, composeMarker)
		for name, svc := range composeServices {
			cfg.Services[name] = svc
		}
	}

	// 2. Heurística Node.js
	nodeSvc, nodeMarker := sd.detectNodeJS()
	if nodeMarker != "" {
		markers = append(markers, nodeMarker)
		cfg.Services["frontend"] = nodeSvc
	}

	// 3. Heurística Python
	pythonSvc, pythonMarker := sd.detectPython()
	if pythonMarker != "" {
		markers = append(markers, pythonMarker)
		svcName := "backend"
		if _, exists := cfg.Services["frontend"]; !exists {
			svcName = "app"
		}
		cfg.Services[svcName] = pythonSvc
	}

	return &DetectionResult{
		Config:  cfg,
		Markers: markers,
	}, nil
}

// detectDockerCompose procura por compose.yaml ou docker-compose.yml (inclusive com sufixos).
func (sd *StackDetector) detectDockerCompose() (map[string]domain.ServiceConfig, string) {
	composeFile := docker.FindComposeFile(sd.RootDir)
	if composeFile == "" {
		return nil, ""
	}

	foundPath := filepath.Join(sd.RootDir, composeFile)
	data, err := os.ReadFile(foundPath)
	if err != nil {
		return nil, ""
	}

	var composeDoc struct {
		Services map[string]struct {
			Image string        `yaml:"image"`
			Ports []interface{} `yaml:"ports"`
		} `yaml:"services"`
	}

	if err := yaml.Unmarshal(data, &composeDoc); err != nil || len(composeDoc.Services) == 0 {
		return nil, filepath.Base(foundPath)
	}

	services := make(map[string]domain.ServiceConfig)
	for name, svc := range composeDoc.Services {
		var port int
		lowerName := strings.ToLower(name) + " " + strings.ToLower(svc.Image)

		switch {
		case strings.Contains(lowerName, "postgres") || strings.Contains(lowerName, "pgsql"):
			port = 5432
		case strings.Contains(lowerName, "redis"):
			port = 6379
		case strings.Contains(lowerName, "mysql") || strings.Contains(lowerName, "mariadb"):
			port = 3306
		case strings.Contains(lowerName, "mongo"):
			port = 27017
		case strings.Contains(lowerName, "rabbit"):
			port = 5672
		}

		serviceCfg := domain.ServiceConfig{
			ComposeService: name,
			PortPolicy:     domain.PortPolicyReuse,
		}

		if port > 0 {
			serviceCfg.Ports = []int{port}
			serviceCfg.HealthCheck = &domain.HealthCheckConfig{
				Type: domain.HealthCheckTCP,
				Port: port,
			}
		}

		services[name] = serviceCfg
	}

	return services, fmt.Sprintf("Docker Compose (%s)", filepath.Base(foundPath))
}

// detectNodeJS inspeciona package.json e lockfiles.
func (sd *StackDetector) detectNodeJS() (domain.ServiceConfig, string) {
	pkgPath := filepath.Join(sd.RootDir, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return domain.ServiceConfig{}, ""
	}

	var pkg struct {
		Scripts      map[string]string `json:"scripts"`
		Dependencies map[string]string `json:"dependencies"`
		DevDeps      map[string]string `json:"devDependencies"`
	}
	_ = json.Unmarshal(data, &pkg)

	// Detecta gerenciador de pacotes por lockfile
	pkgManager := "npm"
	switch {
	case fileExists(filepath.Join(sd.RootDir, "pnpm-lock.yaml")):
		pkgManager = "pnpm"
	case fileExists(filepath.Join(sd.RootDir, "yarn.lock")):
		pkgManager = "yarn"
	case fileExists(filepath.Join(sd.RootDir, "bun.lockb")) || fileExists(filepath.Join(sd.RootDir, "bun.lock")):
		pkgManager = "bun"
	}

	// Identifica script de inicialização
	script := "dev"
	if _, ok := pkg.Scripts["dev"]; !ok {
		if _, ok := pkg.Scripts["start"]; ok {
			script = "start"
		}
	}

	// Infere porta padrão baseada em framework
	port := 3000
	depsStr := fmt.Sprintf("%v %v", pkg.Dependencies, pkg.DevDeps)
	if strings.Contains(depsStr, "vite") {
		port = 5173
	} else if strings.Contains(depsStr, "next") || strings.Contains(depsStr, "nuxt") {
		port = 3000
	}

	cmd := []string{pkgManager, "run", script}
	if pkgManager == "npm" && script == "start" {
		cmd = []string{"npm", "start"}
	}

	svc := domain.ServiceConfig{
		Command:    cmd,
		Ports:      []int{port},
		PortPolicy: domain.PortPolicyRemap,
		HealthCheck: &domain.HealthCheckConfig{
			Type: domain.HealthCheckTCP,
			Port: port,
		},
	}

	return svc, fmt.Sprintf("Node.js (%s run %s)", pkgManager, script)
}

// detectPython inspeciona manage.py (Django) ou FastAPI/uvicorn.
func (sd *StackDetector) detectPython() (domain.ServiceConfig, string) {
	// 1. Django
	if fileExists(filepath.Join(sd.RootDir, "manage.py")) {
		return domain.ServiceConfig{
			Command:    []string{"python", "manage.py", "runserver", "0.0.0.0:{port}"},
			Ports:      []int{8000},
			PortPolicy: domain.PortPolicyRemap,
			HealthCheck: &domain.HealthCheckConfig{
				Type: domain.HealthCheckHTTP,
				URL:  "http://127.0.0.1:{port}/",
			},
		}, "Python (Django manage.py)"
	}

	// 2. FastAPI / Uvicorn
	reqFiles := []string{"requirements.txt", "pyproject.toml"}
	for _, req := range reqFiles {
		p := filepath.Join(sd.RootDir, req)
		if data, err := os.ReadFile(p); err == nil {
			content := strings.ToLower(string(data))
			if strings.Contains(content, "fastapi") || strings.Contains(content, "uvicorn") {
				return domain.ServiceConfig{
					Command:    []string{"uvicorn", "main:app", "--port", "{port}", "--reload"},
					Ports:      []int{8000},
					PortPolicy: domain.PortPolicyRemap,
					HealthCheck: &domain.HealthCheckConfig{
						Type: domain.HealthCheckHTTP,
						URL:  "http://127.0.0.1:{port}/docs",
					},
				}, fmt.Sprintf("Python (FastAPI via %s)", req)
			}
		}
	}

	return domain.ServiceConfig{}, ""
}

// GenerateYAML formata a configuração gerada em um YAML documentado e canônico.
func GenerateYAML(cfg *domain.VigiaConfig, markers []string) (string, error) {
	var sb strings.Builder

	sb.WriteString("# vigiaDev configuration (version 1)\n")
	if len(markers) > 0 {
		sb.WriteString(fmt.Sprintf("# Auto-detectado a partir de: %s\n", strings.Join(markers, ", ")))
	} else {
		sb.WriteString("# Template canônico do vigiaDev\n")
	}
	sb.WriteString("# {port} é interpolado automaticamente caso ocorra colisão (port_policy: remap)\n\n")

	yamlBytes, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}

	sb.Write(yamlBytes)
	return sb.String(), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
