package detector_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/detector"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestDetect_DockerCompose(t *testing.T) {
	tempDir := t.TempDir()

	composeContent := `
services:
  db:
    image: postgres:16
    ports:
      - "5432:5432"
  cache:
    image: redis:alpine
    ports:
      - "6379:6379"
`
	if err := os.WriteFile(filepath.Join(tempDir, "docker-compose.yml"), []byte(composeContent), 0644); err != nil {
		t.Fatal(err)
	}

	d := detector.NewStackDetector(tempDir)
	result, err := d.Detect()
	if err != nil {
		t.Fatalf("erro inesperado na detecção: %v", err)
	}

	if len(result.Config.Services) != 2 {
		t.Fatalf("esperava 2 serviços detectados, obteve %d", len(result.Config.Services))
	}

	db := result.Config.Services["db"]
	if len(db.Ports) == 0 || db.Ports[0] != 5432 {
		t.Errorf("esperava porta 5432 no postgres, obteve %v", db.Ports)
	}

	cache := result.Config.Services["cache"]
	if len(cache.Ports) == 0 || cache.Ports[0] != 6379 {
		t.Errorf("esperava porta 6379 no redis, obteve %v", cache.Ports)
	}
}

func TestDetect_NodeJS_Vite_Pnpm(t *testing.T) {
	tempDir := t.TempDir()

	pkgJSON := `
{
  "name": "meu-front",
  "scripts": {
    "dev": "vite"
  },
  "devDependencies": {
    "vite": "^5.0.0"
  }
}


`
	if err := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "pnpm-lock.yaml"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	d := detector.NewStackDetector(tempDir)
	result, err := d.Detect()
	if err != nil {
		t.Fatal(err)
	}

	front, exists := result.Config.Services["frontend"]
	if !exists {
		t.Fatal("esperava serviço 'frontend' detectado")
	}

	if front.Command[0] != "pnpm" || front.Command[2] != "dev" {
		t.Errorf("esperava comando pnpm run dev, obteve %v", front.Command)
	}

	if len(front.Ports) == 0 || front.Ports[0] != 5173 {
		t.Errorf("esperava inferência da porta 5173 para Vite, obteve %v", front.Ports)
	}

	if front.PortPolicy != domain.PortPolicyRemap {
		t.Errorf("esperava port_policy remap, obteve %s", front.PortPolicy)
	}
}

func TestDetectNodeJS_ExplicitPortArg(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		port         int
	}{
		{"long", "vite --port 5187", 5187},
		{"short", "vite -p 4187", 4187},
		{"equals", "vite --port=5188", 5188},
		{"invalid", "vite --port 99999", 5173},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pkg := `{"scripts":{"dev":"` + tc.script + `"},"devDependencies":{"vite":"^5"}}`
			if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0600); err != nil {
				t.Fatal(err)
			}
			result, err := detector.NewStackDetector(dir).Detect()
			if err != nil {
				t.Fatal(err)
			}
			svc := result.Config.Services["frontend"]
			if len(svc.Ports) != 1 || svc.Ports[0] != tc.port || svc.HealthCheck == nil || svc.HealthCheck.Port != tc.port {
				t.Fatalf("explicit port not reflected in service: %+v", svc)
			}
		})
	}
}

func TestDetect_Python_Django(t *testing.T) {
	tempDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tempDir, "manage.py"), []byte("#!/usr/bin/env python\n"), 0644); err != nil {
		t.Fatal(err)
	}

	d := detector.NewStackDetector(tempDir)
	result, err := d.Detect()
	if err != nil {
		t.Fatal(err)
	}

	app, exists := result.Config.Services["app"]
	if !exists {
		t.Fatal("esperava serviço 'app' detectado para Django")
	}

	expectedCmd := "python manage.py runserver 0.0.0.0:{port}"
	actualCmd := strings.Join(app.Command, " ")
	if actualCmd != expectedCmd {
		t.Errorf("esperava comando '%s', obteve '%s'", expectedCmd, actualCmd)
	}

	if app.HealthCheck == nil || app.HealthCheck.Type != domain.HealthCheckHTTP {
		t.Error("esperava healthcheck HTTP no Django")
	}
}

