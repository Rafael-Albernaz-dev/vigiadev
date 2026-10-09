package detector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	usedPorts := make(map[int]bool)

	// 1. Heurística Docker Compose
	composeServices, composeMarker := sd.detectDockerCompose()
	if composeMarker != "" {
		markers = append(markers, composeMarker)
		for name, svc := range composeServices {
			cfg.Services[name] = svc
			for _, p := range svc.Ports {
				usedPorts[p] = true
			}
		}
	}

	// 2. Heurística Node.js (Workspaces, Monorepos e Single Projects)
	nodeResult := sd.detectNodeServices(usedPorts)
	for name, svc := range nodeResult.services {
		cfg.Services[name] = svc
	}
	markers = append(markers, nodeResult.markers...)

	// 3. Heurística Python
	pythonSvc, pythonMarker := sd.detectPython()
	if pythonMarker != "" {
		markers = append(markers, pythonMarker)
		svcName := "backend"
		if len(cfg.Services) == 0 {
			svcName = "app"
		} else if _, exists := cfg.Services["frontend"]; !exists {
			if _, existsApp := cfg.Services["app"]; !existsApp && len(cfg.Services) == len(composeServices) {
				svcName = "app"
			}
		}
		cfg.Services[svcName] = pythonSvc
	}

	tasks, err := sd.DetectTasks()
	if err != nil {
		return nil, err
	}
	cfg.Tasks = tasks

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
		var portsList []int
		for _, rawPort := range svc.Ports {
			if hp := parseComposeHostPort(rawPort); hp > 0 {
				portsList = append(portsList, hp)
			}
		}

		if len(portsList) == 0 {
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
			if port > 0 {
				portsList = []int{port}
			}
		}

		serviceCfg := domain.ServiceConfig{
			ComposeService: name,
			PortPolicy:     domain.PortPolicyReuse,
		}

		if len(portsList) > 0 {
			serviceCfg.Ports = portsList
			serviceCfg.HealthCheck = &domain.HealthCheckConfig{
				Type: domain.HealthCheckTCP,
				Port: portsList[0],
			}
		}

		services[name] = serviceCfg
	}

	return services, fmt.Sprintf("Docker Compose (%s)", filepath.Base(foundPath))
}

type nodeDetection struct {
	services map[string]domain.ServiceConfig
	markers  []string
}

