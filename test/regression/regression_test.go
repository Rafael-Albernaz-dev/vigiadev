package regression_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/cmd/vigiadev/commands"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/application"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/ui/tui"
	tea "github.com/charmbracelet/bubbletea"
)

// getFreeTCPPort reserva e libera uma porta TCP efêmera no loopback para evitar colisões
func getFreeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// TestRegression_UpCommand_SelectAll_CLIAndTUI valida a seleção de todos os serviços
// via flag CLI (-a, -A, --all) e via teclas 'a' e 'A' no modelo da TUI.
func TestRegression_UpCommand_SelectAll_CLIAndTUI(t *testing.T) {
	// 1. Configuração com múltiplos profiles que ocultariam serviços caso 'all' não fosse selecionado
	cfg := &domain.VigiaConfig{
		Version:     1,
		ProjectName: "atlas-select-all",
		Profiles: map[string][]string{
			"erp":         {"postgres", "erp-backend"},
			"viabilidade": {"postgres", "viabilidade-backend"},
		},
		Services: map[string]domain.ServiceConfig{
			"postgres":            {Command: []string{"postgres"}},
			"redis":               {Command: []string{"redis"}},
			"erp-backend":         {Command: []string{"erp"}, DependsOn: []string{"postgres"}},
			"viabilidade-backend": {Command: []string{"viab"}, DependsOn: []string{"postgres", "redis"}},
			"worker":              {Command: []string{"worker"}},
		},
	}

	// PARTE A: Teste da CLI / Flags (-a, -A, --all)
	// Deve resolver 100% dos 5 serviços mesmo quando profiles existirem e sem abrir prompt interativo
	for _, flag := range []string{"-a", "-A", "--all"} {
		t.Run("CLI_Flag_"+flag, func(t *testing.T) {
			rootCmd := commands.NewRootCommand()
			var outBuf, errBuf bytes.Buffer
			rootCmd.SetOut(&outBuf)
			rootCmd.SetErr(&errBuf)
			// Passamos help com a flag ou checamos se o flag está registrado no comando 'up'
			upSubCmd, _, err := rootCmd.Find([]string{"up"})
			if err != nil {
				t.Fatalf("up command not found: %v", err)
			}
			f := upSubCmd.Flags().Lookup("all")
			if f == nil {
				t.Fatalf("flag --all / -a / -A is NOT registered on upCmd")
			}
			if f.Shorthand != "a" && f.Shorthand != "A" {
				t.Errorf("expected shorthand 'a' or 'A', got %q", f.Shorthand)
			}
		})
	}

	t.Run("ResolveProfilesAndServices_AllFlag", func(t *testing.T) {
		// Quando allServices=true, nenhum prompt deve ser chamado e todos os serviços devem ser retornados
		res, err := commands.ResolveProfilesAndServicesWithAll(cfg, "", nil, strings.NewReader(""), &bytes.Buffer{}, false, true)
		if err != nil {
			t.Fatalf("unexpected error resolving with all=true: %v", err)
		}
		if len(res.Services) != 5 {
			t.Fatalf("expected all 5 services with all=true, got %d (%v)", len(res.Services), res.Services)
		}
	})

	// PARTE B: Teste da TUI (Pressionamento das teclas 'a' e 'A')
	t.Run("TUI_Keys_a_and_A_StartAllServices", func(t *testing.T) {
		bus := domain.NewEventBus()
		_, cancel := context.WithCancel(context.Background())
		defer cancel()

		var startAllCalled int
		startAllFunc := func() error {
			startAllCalled++
			return nil
		}

		serviceNames := []string{"postgres", "redis", "erp-backend", "viabilidade-backend", "worker"}
		servicePorts := map[string]int{"postgres": 5432, "erp-backend": 3000}

		model := tui.NewAppModel("atlas-select-all", serviceNames, servicePorts, bus, cancel, nil)
		model.SetStartAllFunc(startAllFunc)

		// 1. Pressionar 'a' minúsculo
		updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		model = updated.(*tui.AppModel)
		if cmd == nil {
			t.Fatal("expected non-nil tea.Cmd upon pressing 'a'")
		}
		_ = cmd()
		if startAllCalled != 1 {
			t.Fatalf("expected startAllFunc called once for 'a', got %d", startAllCalled)
		}

		// 2. Pressionar 'A' maiúsculo
		updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
		model = updated.(*tui.AppModel)
		if cmd == nil {
			t.Fatal("expected non-nil tea.Cmd upon pressing 'A'")
		}
		_ = cmd()
		if startAllCalled != 2 {
			t.Fatalf("expected startAllFunc called twice after 'A', got %d", startAllCalled)
		}
	})
}

