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