func (sd *StackDetector) detectNodeServices(usedPorts map[int]bool) nodeDetection {
	result := nodeDetection{
		services: make(map[string]domain.ServiceConfig),
	}

	allocPort := func(preferred int) int {
		p := preferred
		for usedPorts[p] {
			p++
		}
		usedPorts[p] = true
		return p
	}

	// 1. Inspeciona se há monorepo profundo com apps modulares (NestJS apps, workspaces)
	nested := sd.detectNestedMonorepoApps(usedPorts, allocPort)
	for k, v := range nested.services {
		result.services[k] = v
	}
	result.markers = append(result.markers, nested.markers...)

	// 2. Inspeciona package.json raiz (somente se não encontrou apps modulares profundos)
	if len(result.services) == 0 {
		rootPkgPath := filepath.Join(sd.RootDir, "package.json")
		if data, err := os.ReadFile(rootPkgPath); err == nil {
			var pkg struct {
				Scripts      map[string]string `json:"scripts"`
				Dependencies map[string]string `json:"dependencies"`
				DevDeps      map[string]string `json:"devDependencies"`
			}
			_ = json.Unmarshal(data, &pkg)

			pm := sd.packageManager()

			// Procura por scripts modulares do tipo dev:<module>
			var modularDevs []string
			for scriptName := range pkg.Scripts {
				if strings.HasPrefix(scriptName, "dev:") {
					modularDevs = append(modularDevs, scriptName)
				}
			}
			sort.Strings(modularDevs)

			if len(modularDevs) > 0 {
				for _, scriptName := range modularDevs {
					svcName := strings.TrimPrefix(scriptName, "dev:")
					cmd := []string{pm, "run", scriptName}
					scriptBody := pkg.Scripts[scriptName]
					port := scriptPort(scriptBody)
					if port == 0 {
						lower := strings.ToLower(svcName)
						pref := 3000
						if strings.Contains(lower, "front") || strings.Contains(lower, "web") || strings.Contains(lower, "ui") {
							pref = 5173
						} else if strings.Contains(lower, "api") || strings.Contains(lower, "back") || strings.Contains(lower, "server") {
							pref = 3000
						}
						port = allocPort(pref)
					} else {
						usedPorts[port] = true
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
					result.services[svcName] = svc
					result.markers = append(result.markers, fmt.Sprintf("Node.js %s (%s run %s)", svcName, pm, scriptName))
				}
			} else {
				// Busca script dev ou start padrão na raiz
				script := "dev"
				if _, ok := pkg.Scripts["dev"]; !ok {
					if _, ok := pkg.Scripts["start"]; ok {
						script = "start"
					}
				}

				if _, hasScript := pkg.Scripts[script]; hasScript {
					depsStr := fmt.Sprintf("%v %v", pkg.Dependencies, pkg.DevDeps)
					svcName := "frontend"
					prefPort := 3000
					if strings.Contains(depsStr, "vite") {
						svcName = "frontend"
						prefPort = 5173
					} else if strings.Contains(depsStr, "next") || strings.Contains(depsStr, "nuxt") {
						svcName = "frontend"
						prefPort = 3000
					} else if strings.Contains(depsStr, "nest") || strings.Contains(depsStr, "express") || strings.Contains(depsStr, "fastify") || strings.Contains(depsStr, "koa") {
						svcName = "backend"
						prefPort = 3000
					}

					port := scriptPort(pkg.Scripts[script])
					if port == 0 {
						port = allocPort(prefPort)
					} else {
						usedPorts[port] = true
					}

					cmd := []string{pm, "run", script}
					if pm == "npm" && script == "start" {
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
					result.services[svcName] = svc
					result.markers = append(result.markers, fmt.Sprintf("Node.js (%s run %s)", pm, script))
				}
			}
		}
	}

	// 3. Inspeciona subdiretórios modulares (monorepos) se não cobertos
	candidateDirs := []string{
		"backend", "frontend", "api", "web", "server", "client",
	}

	for _, parent := range []string{"apps", "services", "packages"} {
		entries, err := os.ReadDir(filepath.Join(sd.RootDir, parent))
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					candidateDirs = append(candidateDirs, filepath.Join(parent, e.Name()))
				}
			}
		}
	}

	for _, relDir := range candidateDirs {
		svcName := filepath.Base(relDir)
		if _, exists := result.services[svcName]; exists {
			continue
		}

		// Se este diretório já contém apps aninhados que foram detectados, pula para não duplicar
		alreadyCovered := false
		for _, existingSvc := range result.services {
			if existingSvc.Dir == relDir || strings.HasPrefix(existingSvc.Dir, relDir+string(filepath.Separator)) {
				alreadyCovered = true
				break
			}
		}
		if alreadyCovered {
			continue
		}
		subPkgPath := filepath.Join(sd.RootDir, relDir, "package.json")
		data, err := os.ReadFile(subPkgPath)
		if err != nil {
			continue
		}
		var subPkg struct {
			Scripts      map[string]string `json:"scripts"`
			Dependencies map[string]string `json:"dependencies"`
			DevDeps      map[string]string `json:"devDependencies"`
		}
		if err := json.Unmarshal(data, &subPkg); err != nil {
			continue
		}

		script := "dev"
		if _, ok := subPkg.Scripts["dev"]; !ok {
			if _, ok := subPkg.Scripts["start"]; ok {
				script = "start"
			} else {
				continue
			}
		}

		subPm := detectPackageManager(filepath.Join(sd.RootDir, relDir))
		depsStr := fmt.Sprintf("%v %v", subPkg.Dependencies, subPkg.DevDeps)
		prefPort := 3000
		if strings.Contains(depsStr, "vite") {
			prefPort = 5173
		} else if strings.Contains(depsStr, "next") || strings.Contains(depsStr, "nuxt") {
			prefPort = 3000
		} else if strings.Contains(svcName, "front") || strings.Contains(svcName, "web") {
			prefPort = 5173
		}

		port := scriptPort(subPkg.Scripts[script])
		if port == 0 {
			port = allocPort(prefPort)
		} else {
			usedPorts[port] = true
		}

		cmd := []string{subPm, "run", script}
		if subPm == "npm" && script == "start" {
			cmd = []string{"npm", "start"}
		}

		svc := domain.ServiceConfig{
			Dir:        relDir,
			Command:    cmd,
			Ports:      []int{port},
			PortPolicy: domain.PortPolicyRemap,
			HealthCheck: &domain.HealthCheckConfig{
				Type: domain.HealthCheckTCP,
				Port: port,
			},
		}
		result.services[svcName] = svc
		result.markers = append(result.markers, fmt.Sprintf("Node.js %s (%s run %s)", relDir, subPm, script))
	}

	return result
}