// TestRegression_ModularMonolith_AtlasExecution valida a resolução de paths, execução
// de serviços em subdiretórios com healthcheck e encerramento limpo via PGID tanto da raiz
// quanto de dentro de subdiretórios do monolito.
func TestRegression_ModularMonolith_AtlasExecution(t *testing.T) {
	fixtureDir := t.TempDir()
	backendDir := filepath.Join(fixtureDir, "backend")
	frontendDir := filepath.Join(fixtureDir, "frontend")
	if err := os.MkdirAll(backendDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(frontendDir, 0755); err != nil {
		t.Fatal(err)
	}

	portBackend := getFreeTCPPort(t)
	portFrontend := getFreeTCPPort(t)

	// Fixture de configuração espelhando a arquitetura modular do atlas
	yamlConfig := fmt.Sprintf(`version: 1
project_name: atlas-fixture
services:
  backend-svc:
    dir: backend
    command:
      - python3
      - -m
      - http.server
      - "%d"
      - --bind
      - 127.0.0.1
    ports:
      - %d
    healthcheck:
      type: tcp
      port: %d
      retries: 30
      interval_ms: 100
  frontend-svc:
    dir: frontend
    command:
      - python3
      - -m
      - http.server
      - "%d"
      - --bind
      - 127.0.0.1
    ports:
      - %d
    depends_on:
      - backend-svc
    healthcheck:
      type: tcp
      port: %d
      retries: 30
      interval_ms: 100
`, portBackend, portBackend, portBackend, portFrontend, portFrontend, portFrontend)

	cfgFilePath := filepath.Join(fixtureDir, "vigiadev.local.yaml")
	if err := os.WriteFile(cfgFilePath, []byte(yamlConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Provar que FindConfigFile deve encontrar o arquivo ao ser invocado DE DENTRO de um subdiretório
	foundPath, err := config.FindConfigFile(backendDir, "")
	if err != nil {
		t.Fatalf("FindConfigFile from subfolder failed: %v", err)
	}
	if foundPath != cfgFilePath {
		t.Fatalf("expected config at %q, got %q", cfgFilePath, foundPath)
	}

	// 2. Provar que FindConfigFile funciona quando explicitPath é um diretório (ex: -c /path/to/atlas)
	dirPathFound, err := config.FindConfigFile(fixtureDir, fixtureDir)
	if err != nil {
		t.Fatalf("FindConfigFile with directory explicitPath failed: %v", err)
	}
	if dirPathFound != cfgFilePath {
		t.Fatalf("expected %q when passing directory as explicitPath, got %q", cfgFilePath, dirPathFound)
	}

	// 3. Execução completa do ciclo de vida: Start -> Healthcheck -> Teardown (PGID clean)
	// O Orchestrator deve utilizar o diretório da raiz do projeto (fixtureDir) mesmo se executado do subdiretório
	cfg, err := config.LoadConfig(cfgFilePath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	bus := domain.NewEventBus()
	healthyServices := make(map[string]bool)
	var healthMu sync.Mutex
	doneHealthy := make(chan struct{})
	bus.Subscribe(func(event domain.Event) {
		if sc, ok := event.(domain.ServiceStateChanged); ok {
			if sc.NewState == domain.StateHealthy {
				healthMu.Lock()
				healthyServices[sc.Service] = true
				if healthyServices["backend-svc"] && healthyServices["frontend-svc"] {
					select {
					case <-doneHealthy:
					default:
						close(doneHealthy)
					}
				}
				healthMu.Unlock()
			}
		}
	})

	orch, err := application.NewOrchestrator(cfg, fixtureDir, bus)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}
	defer orch.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- orch.Run(ctx)
	}()

	// Aguarda ambos os serviços atingirem o estado saudável via DAG
	select {
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for services in modular monolith to become healthy")
	case <-doneHealthy:
	}

	// Confirma que os processos foram gravados com seus PIDs, PGIDs e diretórios corretos
	m, err := manifest.ReadManifest(fixtureDir)
	if err != nil {
		t.Fatalf("failed to read manifest: %v", err)
	}

	backendSvc := m.Services["backend-svc"]
	frontendSvc := m.Services["frontend-svc"]

	if backendSvc.Dir != backendDir {
		t.Fatalf("expected backend dir %q, got %q", backendDir, backendSvc.Dir)
	}
	if frontendSvc.Dir != frontendDir {
		t.Fatalf("expected frontend dir %q, got %q", frontendDir, frontendSvc.Dir)
	}

	// Verifica o diretório real de execução no Linux via /proc/<pid>/cwd
	if realCwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", backendSvc.PID)); err == nil {
		if realCwd != backendDir {
			t.Fatalf("real Linux process cwd %q does not match backend directory %q", realCwd, backendDir)
		}
	}
	if realCwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", frontendSvc.PID)); err == nil {
		if realCwd != frontendDir {
			t.Fatalf("real Linux process cwd %q does not match frontend directory %q", realCwd, frontendDir)
		}
	}

	backendPGID := backendSvc.PGID
	frontendPGID := frontendSvc.PGID

	// Valida que os processos realmente estão respondendo
	if err := syscall.Kill(-backendPGID, 0); err != nil {
		t.Fatalf("backend process group %d is not running: %v", backendPGID, err)
	}
	if err := syscall.Kill(-frontendPGID, 0); err != nil {
		t.Fatalf("frontend process group %d is not running: %v", frontendPGID, err)
	}

	// 4. Teardown gracioso: cancela o contexto e aguarda encerramento
	cancel()
	select {
	case err := <-errChan:
		if err != nil && err != context.Canceled {
			t.Fatalf("orchestrator exited with unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("orchestrator failed to terminate in time after context cancellation")
	}

	// Garante que o PGID de cada serviço foi totalmente limpo no Linux (não virou zumbi/órfão)
	time.Sleep(300 * time.Millisecond)
	if err := syscall.Kill(-backendPGID, 0); err == nil {
		_ = syscall.Kill(-backendPGID, syscall.SIGKILL)
		t.Fatalf("backend process group %d still lingering after orchestrator termination", backendPGID)
	}
	if err := syscall.Kill(-frontendPGID, 0); err == nil {
		_ = syscall.Kill(-frontendPGID, syscall.SIGKILL)
		t.Fatalf("frontend process group %d still lingering after orchestrator termination", frontendPGID)
	}
}

// TestRegression_UpCommand_ActiveSessionHandling reproduz o cenário onde já existe uma
// sessão ativa do vigiadev no diretório (como no atlas) e o usuário executa 'vigiadev up'
// selecionando 'A' (All services). Deve detectar e parar a sessão anterior quando autorizado
// em modo interativo ou via --force, sem falhar com "prior vigiadev session with PID ... is still active".
func TestRegression_UpCommand_ActiveSessionHandling(t *testing.T) {
	dir := t.TempDir()

	// Inicia um processo dummy simulando um supervisor anterior ativo e um serviço filho
	ownerCmd := exec.Command("sh", "-c", "sleep 30")
	ownerCmd.Dir = dir
	ownerCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := ownerCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ownerCmd.Process.Kill(); _ = ownerCmd.Wait() })

	svcCmd := exec.Command("sh", "-c", "sleep 30")
	svcCmd.Dir = dir
	svcCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := svcCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svcCmd.Process.Kill(); _ = svcCmd.Wait() })

	m := &manifest.RunManifest{
		RunID:       "run-prior-atlas",
		ProjectName: "atlas",
		StartTime:   time.Now().Add(-10 * time.Minute),
		Services: map[string]manifest.ServiceManifest{
			"erp-backend": {
				Name: "erp-backend",
				PID:  svcCmd.Process.Pid,
				PGID: svcCmd.Process.Pid,
				Port: 3000,
				Dir:  dir,
			},
		},
	}
	if err := manifest.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.VigiaDir, manifest.LockFile), []byte(strconv.Itoa(ownerCmd.Process.Pid)), 0644); err != nil {
		t.Fatal(err)
	}

	// Simula o input do usuário no terminal interativo:
	// 1ª linha: "Y\n" para parar a sessão ativa anterior
	// 2ª linha: "A\n" para selecionar [A] All services no prompt de profiles
	userInput := strings.NewReader("Y\nA\n")
	var outBuf bytes.Buffer

	proceed, err := commands.HandleOrReapPriorSession(dir, "atlas", userInput, &outBuf, true, false)
	if err != nil {
		t.Fatalf("HandleOrReapPriorSession failed: %v", err)
	}
	if !proceed {
		t.Fatal("expected proceed=true after confirming session replacement")
	}

	// Verifica que o manifesto e lock anteriores foram limpos
	if _, err := manifest.ReadManifest(dir); !os.IsNotExist(err) {
		t.Fatalf("expected prior manifest to be removed, got err=%v", err)
	}

	// Em seguida, o mesmo reader de stdin deve ler "A\n" no seletor de profiles sem perder buffer
	cfg := &domain.VigiaConfig{
		Version:     1,
		ProjectName: "atlas",
		Profiles: map[string][]string{
			"erp":      {"postgres", "erp-backend"},
			"platform": {"postgres", "platform-backend"},
		},
		Services: map[string]domain.ServiceConfig{
			"postgres":         {Command: []string{"postgres"}},
			"erp-backend":      {Command: []string{"erp"}, DependsOn: []string{"postgres"}},
			"platform-backend": {Command: []string{"plat"}, DependsOn: []string{"postgres"}},
		},
	}

	resolvedCfg, err := commands.ResolveProfilesAndServicesWithAll(cfg, "", nil, userInput, &outBuf, true, false)
	if err != nil {
		t.Fatalf("unexpected error in ResolveProfilesAndServicesWithAll: %v", err)
	}
	if len(resolvedCfg.Services) != 3 {
		t.Fatalf("expected all 3 services after selecting 'A', got %d", len(resolvedCfg.Services))
	}
}