func TestDetect_Python_FastAPI(t *testing.T) {
	tempDir := t.TempDir()

	reqs := "fastapi>=0.100.0\nuvicorn>=0.20.0\n"
	if err := os.WriteFile(filepath.Join(tempDir, "requirements.txt"), []byte(reqs), 0644); err != nil {
		t.Fatal(err)
	}

	d := detector.NewStackDetector(tempDir)
	result, err := d.Detect()
	if err != nil {
		t.Fatal(err)
	}

	app, exists := result.Config.Services["app"]
	if !exists {
		t.Fatal("esperava serviço 'app' detectado para FastAPI")
	}

	if app.Command[0] != "uvicorn" {
		t.Errorf("esperava comando uvicorn, obteve %v", app.Command)
	}
}

func TestGenerateYAML(t *testing.T) {
	cfg := &domain.VigiaConfig{
		Version:     1,
		ProjectName: "teste-yaml",
		Services: map[string]domain.ServiceConfig{
			"web": {
				Command: []string{"node", "server.js"},
				Ports:   []int{3000},
			},
		},
	}

	yamlStr, err := detector.GenerateYAML(cfg, []string{"Node.js"})
	if err != nil {
		t.Fatalf("erro ao gerar YAML: %v", err)
	}

	if !strings.Contains(yamlStr, "Auto-detectado a partir de: Node.js") {
		t.Error("esperava cabeçalho com marcadores detectados")
	}
	if !strings.Contains(yamlStr, "project_name: teste-yaml") {
		t.Error("esperava project_name no YAML")
	}
}

func TestDetect_Tasks(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  map[string]string
	}{
		{"npm", map[string]string{"package.json": `{"scripts":{"test":"test command","lint":"lint command","empty":""}}`, "package-lock.json": ""}, map[string]string{"test": "npm run test", "lint": "npm run lint"}},
		{"pnpm precedence", map[string]string{"package.json": `{"scripts":{"test":"x"}}`, "pnpm-lock.yaml": "", "yarn.lock": "", "bun.lockb": "", "package-lock.json": ""}, map[string]string{"test": "pnpm run test"}},
		{"yarn", map[string]string{"package.json": `{"scripts":{"test":"x"}}`, "yarn.lock": "", "bun.lockb": ""}, map[string]string{"test": "yarn run test"}},
		{"bun", map[string]string{"package.json": `{"scripts":{"test":"x"}}`, "bun.lockb": ""}, map[string]string{"test": "bun run test"}},
		{"bun text", map[string]string{"package.json": `{"scripts":{"test":"x"}}`, "bun.lock": ""}, map[string]string{"test": "bun run test"}},
		{"go", map[string]string{"go.mod": "module example.test/demo\n"}, map[string]string{"test": "go test ./..."}},
		{"django", map[string]string{"manage.py": "", "pytest.ini": ""}, map[string]string{"test": "python manage.py test"}},
		{"pytest config", map[string]string{"pytest.ini": ""}, map[string]string{"test": "python -m pytest"}},
		{"pytest dependency", map[string]string{"requirements-dev.txt": "pytest>=8\n"}, map[string]string{"test": "python -m pytest"}},
		{"pytest pyproject", map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\n"}, map[string]string{"test": "python -m pytest"}},
		{"mixed", map[string]string{"package.json": `{"scripts":{"test":"x","test:go":"y"}}`, "go.mod": "", "manage.py": ""}, map[string]string{"test": "npm run test", "test:go": "npm run test:go", "test:go:go": "go test ./...", "test:python": "python manage.py test"}},
		{"empty", map[string]string{}, map[string]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			result, err := detector.NewStackDetector(dir).Detect()
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Config.Tasks) != len(tc.want) {
				t.Fatalf("tasks: %+v, want %v", result.Config.Tasks, tc.want)
			}
			for name, want := range tc.want {
				if got := strings.Join(result.Config.Tasks[name].Command, " "); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
		})
	}
	t.Run("malformed package", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := detector.NewStackDetector(dir).Detect(); err == nil {
			t.Fatal("expected JSON error")
		}
	})
}