func detectProjectPrefix(rootDir string, appNames []string) string {
	// 1. Root ou sub package.json name com escopo (e.g. "@atlas/workspace" -> "atlas")
	for _, p := range []string{"package.json", "backend/package.json", "frontend/package.json"} {
		if data, err := os.ReadFile(filepath.Join(rootDir, p)); err == nil {
			var pkg struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(data, &pkg); err == nil && pkg.Name != "" {
				name := pkg.Name
				if strings.HasPrefix(name, "@") {
					parts := strings.Split(name[1:], "/")
					if len(parts) > 0 && parts[0] != "" {
						return strings.ToLower(parts[0])
					}
				}
			}
		}
	}

	// 2. Prefixo compartilhado entre apps (e.g. "atlas-viabilidade", "atlas-erp" -> "atlas")
	if len(appNames) > 0 {
		var candidate string
		for _, name := range appNames {
			idx := strings.Index(name, "-")
			if idx > 0 {
				p := strings.ToLower(name[:idx])
				if candidate == "" {
					candidate = p
				} else if candidate != p {
					candidate = ""
					break
				}
			} else {
				candidate = ""
				break
			}
		}
		if candidate != "" {
			return candidate
		}
	}

	// 3. Base directory name se não for numérico ou pasta temporária
	base := strings.ToLower(filepath.Base(rootDir))
	if _, err := strconv.Atoi(base); err != nil && base != "." && base != "/" && base != "" &&
		!strings.Contains(base, "temp") && !strings.Contains(base, "tmp") && !strings.Contains(base, "test") {
		return base
	}

	return ""
}

func cleanAppName(appName, prefix string) string {
	lowerName := strings.ToLower(appName)
	lowerPref := strings.ToLower(prefix)
	if lowerPref != "" && strings.HasPrefix(lowerName, lowerPref+"-") {
		return appName[len(prefix)+1:]
	}
	if lowerPref != "" && strings.HasPrefix(lowerName, lowerPref+"_") {
		return appName[len(prefix)+1:]
	}
	return appName
}

func findBackendScript(scripts map[string]string, appName, cleanName string) (string, string) {
	patterns := []string{
		"start:dev:" + cleanName,
		"start:dev:" + appName,
		"start:" + cleanName + ":dev",
		"start:" + appName + ":dev",
		"start:dev",
		"dev:" + cleanName,
		"dev:" + appName,
		"dev",
	}
	for _, p := range patterns {
		if body, ok := scripts[p]; ok {
			if p == "start:dev" || p == "dev" {
				if strings.Contains(body, appName) || strings.Contains(body, cleanName) {
					return p, body
				}
			} else {
				return p, body
			}
		}
	}

	var fallbackName, fallbackBody string
	for name, body := range scripts {
		if (strings.Contains(body, "nest start "+appName) || strings.Contains(body, "nest start "+cleanName)) && strings.Contains(body, "--watch") {
			if !strings.Contains(name, "worker") {
				return name, body
			}
			fallbackName = name
			fallbackBody = body
		}
	}
	if fallbackName != "" {
		return fallbackName, fallbackBody
	}

	for name, body := range scripts {
		if strings.Contains(body, "nest start "+appName) || strings.Contains(body, "nest start "+cleanName) {
			if !strings.Contains(name, "worker") {
				return name, body
			}
			fallbackName = name
			fallbackBody = body
		}
	}
	if fallbackName != "" {
		return fallbackName, fallbackBody
	}

	return "", ""
}

func findFrontendScript(scripts map[string]string, appName, cleanName string) (string, string) {
	patterns := []string{
		"dev:" + cleanName,
		"dev:" + appName,
		"start:" + cleanName,
		"start:" + appName,
	}
	for _, p := range patterns {
		if body, ok := scripts[p]; ok {
			return p, body
		}
	}
	for name, body := range scripts {
		if strings.HasPrefix(name, "dev") && (strings.Contains(body, appName) || strings.Contains(body, cleanName)) {
			return name, body
		}
	}
	for name, body := range scripts {
		if strings.Contains(body, appName) || strings.Contains(body, cleanName) {
			return name, body
		}
	}
	return "", ""
}

