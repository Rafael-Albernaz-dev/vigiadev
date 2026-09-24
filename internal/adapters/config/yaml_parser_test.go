package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestFindConfigFile_Precedence(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Cenário: Nenhum arquivo existe
	_, err := config.FindConfigFile(tempDir, "")
	if !errors.Is(err, config.ErrConfigNotFound) {
		t.Fatalf("esperava ErrConfigNotFound, obteve %v", err)
	}

	// 2. Cenário: dev.yaml criado
	devPath := filepath.Join(tempDir, "dev.yaml")
	if err := os.WriteFile(devPath, []byte("version: 1\nproject_name: test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	found, err := config.FindConfigFile(tempDir, "")
	if err != nil || found != devPath {
		t.Fatalf("esperava encontrar dev.yaml, obteve %s (err: %v)", found, err)
	}

	// 3. Cenário: vigiadev.yaml criado (tem precedência sobre dev.yaml)
	vigiadevPath := filepath.Join(tempDir, "vigiadev.yaml")
	if err := os.WriteFile(vigiadevPath, []byte("version: 1\nproject_name: test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	found, err = config.FindConfigFile(tempDir, "")
	if err != nil || found != vigiadevPath {
		t.Fatalf("esperava encontrar vigiadev.yaml sobre dev.yaml, obteve %s (err: %v)", found, err)
	}

	// 4. Cenário: vigiadev.local.yaml criado (tem precedência absoluta no diretório)
	localPath := filepath.Join(tempDir, "vigiadev.local.yaml")
	if err := os.WriteFile(localPath, []byte("version: 1\nproject_name: test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	found, err = config.FindConfigFile(tempDir, "")
	if err != nil || found != localPath {
		t.Fatalf("esperava encontrar vigiadev.local.yaml sobre vigiadev.yaml, obteve %s (err: %v)", found, err)
	}

	// 5. Cenário: flag explícita sobrescreve tudo
	explicitFile := filepath.Join(tempDir, "custom.yaml")
	if err := os.WriteFile(explicitFile, []byte("version: 1\nproject_name: test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	found, err = config.FindConfigFile(tempDir, "custom.yaml")
	if err != nil || found != explicitFile {
		t.Fatalf("esperava encontrar explicit custom.yaml, obteve %s (err: %v)", found, err)
	}
}

func TestLoadConfig_ValidAndDefaults(t *testing.T) {
	content := `
version: 1
project_name: sample-app
services:
  web:
    command: ["python3", "-m", "http.server", "8000"]
    ports: [8000]
    healthcheck:
      type: http
      url: "http://127.0.0.1:8000/health"
  cache:
    compose_service: redis
    ports: [6379]
    healthcheck:
      type: tcp
`
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "vigiadev.yaml")
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("erro inesperado ao carregar config válido: %v", err)
	}

	if cfg.ProjectName != "sample-app" {
		t.Errorf("esperava project_name 'sample-app', obteve '%s'", cfg.ProjectName)
	}

	web := cfg.Services["web"]
	if web.PortPolicy != domain.PortPolicyReuse {
		t.Errorf("esperava port_policy padrão 'reuse', obteve '%s'", web.PortPolicy)
	}
	if web.HealthCheck.ExpectedStatus != 200 {
		t.Errorf("esperava ExpectedStatus padrão 200, obteve %d", web.HealthCheck.ExpectedStatus)
	}
	if web.HealthCheck.IntervalMs != 1000 {
		t.Errorf("esperava IntervalMs padrão 1000, obteve %d", web.HealthCheck.IntervalMs)
	}

	cache := cfg.Services["cache"]
	if cache.HealthCheck.Port != 6379 {
		t.Errorf("esperava que healthcheck tcp herdasse porta 6379, obteve %d", cache.HealthCheck.Port)
	}
}

func TestLoadConfig_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "versao invalida",
			yaml:    "version: 2\nproject_name: app\nservices:\n  a:\n    command: ['ls']\n",
			wantErr: "versão suportada é 1",
		},
		{
			name:    "sem project_name",
			yaml:    "version: 1\nservices:\n  a:\n    command: ['ls']\n",
			wantErr: "'project_name' é obrigatório",
		},
		{
			name:    "servico sem command nem compose_service",
			yaml:    "version: 1\nproject_name: app\nservices:\n  a:\n    ports: [8080]\n",
			wantErr: "deve declarar 'command' ou 'compose_service'",
		},
		{
			name:    "porta fora do range",
			yaml:    "version: 1\nproject_name: app\nservices:\n  a:\n    command: ['ls']\n    ports: [70000]\n",
			wantErr: "possui porta inválida",
		},
		{
			name:    "healthcheck http sem url",
			yaml:    "version: 1\nproject_name: app\nservices:\n  a:\n    command: ['ls']\n    healthcheck:\n      type: http\n",
			wantErr: "healthcheck HTTP do serviço 'a' exige 'url'",
		},
	}

	tempDir := t.TempDir()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tempDir, tc.name+".yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0644); err != nil {
				t.Fatal(err)
			}

			_, err := config.LoadConfig(path)
			if err == nil {
				t.Fatalf("esperava erro contendo '%s', mas teve sucesso", tc.wantErr)
			}
			if !errors.Is(err, config.ErrInvalidConfig) {
				t.Errorf("esperava que o erro encapsulasse ErrInvalidConfig, obteve: %v", err)
			}
		})
	}
}