func TestGenerateYAML_WithTasks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := detector.NewStackDetector(dir).Detect()
	if err != nil {
		t.Fatal(err)
	}
	data, err := detector.GenerateYAML(result.Config, result.Markers)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data, "tasks:") {
		t.Fatal(data)
	}
	path := filepath.Join(dir, "vigiadev.yaml")
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(loaded.Tasks["test"].Command, " "); got != "go test ./..." {
		t.Fatalf("got %q", got)
	}
}

func TestDetect_DockerCompose_PublishedHostPorts(t *testing.T) {
	dir := t.TempDir()
	composeYAML := `
services:
  zitadel-postgres:
    image: postgres:17-alpine
    ports:
      - '127.0.0.1:5434:5432'
  db:
    image: postgres:17
    ports:
      - '5432:5432'
  minio:
    image: minio/minio
    ports:
      - '9000-9001:9000-9001'
  gateway:
    image: nginx:alpine
    ports:
      - 8080
`
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(composeYAML), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := detector.NewStackDetector(dir).Detect()
	if err != nil {
		t.Fatal(err)
	}
	svcs := result.Config.Services
	if svcs["zitadel-postgres"].Ports[0] != 5434 {
		t.Fatalf("expected host port 5434 for zitadel-postgres, got %v", svcs["zitadel-postgres"].Ports)
	}
	if svcs["db"].Ports[0] != 5432 {
		t.Fatalf("expected host port 5432 for db, got %v", svcs["db"].Ports)
	}
	if svcs["minio"].Ports[0] != 9000 {
		t.Fatalf("expected host port 9000 for minio, got %v", svcs["minio"].Ports)
	}
	if svcs["gateway"].Ports[0] != 8080 {
		t.Fatalf("expected host port 8080 for gateway, got %v", svcs["gateway"].Ports)
	}
}

func TestDetect_Monorepo_ModularScripts(t *testing.T) {
	dir := t.TempDir()
	pkgJSON := `{
  "name": "@myorg/workspace",
  "scripts": {
    "dev:backend": "node server.js",
    "dev:frontend": "vite --port 5173",
    "dev:platform": "vite --port 5174",
    "dev:erp": "node erp.js"
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := detector.NewStackDetector(dir).Detect()
	if err != nil {
		t.Fatal(err)
	}

	svcs := result.Config.Services
	if len(svcs) != 4 {
		t.Fatalf("expected 4 modular services detected, got %d: %+v", len(svcs), svcs)
	}

	backend, ok := svcs["backend"]
	if !ok || backend.Command[2] != "dev:backend" {
		t.Fatalf("expected backend service running dev:backend, got %+v", backend)
	}

	frontend, ok := svcs["frontend"]
	if !ok || len(frontend.Ports) == 0 || frontend.Ports[0] != 5173 {
		t.Fatalf("expected frontend service on port 5173, got %+v", frontend)
	}

	platform, ok := svcs["platform"]
	if !ok || len(platform.Ports) == 0 || platform.Ports[0] != 5174 {
		t.Fatalf("expected platform service on port 5174, got %+v", platform)
	}

	erp, ok := svcs["erp"]
	if !ok || erp.Command[2] != "dev:erp" {
		t.Fatalf("expected erp service, got %+v", erp)
	}
}

func TestDetect_Monorepo_Subdirectories(t *testing.T) {
	dir := t.TempDir()

	// Backend subfolder
	backendDir := filepath.Join(dir, "backend")
	if err := os.MkdirAll(backendDir, 0755); err != nil {
		t.Fatal(err)
	}
	backendPkg := `{"name":"my-backend","scripts":{"dev":"nest start"},"dependencies":{"@nestjs/core":"^10"}}`
	if err := os.WriteFile(filepath.Join(backendDir, "package.json"), []byte(backendPkg), 0644); err != nil {
		t.Fatal(err)
	}

	// Frontend subfolder
	frontendDir := filepath.Join(dir, "frontend")
	if err := os.MkdirAll(frontendDir, 0755); err != nil {
		t.Fatal(err)
	}
	frontendPkg := `{"name":"my-frontend","scripts":{"dev":"vite"},"dependencies":{"vite":"^5"}}`
	if err := os.WriteFile(filepath.Join(frontendDir, "package.json"), []byte(frontendPkg), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := detector.NewStackDetector(dir).Detect()
	if err != nil {
		t.Fatal(err)
	}

	svcs := result.Config.Services
	if len(svcs) != 2 {
		t.Fatalf("expected 2 subfolder services, got %d: %+v", len(svcs), svcs)
	}

	back, ok := svcs["backend"]
	if !ok || back.Dir != "backend" {
		t.Fatalf("expected backend service with dir 'backend', got %+v", back)
	}

	front, ok := svcs["frontend"]
	if !ok || front.Dir != "frontend" || front.Ports[0] != 5173 {
		t.Fatalf("expected frontend service with dir 'frontend' and port 5173, got %+v", front)
	}
}

func TestDetect_NodeJS_BackendOnly(t *testing.T) {
	dir := t.TempDir()
	pkgJSON := `{
  "name": "my-api",
  "scripts": {"dev": "node server.js"},
  "dependencies": {"express": "^4.18"}
}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := detector.NewStackDetector(dir).Detect()
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := result.Config.Services["frontend"]; ok {
		t.Fatalf("express API should not be detected as 'frontend'")
	}
	if _, ok := result.Config.Services["backend"]; !ok {
		t.Fatalf("expected express API to be detected as 'backend'")
	}
}