// detectNestedMonorepoApps inspeciona diretórios modulares como backend/apps/* e frontend/apps/*.
func (sd *StackDetector) detectNestedMonorepoApps(usedPorts map[int]bool, allocPort func(int) int) nodeDetection {
	result := nodeDetection{
		services: make(map[string]domain.ServiceConfig),
		markers:  make([]string, 0),
	}

	var allAppNames []string
	if bEntries, err := os.ReadDir(filepath.Join(sd.RootDir, "backend", "apps")); err == nil {
		for _, e := range bEntries {
			if e.IsDir() {
				allAppNames = append(allAppNames, e.Name())
			}
		}
	}
	if fEntries, err := os.ReadDir(filepath.Join(sd.RootDir, "frontend", "apps")); err == nil {
		for _, e := range fEntries {
			if e.IsDir() {
				allAppNames = append(allAppNames, e.Name())
			}
		}
	}
	prefix := detectProjectPrefix(sd.RootDir, allAppNames)

	// 1. Inspeciona backend/apps
	backendDir := filepath.Join(sd.RootDir, "backend")
	backendAppsDir := filepath.Join(backendDir, "apps")
	if entries, err := os.ReadDir(backendAppsDir); err == nil {
		var backendScripts map[string]string
		if bData, err := os.ReadFile(filepath.Join(backendDir, "package.json")); err == nil {
			var bPkg struct {
				Scripts map[string]string `json:"scripts"`
			}
			_ = json.Unmarshal(bData, &bPkg)
			backendScripts = bPkg.Scripts
		}
		bPm := detectPackageManager(backendDir)

		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			appName := e.Name()
			clean := cleanAppName(appName, prefix)
			scriptName, scriptBody := findBackendScript(backendScripts, appName, clean)

			var cmd []string
			dir := "backend"
			if scriptName != "" {
				cmd = []string{bPm, "run", scriptName}
			} else {
				innerPkg := filepath.Join(backendAppsDir, appName, "package.json")
				if iData, err := os.ReadFile(innerPkg); err == nil {
					var iPkg struct {
						Scripts map[string]string `json:"scripts"`
					}
					_ = json.Unmarshal(iData, &iPkg)
					if _, hasDev := iPkg.Scripts["dev"]; hasDev {
						dir = filepath.Join("backend", "apps", appName)
						cmd = []string{detectPackageManager(filepath.Join(backendAppsDir, appName)), "run", "dev"}
					}
				}
			}

			if len(cmd) == 0 {
				continue
			}

			port := scriptPort(scriptBody)
			if port == 0 {
				port = allocPort(3000)
			} else {
				usedPorts[port] = true
			}

			svcName := clean + "-backend"
			result.services[svcName] = domain.ServiceConfig{
				Dir:        dir,
				Command:    cmd,
				Ports:      []int{port},
				PortPolicy: domain.PortPolicyRemap,
				HealthCheck: &domain.HealthCheckConfig{
					Type: domain.HealthCheckTCP,
					Port: port,
				},
			}
			result.markers = append(result.markers, fmt.Sprintf("Node.js %s (%s)", svcName, strings.Join(cmd, " ")))
		}
	}

	// 2. Inspeciona frontend/apps
	frontendDir := filepath.Join(sd.RootDir, "frontend")
	frontendAppsDir := filepath.Join(frontendDir, "apps")
	if entries, err := os.ReadDir(frontendAppsDir); err == nil {
		var frontendScripts map[string]string
		if fData, err := os.ReadFile(filepath.Join(frontendDir, "package.json")); err == nil {
			var fPkg struct {
				Scripts map[string]string `json:"scripts"`
			}
			_ = json.Unmarshal(fData, &fPkg)
			frontendScripts = fPkg.Scripts
		}
		fPm := detectPackageManager(frontendDir)

		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			appName := e.Name()
			clean := cleanAppName(appName, prefix)
			scriptName, scriptBody := findFrontendScript(frontendScripts, appName, clean)

			var cmd []string
			dir := "frontend"
			if scriptName != "" {
				cmd = []string{fPm, "run", scriptName}
			} else {
				innerPkg := filepath.Join(frontendAppsDir, appName, "package.json")
				if iData, err := os.ReadFile(innerPkg); err == nil {
					var iPkg struct {
						Scripts map[string]string `json:"scripts"`
					}
					_ = json.Unmarshal(iData, &iPkg)
					if _, hasDev := iPkg.Scripts["dev"]; hasDev {
						dir = filepath.Join("frontend", "apps", appName)
						cmd = []string{detectPackageManager(filepath.Join(frontendAppsDir, appName)), "run", "dev"}
					}
				}
			}

			if len(cmd) == 0 {
				continue
			}

			port := scriptPort(scriptBody)
			if port == 0 {
				port = allocPort(5173)
			} else {
				usedPorts[port] = true
			}

			svcName := clean + "-frontend"
			result.services[svcName] = domain.ServiceConfig{
				Dir:        dir,
				Command:    cmd,
				Ports:      []int{port},
				PortPolicy: domain.PortPolicyRemap,
				HealthCheck: &domain.HealthCheckConfig{
					Type: domain.HealthCheckTCP,
					Port: port,
				},
			}
			result.markers = append(result.markers, fmt.Sprintf("Node.js %s (%s)", svcName, strings.Join(cmd, " ")))
		}
	}

	return result
}