func TestLoadConfig_Tasks(t *testing.T) {
	tempDir := t.TempDir()

	content := `
version: 1
project_name: task-test
services:
  db:
    command: ["postgres"]
tasks:
  migrate:
    command: "npx prisma migrate deploy"
    depends_on: [db]
    env:
      DB_HOST: "127.0.0.1"
  seed:
    command: ["node", "seed.js"]
    depends_on: [db]
`
	path := filepath.Join(tempDir, "vigiadev.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("falha ao carregar config com tasks: %v", err)
	}

	if len(cfg.Tasks) != 2 {
		t.Fatalf("esperava 2 tasks, obteve %d", len(cfg.Tasks))
	}

	migrate, exists := cfg.Tasks["migrate"]
	if !exists {
		t.Fatal("task 'migrate' não encontrada")
	}
	if len(migrate.Command) != 4 || migrate.Command[0] != "npx" || migrate.Command[3] != "deploy" {
		t.Errorf("esperava command split de string ['npx', 'prisma', 'migrate', 'deploy'], obteve %v", migrate.Command)
	}
	if len(migrate.DependsOn) != 1 || migrate.DependsOn[0] != "db" {
		t.Errorf("esperava depends_on [db], obteve %v", migrate.DependsOn)
	}
	if migrate.Env["DB_HOST"] != "127.0.0.1" {
		t.Errorf("esperava env DB_HOST=127.0.0.1, obteve %v", migrate.Env)
	}

	seed, exists := cfg.Tasks["seed"]
	if !exists {
		t.Fatal("task 'seed' não encontrada")
	}
	if len(seed.Command) != 2 || seed.Command[0] != "node" || seed.Command[1] != "seed.js" {
		t.Errorf("esperava command ['node', 'seed.js'], obteve %v", seed.Command)
	}

	// Cenário de erro: task com depends_on inexistente
	badContent := `
version: 1
project_name: task-test
services:
  app:
    command: ["node", "server.js"]
tasks:
  broken:
    command: "run"
    depends_on: [nonexistent]
`
	badPath := filepath.Join(tempDir, "bad.yaml")
	_ = os.WriteFile(badPath, []byte(badContent), 0644)
	if _, err := config.LoadConfig(badPath); err == nil {
		t.Error("esperava erro ao referenciar serviço inexistente em depends_on de task")
	}
}
