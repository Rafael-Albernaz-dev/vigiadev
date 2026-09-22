package detector_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
