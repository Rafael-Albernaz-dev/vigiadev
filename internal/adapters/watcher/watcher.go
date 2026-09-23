package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/fsnotify/fsnotify"
)

// DefaultIgnoredDirs lista diretórios pesados ou transitórios ignorados por padrão.
var DefaultIgnoredDirs = []string{
	".git",
	"node_modules",
	"dist",
	"build",
	".vigiadev",
	"__pycache__",
	".venv",
	"venv",
	"env",
	".next",
	".nuxt",
	".cache",
	".tmp",
	"tmp",
}

// DefaultIgnoredExts lista extensões de arquivos efêmeros ignorados por padrão.
var DefaultIgnoredExts = []string{
	".log",
	".tmp",
	".swp",
	".swo",
	"~",
}

// ReloadCallback é acionado após a janela de debounce quando arquivos são modificados.
type ReloadCallback func(serviceName string, changedPath string)

type serviceWatchState struct {
	name        string
	debounce    time.Duration
	timer       *time.Timer
	lastPath    string
	ignorePaths []string
	mu          sync.Mutex
}

// ServiceWatcher supervisiona o sistema de arquivos e dispara hot-reload com debounce.
type ServiceWatcher struct {
	workDir  string
	watcher  *fsnotify.Watcher
	callback ReloadCallback
	services map[string]*serviceWatchState
	watched  map[string]map[string]bool // dir -> set of services
	mu       sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewServiceWatcher inicializa o monitor de arquivos assíncrono.
func NewServiceWatcher(workDir string, callback ReloadCallback) (*ServiceWatcher, error) {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		absWorkDir = workDir
	}

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize fsnotify watcher: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	sw := &ServiceWatcher{
		workDir:  absWorkDir,
		watcher:  fw,
		callback: callback,
		services: make(map[string]*serviceWatchState),
		watched:  make(map[string]map[string]bool),
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}

	go sw.eventLoop()

	return sw, nil
}

// WatchService registra e adiciona os caminhos observados para um serviço.
func (sw *ServiceWatcher) WatchService(serviceName string, cfg domain.ServiceConfig) error {
	// Se o serviço não habilitou watch nem declarou caminhos, nada a observar
	if !cfg.Watch && len(cfg.WatchPaths) == 0 {
		return nil
	}

	debounceMs := cfg.DebounceMs
	if debounceMs <= 0 {
		debounceMs = 300
	}

	state := &serviceWatchState{
		name:        serviceName,
		debounce:    time.Duration(debounceMs) * time.Millisecond,
		ignorePaths: cfg.IgnorePaths,
	}

	sw.mu.Lock()
	sw.services[serviceName] = state
	sw.mu.Unlock()

	targets := cfg.WatchPaths
	if len(targets) == 0 && cfg.Watch {
		targets = []string{"."}
	}

	for _, target := range targets {
		resolved := target
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(sw.workDir, target)
		}

		info, err := os.Stat(resolved)
		if err != nil {
			// Se o caminho não existe ainda, ignora silenciosamente ou aguarda criação
			continue
		}

		if info.IsDir() {
			if err := sw.addRecursive(resolved, serviceName, state); err != nil {
				return err
			}
		} else {
			dir := filepath.Dir(resolved)
			sw.addDir(dir, serviceName)
		}
	}

	return nil
}

func (sw *ServiceWatcher) addDir(dir string, serviceName string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	if _, exists := sw.watched[dir]; !exists {
		sw.watched[dir] = make(map[string]bool)
		_ = sw.watcher.Add(dir)
	}
	sw.watched[dir][serviceName] = true
}

func (sw *ServiceWatcher) addRecursive(root string, serviceName string, state *serviceWatchState) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			base := filepath.Base(path)
			// Pula diretórios padrão ignorados
			for _, ignored := range DefaultIgnoredDirs {
				if base == ignored {
					return filepath.SkipDir
				}
			}

			// Pula ignore_paths customizados
			for _, ignored := range state.ignorePaths {
				rel, _ := filepath.Rel(sw.workDir, path)
				if rel == ignored || strings.HasPrefix(rel, ignored+string(filepath.Separator)) {
					return filepath.SkipDir
				}
			}

			sw.addDir(path, serviceName)
		}
		return nil
	})
}

func (sw *ServiceWatcher) eventLoop() {
	defer close(sw.done)

	for {
		select {
		case <-sw.ctx.Done():
			return

		case err, ok := <-sw.watcher.Errors:
			if !ok {
				return
			}
			_ = err

		case event, ok := <-sw.watcher.Events:
			if !ok {
				return
			}

			// Apenas eventos de modificação, criação, remoção ou renomeação de conteúdo
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}

			// Se for arquivo ou extensão temporária ignorada, pula
			if sw.isIgnoredFile(event.Name) {
				continue
			}

			// Se for novo diretório criado, adiciona recursivamente aos watchers
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					dir := filepath.Dir(event.Name)
					sw.mu.RLock()
					services := sw.watched[dir]
					sw.mu.RUnlock()

					for svc := range services {
						sw.mu.RLock()
						state := sw.services[svc]
						sw.mu.RUnlock()
						if state != nil {
							_ = sw.addRecursive(event.Name, svc, state)
						}
					}
				}
			}

			// Identifica quais serviços observam este diretório
			dir := filepath.Dir(event.Name)
			sw.mu.RLock()
			services := make([]string, 0)
			for svc := range sw.watched[dir] {
				services = append(services, svc)
			}
			sw.mu.RUnlock()

			for _, svc := range services {
				sw.triggerDebounced(svc, event.Name)
			}
		}
	}
}

func (sw *ServiceWatcher) triggerDebounced(serviceName string, changedPath string) {
	sw.mu.RLock()
	state, exists := sw.services[serviceName]
	sw.mu.RUnlock()

	if !exists || state == nil {
		return
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	state.lastPath = changedPath

	if state.timer != nil {
		state.timer.Stop()
	}

	state.timer = time.AfterFunc(state.debounce, func() {
		state.mu.Lock()
		path := state.lastPath
		state.mu.Unlock()

		if sw.callback != nil {
			sw.callback(serviceName, path)
		}
	})
}

func (sw *ServiceWatcher) isIgnoredFile(filePath string) bool {
	base := filepath.Base(filePath)

	// Arquivos ocultos
	if strings.HasPrefix(base, ".") && base != "." && base != ".." {
		return true
	}

	// Extensões ignoradas
	for _, ext := range DefaultIgnoredExts {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}

	// Diretórios ignorados relativos ao workDir
	rel, err := filepath.Rel(sw.workDir, filePath)
	if err == nil {
		parts := strings.Split(rel, string(filepath.Separator))
		for _, part := range parts {
			for _, ignored := range DefaultIgnoredDirs {
				if part == ignored {
					return true
				}
			}
		}
	}

	return false
}

// Close encerra todos os observadores e timers ativos.
func (sw *ServiceWatcher) Close() error {
	sw.cancel()
	err := sw.watcher.Close()

	sw.mu.Lock()
	for _, state := range sw.services {
		state.mu.Lock()
		if state.timer != nil {
			state.timer.Stop()
		}
		state.mu.Unlock()
	}
	sw.mu.Unlock()

	<-sw.done
	return err
}
