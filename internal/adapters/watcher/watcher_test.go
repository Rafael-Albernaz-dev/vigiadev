package watcher_test

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/watcher"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestServiceWatcher_DebounceAndTrigger(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	var triggerCount int32
	var mu sync.Mutex
	var lastChangedService string
	var lastChangedFile string

	sw, err := watcher.NewServiceWatcher(tempDir, func(serviceName string, changedPath string) {
		atomic.AddInt32(&triggerCount, 1)
		mu.Lock()
		lastChangedService = serviceName
		lastChangedFile = changedPath
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("failed to create ServiceWatcher: %v", err)
	}
	defer sw.Close()

	cfg := domain.ServiceConfig{
		Watch:      true,
		WatchPaths: []string{"src"},
		DebounceMs: 50,
	}

	if err := sw.WatchService("api", cfg); err != nil {
		t.Fatalf("failed to watch service: %v", err)
	}

	// Aguarda estabilização do fsnotify
	time.Sleep(50 * time.Millisecond)

	// Simula 4 alterações rápidas (rajada de escrita de IDE)
	for i := 0; i < 4; i++ {
		_ = os.WriteFile(testFile, []byte("package main // update"), 0644)
		time.Sleep(10 * time.Millisecond)
	}

	// Aguarda tempo suficiente para o debounce de 50ms disparar
	time.Sleep(120 * time.Millisecond)

	count := atomic.LoadInt32(&triggerCount)
	if count != 1 {
		t.Errorf("esperava exatamente 1 disparo por debounce, obteve %d", count)
	}

	mu.Lock()
	defer mu.Unlock()
	if lastChangedService != "api" {
		t.Errorf("esperava serviço 'api', obteve '%s'", lastChangedService)
	}
	if !filepath.IsAbs(lastChangedFile) && filepath.Base(lastChangedFile) != "main.go" {
		t.Errorf("esperava arquivo alterado 'main.go', obteve '%s'", lastChangedFile)
	}
}

func TestServiceWatcher_IgnoresDefaultDirectories(t *testing.T) {
	tempDir := t.TempDir()
	nodeModules := filepath.Join(tempDir, "node_modules", "pkg")
	gitDir := filepath.Join(tempDir, ".git", "objects")
	_ = os.MkdirAll(nodeModules, 0755)
	_ = os.MkdirAll(gitDir, 0755)

	var triggerCount int32

	sw, err := watcher.NewServiceWatcher(tempDir, func(serviceName string, changedPath string) {
		atomic.AddInt32(&triggerCount, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sw.Close()

	cfg := domain.ServiceConfig{
		Watch:      true,
		DebounceMs: 30,
	}
	if err := sw.WatchService("backend", cfg); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	// Escreve em node_modules e .git
	_ = os.WriteFile(filepath.Join(nodeModules, "index.js"), []byte("module.exports={}"), 0644)
	_ = os.WriteFile(filepath.Join(gitDir, "commit"), []byte("commit-data"), 0644)

	time.Sleep(80 * time.Millisecond)

	if count := atomic.LoadInt32(&triggerCount); count != 0 {
		t.Errorf("esperava 0 disparos para diretórios ignorados, obteve %d", count)
	}
}

func TestServiceWatcher_CustomIgnorePaths(t *testing.T) {
	tempDir := t.TempDir()
	docsDir := filepath.Join(tempDir, "docs")
	_ = os.MkdirAll(docsDir, 0755)

	var triggerCount int32

	sw, err := watcher.NewServiceWatcher(tempDir, func(serviceName string, changedPath string) {
		atomic.AddInt32(&triggerCount, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sw.Close()

	cfg := domain.ServiceConfig{
		Watch:       true,
		IgnorePaths: []string{"docs"},
		DebounceMs:  30,
	}
	if err := sw.WatchService("backend", cfg); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	_ = os.WriteFile(filepath.Join(docsDir, "README.md"), []byte("docs update"), 0644)
	time.Sleep(80 * time.Millisecond)

	if count := atomic.LoadInt32(&triggerCount); count != 0 {
		t.Errorf("esperava 0 disparos para ignore_paths customizados, obteve %d", count)
	}
}
