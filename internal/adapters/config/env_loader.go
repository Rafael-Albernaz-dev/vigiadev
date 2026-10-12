package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

// ParseEnvFile lê um arquivo .env e extrai as variáveis chave-valor.
// Retorna um mapa vazio se o arquivo não existir.
func ParseEnvFile(filePath string) (map[string]string, error) {
	result := make(map[string]string)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, fmt.Errorf("failed to read env file '%s': %w", filePath, err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		idx := strings.Index(line, "=")
		if idx == -1 {
			continue
		}

		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])

		// Trata aspas duplas e simples
		if len(val) >= 2 {
			if strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"") {
				val = val[1 : len(val)-1]
				val = strings.ReplaceAll(val, "\\n", "\n")
				val = strings.ReplaceAll(val, "\\\"", "\"")
			} else if strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'") {
				val = val[1 : len(val)-1]
			}
		}

		if key != "" {
			result[key] = val
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan env file '%s': %w", filePath, err)
	}

	return result, nil
}

// ResolveLayeredEnv computa as variáveis de ambiente de um serviço mesclando
// as camadas conforme a precedência rigorosa definida em DEC-033:
// 1. .env na raiz (base)
// 2. .env.local na raiz (sobrescreve .env raiz)
// 3. <dir>/.env do serviço (se dir configurado)
// 4. <dir>/.env.local do serviço (se dir configurado)
// 5. env_file explicitamente declarado no serviço
// 6. env declarado diretamente no serviço no YAML (precedência máxima)
func ResolveLayeredEnv(baseDir string, service domain.ServiceConfig) (map[string]string, error) {
	merged := make(map[string]string)

	applyFile := func(path string) error {
		if path == "" {
			return nil
		}
		vars, err := ParseEnvFile(path)
		if err != nil {
			return err
		}
		for k, v := range vars {
			merged[k] = v
		}
		return nil
	}

	// Camada 1: .env na raiz
	if err := applyFile(filepath.Join(baseDir, ".env")); err != nil {
		return nil, err
	}

	// Camada 2: .env.local na raiz
	if err := applyFile(filepath.Join(baseDir, ".env.local")); err != nil {
		return nil, err
	}

	// Camadas 3 e 4: .env e .env.local no dir do serviço
	if service.Dir != "" {
		svcDir := service.Dir
		if !filepath.IsAbs(svcDir) {
			svcDir = filepath.Join(baseDir, svcDir)
		}
		if err := applyFile(filepath.Join(svcDir, ".env")); err != nil {
			return nil, err
		}
		if err := applyFile(filepath.Join(svcDir, ".env.local")); err != nil {
			return nil, err
		}
	}

	// Camada 5: env_file declarado explicitamente
	if service.EnvFile != nil {
		var envFiles []string
		switch v := service.EnvFile.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				envFiles = append(envFiles, strings.TrimSpace(v))
			}
		case []string:
			envFiles = append(envFiles, v...)
		case []interface{}:
			for _, item := range v {
				if s := fmt.Sprint(item); strings.TrimSpace(s) != "" {
					envFiles = append(envFiles, strings.TrimSpace(s))
				}
			}
		}

		for _, rel := range envFiles {
			var candidate string
			if filepath.IsAbs(rel) {
				candidate = rel
			} else {
				// Tenta relativo ao dir do serviço primeiro se existir
				if service.Dir != "" {
					dirCandidate := filepath.Join(baseDir, service.Dir, rel)
					if _, err := os.Stat(dirCandidate); err == nil {
						candidate = dirCandidate
					}
				}
				if candidate == "" {
					candidate = filepath.Join(baseDir, rel)
				}
			}

			if err := applyFile(candidate); err != nil {
				return nil, err
			}
		}
	}

	// Camada 6: env declarado diretamente no YAML (precedência máxima)
	for k, v := range service.Env {
		merged[k] = v
	}

	return merged, nil
}
