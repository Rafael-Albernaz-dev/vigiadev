package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestLayeredEnvLoading(t *testing.T) {
	tempDir := t.TempDir()

	// 1. .env na raiz
	rootEnv := "COMMON=root\nROOT_ONLY=root_val\nOVERRIDDEN=root\n"
	if err := os.WriteFile(filepath.Join(tempDir, ".env"), []byte(rootEnv), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. .env.local na raiz
	rootLocalEnv := "OVERRIDDEN=root_local\nLOCAL_ONLY=local_val\n"
	if err := os.WriteFile(filepath.Join(tempDir, ".env.local"), []byte(rootLocalEnv), 0644); err != nil {
		t.Fatal(err)
	}

	// Cria subdiretório para serviço
	backendDir := filepath.Join(tempDir, "backend")
	if err := os.MkdirAll(backendDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 3. backend/.env
	svcEnv := "OVERRIDDEN=svc_env\nSVC_ONLY=svc_val\n"
	if err := os.WriteFile(filepath.Join(backendDir, ".env"), []byte(svcEnv), 0644); err != nil {
		t.Fatal(err)
	}

	// 4. backend/.env.local
	svcLocalEnv := "OVERRIDDEN=svc_local\n"
	if err := os.WriteFile(filepath.Join(backendDir, ".env.local"), []byte(svcLocalEnv), 0644); err != nil {
		t.Fatal(err)
	}

	// 5. custom env_file
	customEnvPath := filepath.Join(backendDir, ".env.custom")
	customEnv := "OVERRIDDEN=custom_file\nCUSTOM_VAL=123\n"
	if err := os.WriteFile(customEnvPath, []byte(customEnv), 0644); err != nil {
		t.Fatal(err)
	}

	svc := domain.ServiceConfig{
		Dir:     "backend",
		EnvFile: ".env.custom",
		Env: map[string]string{
			"OVERRIDDEN": "yaml_explicit",
			"YAML_ONLY":   "pure_yaml",
		},
	}

	resolved, err := ResolveLayeredEnv(tempDir, svc)
	if err != nil {
		t.Fatalf("unexpected error resolving env: %v", err)
	}

	// Assertivas de precedência
	if resolved["ROOT_ONLY"] != "root_val" {
		t.Errorf("expected ROOT_ONLY=root_val, got '%s'", resolved["ROOT_ONLY"])
	}
	if resolved["LOCAL_ONLY"] != "local_val" {
		t.Errorf("expected LOCAL_ONLY=local_val, got '%s'", resolved["LOCAL_ONLY"])
	}
	if resolved["SVC_ONLY"] != "svc_val" {
		t.Errorf("expected SVC_ONLY=svc_val, got '%s'", resolved["SVC_ONLY"])
	}
	if resolved["CUSTOM_VAL"] != "123" {
		t.Errorf("expected CUSTOM_VAL=123, got '%s'", resolved["CUSTOM_VAL"])
	}
	if resolved["YAML_ONLY"] != "pure_yaml" {
		t.Errorf("expected YAML_ONLY=pure_yaml, got '%s'", resolved["YAML_ONLY"])
	}
	// Precedência máxima: YAML explicitamente sobrescreve todas as camadas
	if resolved["OVERRIDDEN"] != "yaml_explicit" {
		t.Errorf("expected OVERRIDDEN=yaml_explicit, got '%s'", resolved["OVERRIDDEN"])
	}
}

func TestParseEnvFile_QuotesAndComments(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	content := `
# Comentário no início
FOO=bar
DOUBLE_QUOTED="hello world"
SINGLE_QUOTED='single quote'
WITH_COMMENT=val # Note: parser treats as val unless quoted
EMPTY_LINE=
SPACED =  spaced_val  
`
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseEnvFile(envPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed["FOO"] != "bar" {
		t.Errorf("expected FOO=bar, got '%s'", parsed["FOO"])
	}
	if parsed["DOUBLE_QUOTED"] != "hello world" {
		t.Errorf("expected DOUBLE_QUOTED='hello world', got '%s'", parsed["DOUBLE_QUOTED"])
	}
	if parsed["SINGLE_QUOTED"] != "single quote" {
		t.Errorf("expected SINGLE_QUOTED='single quote', got '%s'", parsed["SINGLE_QUOTED"])
	}
	if parsed["SPACED"] != "spaced_val" {
		t.Errorf("expected SPACED='spaced_val', got '%s'", parsed["SPACED"])
	}
}
