package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

const (
	VigiaDir     = ".vigiadev"
	RunFile      = "run.json"
	LockFile     = "vigiadev.lock"
)

// ServiceManifest armazena metadados de processo para rastreamento estrito de posse.
type ServiceManifest struct {
	Name        string `json:"name"`
	PID         int    `json:"pid,omitempty"`
	PGID        int    `json:"pgid,omitempty"`
	Port        int    `json:"port,omitempty"`
	Dir         string `json:"dir,omitempty"`
	ContainerID string `json:"container_id,omitempty"`
	IsContainer bool   `json:"is_container,omitempty"`
}

// RunManifest representa o estado persistido da sessão ativa no disco (.vigiadev/run.json).
type RunManifest struct {
	RunID       string                     `json:"run_id"`
	ProjectName string                     `json:"project_name"`
	StartTime   time.Time                  `json:"start_time"`
	Services    map[string]ServiceManifest `json:"services"`
	Containers  []string                   `json:"containers,omitempty"`
}

// EnsureVigiaDir garante que o diretório .vigiadev exista no caminho do projeto.
func EnsureVigiaDir(workDir string) (string, error) {
	dir := filepath.Join(workDir, VigiaDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("falha ao criar pasta .vigiadev: %w", err)
	}
	return dir, nil
}

// WriteManifest serializa o manifesto em .vigiadev/run.json.
func WriteManifest(workDir string, m *RunManifest) error {
	dir, err := EnsureVigiaDir(workDir)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("falha ao serializar manifesto: %w", err)
	}

	path := filepath.Join(dir, RunFile)
	return os.WriteFile(path, data, 0644)
}

// ReadManifest lê o manifesto ativo em .vigiadev/run.json.
func ReadManifest(workDir string) (*RunManifest, error) {
	path := filepath.Join(workDir, VigiaDir, RunFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var m RunManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifesto corrompido: %w", err)
	}

	return &m, nil
}

// RemoveManifest remove o arquivo de manifesto após o teardown gracioso.
func RemoveManifest(workDir string) error {
	path := filepath.Join(workDir, VigiaDir, RunFile)
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// IsPIDAlive verifica se um PID existe na tabela de processos do sistema operacional e não está em estado zumbi.
func IsPIDAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	if statData, readErr := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); readErr == nil {
		s := string(statData)
		if idx := strings.LastIndex(s, ")"); idx != -1 && idx+2 < len(s) {
			state := s[idx+2]
			if state == 'Z' || state == 'X' {
				return false
			}
		}
	}
	return true
}

// ReadLockPID lê o PID gravado em .vigiadev/vigiadev.lock e informa se o processo ainda está ativo.
func ReadLockPID(workDir string) (int, bool) {
	lockPath := filepath.Join(workDir, VigiaDir, LockFile)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return 0, false
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if parseErr != nil || pid <= 1 {
		return 0, false
	}
	return pid, IsPIDAlive(pid)
}

// AcquireLock tenta obter a trava de execução exclusiva em .vigiadev/vigiadev.lock.
// Implementa recuperação determinística de trava órfã (stale lock).
func AcquireLock(workDir string) error {
	dir, err := EnsureVigiaDir(workDir)
	if err != nil {
		return err
	}

	lockPath := filepath.Join(dir, LockFile)

	// Se o arquivo de lock já existir, verifica a saúde do processo que o criou
	if pid, alive := ReadLockPID(workDir); alive {
		return fmt.Errorf("%w: processo com PID %d ainda está ativo", domain.ErrSessionLocked, pid)
	}

	currentPID := strconv.Itoa(os.Getpid())
	return os.WriteFile(lockPath, []byte(currentPID), 0644)
}

// ReleaseLock remove a trava ao encerrar a aplicação.
func ReleaseLock(workDir string) error {
	path := filepath.Join(workDir, VigiaDir, LockFile)
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// CleanVigiaDir remove todo o diretório .vigiadev do projeto.
func CleanVigiaDir(workDir string) error {
	dir := filepath.Join(workDir, VigiaDir)
	return os.RemoveAll(dir)
}
