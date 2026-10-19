package manifest_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestManifest_AtomicReplacementPreservesOpenReaderAndMode(t *testing.T) {
	dir := t.TempDir()
	if err := manifest.WriteManifest(dir, &manifest.RunManifest{RunID: "old"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, manifest.VigiaDir, manifest.RunFile)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := manifest.WriteManifest(dir, &manifest.RunManifest{RunID: "new"}); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var previous manifest.RunManifest
	if err := json.Unmarshal(data, &previous); err != nil || previous.RunID != "old" {
		t.Fatalf("open reader lost its complete previous snapshot: run=%q err=%v", previous.RunID, err)
	}
	current, err := manifest.ReadManifest(dir)
	if err != nil || current.RunID != "new" {
		t.Fatalf("replacement not visible to new readers: %+v, %v", current, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("private manifest permissions changed: %o", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files left after replacement: %v, %v", entries, err)
	}
}

func TestManifest_ConcurrentReadersSeeCompleteSnapshots(t *testing.T) {
	dir := t.TempDir()
	snapshot := func(n int) *manifest.RunManifest {
		return &manifest.RunManifest{
			RunID: fmt.Sprint(n), ProjectName: "atomic-manifest",
			Services: map[string]manifest.ServiceManifest{
				"web": {Name: "web", PID: 1000 + n, PGID: 1000 + n, Dir: strings.Repeat("owned/", 2048)},
			},
		}
	}
	if err := manifest.WriteManifest(dir, snapshot(0)); err != nil {
		t.Fatal(err)
	}
	start, done := make(chan struct{}), make(chan struct{})
	errors := make(chan error, 6)
	var writers, readers sync.WaitGroup
	var reads atomic.Int64
	for writer := 0; writer < 2; writer++ {
		writers.Add(1)
		go func(base int) {
			defer writers.Done()
			<-start
			for n := 1; n <= 32; n++ {
				if err := manifest.WriteManifest(dir, snapshot(base+n)); err != nil {
					errors <- err
					return
				}
			}
		}(writer * 32)
	}
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for {
				current, err := manifest.ReadManifest(dir)
				if err != nil {
					errors <- fmt.Errorf("concurrent reader: %w", err)
					return
				}
				svc := current.Services["web"]
				if current.ProjectName != "atomic-manifest" || current.RunID != fmt.Sprint(svc.PID-1000) || svc.PGID != svc.PID || len(svc.Dir) != 6*2048 {
					errors <- fmt.Errorf("inconsistent snapshot: run=%s pid=%d pgid=%d", current.RunID, svc.PID, svc.PGID)
					return
				}
				reads.Add(1)
				select {
				case <-done:
					return
				default:
				}
			}
		}()
	}
	close(start)
	writers.Wait()
	close(done)
	readers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if reads.Load() == 0 {
		t.Fatal("no complete snapshot was read")
	}
}

func TestManifest_WriteFailureCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, manifest.VigiaDir, manifest.RunFile)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "preserve")
	if err := os.WriteFile(marker, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manifest.WriteManifest(dir, &manifest.RunManifest{RunID: "new"}); err == nil {
		t.Fatal("expected failure replacing a directory with a manifest file")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "untouched" {
		t.Fatalf("failed write damaged existing target: %q, %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != manifest.RunFile {
		t.Fatalf("failed write left temporary files: %v, %v", entries, err)
	}
}

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