func TestDetect_Monorepo_NestedApps(t *testing.T) {
	dir := t.TempDir()

	// Backend monorepo com apps
	backendDir := filepath.Join(dir, "backend")
	_ = os.MkdirAll(filepath.Join(backendDir, "apps", "atlas-viabilidade"), 0755)
	_ = os.MkdirAll(filepath.Join(backendDir, "apps", "atlas-erp"), 0755)

	backendPkg := `{
  "name": "backend",
  "scripts": {
    "start:dev": "nest start atlas-erp --watch",
    "start:dev:viabilidade": "nest start atlas-viabilidade --watch"
  }
}`
	if err := os.WriteFile(filepath.Join(backendDir, "package.json"), []byte(backendPkg), 0644); err != nil {
		t.Fatal(err)
	}

	// Frontend monorepo com apps e workspace
	frontendDir := filepath.Join(dir, "frontend")
	_ = os.MkdirAll(filepath.Join(frontendDir, "apps", "atlas-viabilidade"), 0755)
	_ = os.MkdirAll(filepath.Join(frontendDir, "apps", "atlas-erp"), 0755)

	frontendPkg := `{
  "name": "frontend",
  "scripts": {
    "dev:erp": "npm run dev --workspace @atlas/atlas-erp",
    "dev:viabilidade": "npm run dev --workspace @atlas/atlas-viabilidade -- --port 5175 --strictPort"
  }
}`
	if err := os.WriteFile(filepath.Join(frontendDir, "package.json"), []byte(frontendPkg), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := detector.NewStackDetector(dir).Detect()
	if err != nil {
		t.Fatal(err)
	}

	svcs := result.Config.Services

	// Verifica viabilidade-backend
	viabBack, ok := svcs["viabilidade-backend"]
	if !ok {
		t.Fatalf("expected viabilidade-backend service, got: %+v", svcs)
	}
	if viabBack.Dir != "backend" {
		t.Fatalf("expected viabilidade-backend dir 'backend', got %s", viabBack.Dir)
	}
	if viabBack.Command[2] != "start:dev:viabilidade" {
		t.Fatalf("expected command start:dev:viabilidade, got %v", viabBack.Command)
	}

	// Verifica viabilidade-frontend
	viabFront, ok := svcs["viabilidade-frontend"]
	if !ok {
		t.Fatalf("expected viabilidade-frontend service, got: %+v", svcs)
	}
	if viabFront.Dir != "frontend" {
		t.Fatalf("expected viabilidade-frontend dir 'frontend', got %s", viabFront.Dir)
	}
	if len(viabFront.Ports) == 0 || viabFront.Ports[0] != 5175 {
		t.Fatalf("expected viabilidade-frontend port 5175, got %v", viabFront.Ports)
	}

	// Verifica erp-backend
	erpBack, ok := svcs["erp-backend"]
	if !ok {
		t.Fatalf("expected erp-backend service, got: %+v", svcs)
	}
	if erpBack.Dir != "backend" {
		t.Fatalf("expected erp-backend dir 'backend', got %s", erpBack.Dir)
	}
	if erpBack.Command[2] != "start:dev" {
		t.Fatalf("expected erp-backend command start:dev, got %v", erpBack.Command)
	}

	// Verifica erp-frontend
	erpFront, ok := svcs["erp-frontend"]
	if !ok {
		t.Fatalf("expected erp-frontend service, got: %+v", svcs)
	}
	if erpFront.Dir != "frontend" {
		t.Fatalf("expected erp-frontend dir 'frontend', got %s", erpFront.Dir)
	}
	if erpFront.Command[2] != "dev:erp" {
		t.Fatalf("expected erp-frontend command dev:erp, got %v", erpFront.Command)
	}
}

func TestDetect_Monorepo_HybridNestedAndPlain(t *testing.T) {
	dir := t.TempDir()

	// Backend monorepo com apps/api
	backendDir := filepath.Join(dir, "backend")
	_ = os.MkdirAll(filepath.Join(backendDir, "apps", "api"), 0755)

	backendPkg := `{
  "name": "backend",
  "scripts": {
    "start:dev:api": "nest start api --watch"
  }
}`
	if err := os.WriteFile(filepath.Join(backendDir, "package.json"), []byte(backendPkg), 0644); err != nil {
		t.Fatal(err)
	}

	// Frontend simples (não é monorepo de apps, tem apenas frontend/package.json)
	frontendDir := filepath.Join(dir, "frontend")
	_ = os.MkdirAll(frontendDir, 0755)
	frontendPkg := `{
  "name": "frontend-web",
  "scripts": {
    "dev": "vite"
  },
  "dependencies": {
    "vite": "^5.0.0"
  }
}`
	if err := os.WriteFile(filepath.Join(frontendDir, "package.json"), []byte(frontendPkg), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := detector.NewStackDetector(dir).Detect()
	if err != nil {
		t.Fatal(err)
	}

	svcs := result.Config.Services

	// Garante que o app aninhado de backend foi detectado
	apiBack, ok := svcs["api-backend"]
	if !ok {
		t.Fatalf("expected api-backend in services, got: %+v", svcs)
	}
	if apiBack.Dir != "backend" {
		t.Fatalf("expected api-backend Dir 'backend', got %s", apiBack.Dir)
	}

	// Garante que o frontend plano também foi detectado (não ignorado por early return)
	front, ok := svcs["frontend"]
	if !ok {
		t.Fatalf("expected frontend in services, got: %+v", svcs)
	}
	if front.Dir != "frontend" {
		t.Fatalf("expected frontend Dir 'frontend', got %s", front.Dir)
	}
}

func TestDetect_Monorepo_NestedApps_InnerPackageJson(t *testing.T) {
	dir := t.TempDir()

	// Backend sem script no root do backend, mas com package.json próprio no app
	backendDir := filepath.Join(dir, "backend")
	workerAppDir := filepath.Join(backendDir, "apps", "worker")
	_ = os.MkdirAll(workerAppDir, 0755)

	backendPkg := `{
  "name": "backend-root",
  "scripts": {}
}`
	if err := os.WriteFile(filepath.Join(backendDir, "package.json"), []byte(backendPkg), 0644); err != nil {
		t.Fatal(err)
	}

	workerPkg := `{
  "name": "worker",
  "scripts": {
    "dev": "node worker.js"
  }
}`
	if err := os.WriteFile(filepath.Join(workerAppDir, "package.json"), []byte(workerPkg), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := detector.NewStackDetector(dir).Detect()
	if err != nil {
		t.Fatal(err)
	}

	svcs := result.Config.Services
	workerBack, ok := svcs["worker-backend"]
	if !ok {
		t.Fatalf("expected worker-backend in services, got: %+v", svcs)
	}
	expectedDir := filepath.Join("backend", "apps", "worker")
	if workerBack.Dir != expectedDir {
		t.Fatalf("expected worker-backend Dir '%s', got '%s'", expectedDir, workerBack.Dir)
	}
}