// detectNodeJS inspeciona package.json e lockfiles.
func (sd *StackDetector) detectNodeJS() (domain.ServiceConfig, string) {
	res := sd.detectNodeServices(make(map[int]bool))
	if svc, ok := res.services["frontend"]; ok {
		marker := ""
		if len(res.markers) > 0 {
			marker = res.markers[0]
		}
		return svc, marker
	}
	for _, svc := range res.services {
		marker := ""
		if len(res.markers) > 0 {
			marker = res.markers[0]
		}
		return svc, marker
	}
	return domain.ServiceConfig{}, ""
}

func scriptPort(script string) int {
	args := strings.Fields(script)
	for i, arg := range args {
		value := ""
		switch {
		case (arg == "--port" || arg == "-p") && i+1 < len(args):
			value = args[i+1]
		case strings.HasPrefix(arg, "--port="):
			value = strings.TrimPrefix(arg, "--port=")
		}
		if value != "" {
			port, err := strconv.Atoi(value)
			if err == nil && port > 0 && port <= 65535 {
				return port
			}
		}
	}
	return 0
}

func parseComposeHostPort(raw interface{}) int {
	switch v := raw.(type) {
	case int:
		if v > 0 && v <= 65535 {
			return v
		}
	case string:
		str := strings.TrimSpace(v)
		parts := strings.Split(str, ":")
		var hostPart string
		switch len(parts) {
		case 1:
			hostPart = parts[0]
		case 2:
			hostPart = parts[0]
		case 3:
			hostPart = parts[1]
		default:
			return 0
		}
		if idx := strings.Index(hostPart, "-"); idx > 0 {
			hostPart = hostPart[:idx]
		}
		p, err := strconv.Atoi(hostPart)
		if err == nil && p > 0 && p <= 65535 {
			return p
		}
	case map[string]interface{}:
		if pub, ok := v["published"]; ok {
			return parseComposeHostPort(pub)
		}
	}
	return 0
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

func detectPackageManager(dir string) string {
	for _, candidate := range []struct{ file, manager string }{
		{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"},
		{"bun.lockb", "bun"}, {"bun.lock", "bun"}, {"package-lock.json", "npm"},
	} {
		if fileExists(filepath.Join(dir, candidate.file)) {
			return candidate.manager
		}
	}
	return "npm"
}

// packageManager uses a fixed precedence when multiple lockfiles are present.
func (sd *StackDetector) packageManager() string {
	return detectPackageManager(sd.RootDir)
}

// DetectTasks reads repository metadata only; it never executes discovered commands.
// Explicit Node scripts win generic name collisions, followed by Go and Python.
// Other suites remain accessible as test:go / test:python on a collision.
// Dependencies cannot be inferred reliably from stack markers and remain explicit.
func (sd *StackDetector) DetectTasks() (map[string]domain.TaskConfig, error) {
	tasks := make(map[string]domain.TaskConfig)
	data, err := os.ReadFile(filepath.Join(sd.RootDir, "package.json"))
	if err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil, fmt.Errorf("parse package.json: %w", err)
		}
		for name, script := range pkg.Scripts {
			if strings.TrimSpace(name) != "" && strings.TrimSpace(script) != "" {
				tasks[name] = domain.TaskConfig{Command: []string{sd.packageManager(), "run", name}}
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	addSuite := func(stack string, command []string) {
		name := "test"
		if _, exists := tasks[name]; exists {
			name = "test:" + stack
			for {
				if _, exists := tasks[name]; !exists {
					break
				}
				name += ":" + stack
			}
		}
		tasks[name] = domain.TaskConfig{Command: command}
	}
	if fileExists(filepath.Join(sd.RootDir, "go.mod")) {
		addSuite("go", []string{"go", "test", "./..."})
	}
	if fileExists(filepath.Join(sd.RootDir, "manage.py")) {
		addSuite("python", []string{"python", "manage.py", "test"})
	} else {
		pytest := fileExists(filepath.Join(sd.RootDir, "pytest.ini")) || fileExists(filepath.Join(sd.RootDir, "conftest.py"))
		for _, file := range []string{"requirements.txt", "requirements-dev.txt", "pyproject.toml", "setup.cfg", "tox.ini"} {
			data, err := os.ReadFile(filepath.Join(sd.RootDir, file))
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			if strings.Contains(strings.ToLower(string(data)), "pytest") {
				pytest = true
			}
		}
		if pytest {
			addSuite("python", []string{"python", "-m", "pytest"})
		}
	}
	return tasks, nil
}