// TestRegression_StatusCommand_DetectsActiveSessionAndPortConflicts valida o novo comando
// 'vigiadev status' (e alias 'ps') identificando se o ambiente já está 'up' e reportando
// conflitos de porta antes de iniciar.
func TestRegression_StatusCommand_DetectsActiveSessionAndPortConflicts(t *testing.T) {
	dir := t.TempDir()
	port := getFreeTCPPort(t)

	yamlContent := fmt.Sprintf(`version: 1
project_name: atlas-status
services:
  erp-backend:
    command: ["sh", "-c", "sleep 10"]
    ports: [%d]
    port_policy: remap
`, port)
	cfgPath := filepath.Join(dir, "vigiadev.local.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Estado inicial limpo (STOPPED e porta livre)
	var out1 bytes.Buffer
	if err := commands.InspectProjectStatus(dir, cfgPath, &out1); err != nil {
		t.Fatalf("InspectProjectStatus failed on clean state: %v", err)
	}
	s1 := out1.String()
	if !strings.Contains(s1, "STOPPED") || !strings.Contains(s1, "free") {
		t.Fatalf("expected STOPPED and free port in status output, got:\n%s", s1)
	}

	// 2. Simula um processo externo ocupando a porta antes de subir o vigiadev (Conflito de Porta)
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var out2 bytes.Buffer
	if err := commands.InspectProjectStatus(dir, cfgPath, &out2); err != nil {
		t.Fatalf("InspectProjectStatus failed on conflict state: %v", err)
	}
	s2 := out2.String()
	if !strings.Contains(s2, "occupied") {
		t.Fatalf("expected status to report port %d as occupied, got:\n%s", port, s2)
	}

	// 3. Simula uma sessão ativa do vigiadev (ACTIVE)
	m := &manifest.RunManifest{
		RunID:       "run-active-123",
		ProjectName: "atlas-status",
		StartTime:   time.Now().Add(-2 * time.Minute),
		Services: map[string]manifest.ServiceManifest{
			"erp-backend": {
				Name: "erp-backend",
				PID:  os.Getpid(),
				PGID: os.Getpid(),
				Port: port,
				Dir:  dir,
			},
		},
	}
	if err := manifest.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.VigiaDir, manifest.LockFile), []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		t.Fatal(err)
	}

	var out3 bytes.Buffer
	if err := commands.InspectProjectStatus(dir, cfgPath, &out3); err != nil {
		t.Fatalf("InspectProjectStatus failed on active session: %v", err)
	}
	s3 := out3.String()
	if !strings.Contains(s3, "ACTIVE") || !strings.Contains(s3, "run-active-123") {
		t.Fatalf("expected status to report ACTIVE session and run ID, got:\n%s", s3)
	}
}

