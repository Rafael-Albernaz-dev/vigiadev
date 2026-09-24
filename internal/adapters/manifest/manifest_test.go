package manifest_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestManifest_Lifecycle(t *testing.T) {
	tempDir := t.TempDir()

	m := &manifest.RunManifest{
		RunID:       "run-1234",
		ProjectName: "meu-projeto",
		StartTime:   time.Now().Truncate(time.Second),
		Services: map[string]manifest.ServiceManifest{
			"web": {
				Name: "web",
				PID:  1001,
				PGID: 1001,
				Port: 8000,
			},
		},
	}

	// 1. Escrita
	if err := manifest.WriteManifest(tempDir, m); err != nil {
		t.Fatalf("falha ao gravar manifesto: %v", err)
	}

	// 2. Leitura
	read, err := manifest.ReadManifest(tempDir)
	if err != nil {
		t.Fatalf("falha ao ler manifesto: %v", err)
	}

	if read.RunID != "run-1234" || read.ProjectName != "meu-projeto" {
		t.Fatalf("dados do manifesto não batem: %+v", read)
	}

	svc := read.Services["web"]
	if svc.PID != 1001 || svc.Port != 8000 {
		t.Fatalf("dados do serviço não batem: %+v", svc)
	}

	// 3. Remoção
	if err := manifest.RemoveManifest(tempDir); err != nil {
		t.Fatalf("falha ao remover manifesto: %v", err)
	}

	// 4. Leitura após remoção deve falhar
	if _, err := manifest.ReadManifest(tempDir); err == nil {
		t.Fatal("esperava erro ao ler manifesto inexistente")
	}
}

func TestLock_LifecycleAndStaleRecovery(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Obter lock
	if err := manifest.AcquireLock(tempDir); err != nil {
		t.Fatalf("falha ao adquirir lock inicial: %v", err)
	}

	// 2. Tentar obter novamente (mesmo processo ativo) deve falhar com ErrSessionLocked
	err := manifest.AcquireLock(tempDir)
	if err == nil {
		t.Fatal("esperava erro de sessão bloqueada")
	}
	if !errors.Is(err, domain.ErrSessionLocked) {
		t.Fatalf("esperava ErrSessionLocked, obteve: %v", err)
	}

	// 3. Liberar lock
	if err := manifest.ReleaseLock(tempDir); err != nil {
		t.Fatalf("falha ao liberar lock: %v", err)
	}

	// 4. Simulação de Stale Lock (PID inexistente: 9999999)
	lockPath := filepath.Join(tempDir, manifest.VigiaDir, manifest.LockFile)
	if err := os.WriteFile(lockPath, []byte("9999999"), 0644); err != nil {
		t.Fatal(err)
	}

	// Deve recuperar o lock órfão com sucesso
	if err := manifest.AcquireLock(tempDir); err != nil {
		t.Fatalf("falha ao recuperar stale lock: %v", err)
	}
	_ = manifest.ReleaseLock(tempDir)
}

func TestCleanVigiaDir(t *testing.T) {
	tempDir := t.TempDir()
	vigiaDir, err := manifest.EnsureVigiaDir(tempDir)
	if err != nil {
		t.Fatalf("falha ao criar pasta .vigiadev: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vigiaDir, "dummy.txt"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := manifest.CleanVigiaDir(tempDir); err != nil {
		t.Fatalf("falha ao limpar .vigiadev: %v", err)
	}

	if _, err := os.Stat(vigiaDir); !os.IsNotExist(err) {
		t.Fatalf("esperava que pasta .vigiadev tivesse sido removida")
	}
}
