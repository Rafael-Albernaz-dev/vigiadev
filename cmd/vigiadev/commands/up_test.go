package commands_test

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/cmd/vigiadev/commands"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ports"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestPortConflictListenerHelper(t *testing.T) {
	if os.Getenv("VIGIA_CONFLICT_HELPER") != "1" {
		return
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "0"
	}
	l, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		panic(err)
	}
	fmt.Println(l.Addr().(*net.TCPAddr).Port)
	for {
		c, err := l.Accept()
		if err != nil {
			panic(err)
		}
		c.Close()
	}
}

func conflictChild(t *testing.T) (*exec.Cmd, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPortConflictListenerHelper$")
	cmd.Env = append(os.Environ(), "VIGIA_CONFLICT_HELPER=1", "PORT=0")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	return cmd, port
}

func conflictConfig(port int) *domain.VigiaConfig {
	return &domain.VigiaConfig{Version: 1, ProjectName: "test", Services: map[string]domain.ServiceConfig{
		"web": {Ports: []int{port}, Command: []string{"server", "{port}"}, Env: map[string]string{"URL": "http://localhost:{port}", "PORT": "old"}, HealthCheck: &domain.HealthCheckConfig{Port: port, URL: "http://localhost:{port}", Command: []string{"check", "{port}"}}},
	}}
}

func TestUp_PortConflictResolution(t *testing.T) {
	_, port := conflictChild(t)
	cfg := conflictConfig(port)
	var output bytes.Buffer
	_, err := commands.PreflightPorts(cfg, strings.NewReader("yes\n"), &output, true, false, false)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("port %d is occupied", port)) {
		t.Fatalf("expected a clear conflict: %v", err)
	}
	if output.Len() != 0 || ports.NewPortResolver("").IsPortAvailable(port) {
		t.Fatalf("zero-flag preflight prompted or killed listener: %q", &output)
	}
}

func TestPreflightPorts_ReusePolicy(t *testing.T) {
	_, port := conflictChild(t)
	cfg := conflictConfig(port)
	svc := cfg.Services["web"]
	svc.PortPolicy = domain.PortPolicyReuse
	svc.ComposeService = "db"
	cfg.Services["web"] = svc
	var out bytes.Buffer
	events, err := commands.PreflightPorts(cfg, strings.NewReader(""), &out, false, false, false)
	if err != nil || len(events) != 0 || out.Len() != 0 || cfg.Services["web"].Ports[0] != port || cfg.Services["web"].PortPolicy != domain.PortPolicyReuse {
		t.Fatalf("reuse changed: events=%v err=%v output=%q service=%+v", events, err, &out, cfg.Services["web"])
	}
}

func TestPreflightPorts_AutoRemapZeroFlags(t *testing.T) {
	_, port := conflictChild(t)
	cfg := conflictConfig(port)
	svc := cfg.Services["web"]
	svc.PortPolicy = domain.PortPolicyRemap
	cfg.Services["web"] = svc
	var out bytes.Buffer
	events, err := commands.PreflightPorts(cfg, strings.NewReader(""), &out, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Services["web"]
	if out.Len() != 0 || len(events) != 1 || got.Ports[0] <= port || got.Env["PORT"] != strconv.Itoa(got.Ports[0]) || got.Command[1] != strconv.Itoa(got.Ports[0]) || got.HealthCheck.Port != got.Ports[0] || got.HealthCheck.Command[1] != strconv.Itoa(got.Ports[0]) || !strings.HasSuffix(got.HealthCheck.URL, strconv.Itoa(got.Ports[0])) {
		t.Fatalf("automatic remap incomplete: events=%v output=%q service=%+v", events, &out, got)
	}
	if ports.NewPortResolver("").IsPortAvailable(port) {
		t.Fatal("original listener was terminated")
	}
}

func TestUp_ReapOrphanedProcesses(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPortConflictListenerHelper$")
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "VIGIA_CONFLICT_HELPER=1", "PORT=0")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	m := &manifest.RunManifest{RunID: "old-run", ProjectName: "test", Services: map[string]manifest.ServiceManifest{
		"web": {Name: "web", PID: cmd.Process.Pid, PGID: cmd.Process.Pid, Port: port},
	}}
	if err := manifest.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.VigiaDir, manifest.LockFile), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	if err := commands.ReapOrphanedProcesses(dir, "test"); err == nil || ports.NewPortResolver("").IsPortAvailable(port) {
		t.Fatalf("active session was not protected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.VigiaDir, manifest.LockFile), []byte("99999999"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := commands.ReapOrphanedProcesses(dir, "test"); err != nil {
		t.Fatal(err)
	}
	if !ports.NewPortResolver("").IsPortAvailable(port) {
		t.Fatal("orphaned listener remains active")
	}
	if _, err := manifest.ReadManifest(dir); !os.IsNotExist(err) {
		t.Fatalf("stale manifest remains: %v", err)
	}
}

func TestUp_KillPortsFlag(t *testing.T) {
	for _, tc := range []struct {
		name, input                        string
		interactive, force, kill, wantKill bool
	}{
		{"confirmed", "yes\n", true, false, true, true}, {"declined", "n\n", true, false, true, false}, {"default", "\n", true, false, true, false},
		{"force", "", true, true, true, true}, {"ci", "", false, false, true, true}, {"force-alone", "", true, true, false, false}, {"ci-no-kill", "", false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child, port := conflictChild(t)
			var out bytes.Buffer
			_, err := commands.PreflightPorts(conflictConfig(port), strings.NewReader(tc.input), &out, tc.interactive, tc.force, tc.kill)
			if (err == nil) != tc.wantKill {
				t.Fatalf("error=%v wantKill=%v", err, tc.wantKill)
			}
			if ports.NewPortResolver("").IsPortAvailable(port) != tc.wantKill {
				t.Fatal("unexpected listener state")
			}
			wantsPrompt := tc.interactive && !tc.force && tc.kill
			if strings.Contains(out.String(), "Proceed to kill? [y/N]") != wantsPrompt {
				t.Fatalf("unexpected prompt: %s", &out)
			}
			if tc.wantKill {
				if err := child.Wait(); err == nil {
					t.Fatal("expected signal termination")
				}
			}
		})
	}
}

func TestUp_ReservesDeclaredPorts(t *testing.T) {
	_, port := conflictChild(t)
	next, err := ports.NewPortResolver("").FindAvailablePort(port, 50)
	if err != nil {
		t.Fatal(err)
	}
	cfg := conflictConfig(port)
	svc := cfg.Services["web"]
	svc.PortPolicy = domain.PortPolicyRemap
	cfg.Services["web"] = svc
	cfg.Services["other"] = domain.ServiceConfig{Ports: []int{next}}
	_, err = commands.PreflightPorts(cfg, strings.NewReader(""), nil, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Services["web"].Ports[0] <= next {
		t.Fatal("remap stole another service's declared port")
	}
}

func TestUpCLIHelper(t *testing.T) {
	if os.Getenv("VIGIA_CLI_HELPER") != "1" {
		return
	}
	os.Args = append([]string{"vigiadev"}, strings.Split(os.Getenv("VIGIA_CLI_ARGS"), "|")...)
	if err := commands.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestUp_KillPortsFlagCLI(t *testing.T) {
	help := exec.Command(os.Args[0], "-test.run=^TestUpCLIHelper$")
	help.Env = append(os.Environ(), "VIGIA_CLI_HELPER=1", "VIGIA_CLI_ARGS=up|--help")
	output, err := help.CombinedOutput()
	if err != nil {
		t.Fatalf("help: %v %s", err, output)
	}
	for _, flag := range []string{"-k, --kill-ports", "-f, --force"} {
		if !strings.Contains(string(output), flag) {
			t.Fatalf("missing flag %s: %s", flag, output)
		}
	}
	for _, flags := range []string{"-k|-f", "--kill-ports|--force", "-k"} {
		t.Run(flags, func(t *testing.T) {
			original, port := conflictChild(t)
			dir := t.TempDir()
			cfg := fmt.Sprintf("version: 1\nproject_name: cli\nservices:\n  web:\n    command: [%q, '-test.run=^TestPortConflictListenerHelper$']\n    ports: [%d]\n    port_policy: fail\n    env:\n      VIGIA_CONFLICT_HELPER: '1'\n      PORT: '%d'\n", os.Args[0], port, port)
			if err := os.WriteFile(filepath.Join(dir, "vigiadev.yaml"), []byte(cfg), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestUpCLIHelper$")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "VIGIA_CLI_HELPER=1", "VIGIA_CLI_ARGS=up|--no-tui|"+flags)
			var log bytes.Buffer
			cmd.Stdout = &log
			cmd.Stderr = &log
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			t.Cleanup(func() {
				_ = cmd.Process.Signal(syscall.SIGTERM)
				select {
				case <-done:
				case <-time.After(4 * time.Second):
					_ = cmd.Process.Kill()
					<-done
					t.Error("CLI teardown timed out")
				}
			})
			deadline := time.Now().Add(4 * time.Second)
			for {
				info, err := ports.FindProcessByPort(port)
				if err == nil && info.PID != original.Process.Pid {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("CLI did not replace the listener")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err := original.Wait(); err == nil {
				t.Fatal("original process was not terminated")
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				done <- err
				if err != nil {
					t.Fatalf("CLI failed: %v %s", err, &log)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("CLI did not exit")
			}
			if !ports.NewPortResolver("").IsPortAvailable(port) {
				t.Fatal("CLI left owned listener running")
			}
		})
	}
}

func TestUp_DeclarativePoliciesAndAllPorts(t *testing.T) {
	t.Run("yaml-kill-force", func(t *testing.T) {
		child, port := conflictChild(t)
		cfg := conflictConfig(port)
		svc := cfg.Services["web"]
		svc.PortPolicy = domain.PortPolicyKill
		cfg.Services["web"] = svc
		if _, err := commands.PreflightPorts(cfg, nil, nil, false, true, true); err != nil {
			t.Fatal(err)
		}
		if err := child.Wait(); err == nil {
			t.Fatal("expected termination")
		}
	})
	t.Run("second-port", func(t *testing.T) {
		_, port := conflictChild(t)
		cfg := conflictConfig(port)
		svc := cfg.Services["web"]
		svc.Ports = []int{0, port}
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		svc.Ports[0] = l.Addr().(*net.TCPAddr).Port
		l.Close()
		cfg.Services["web"] = svc
		if _, err := commands.PreflightPorts(cfg, strings.NewReader("n\n"), nil, true, false, false); err == nil {
			t.Fatal("second port was not inspected")
		}
		if ports.NewPortResolver("").IsPortAvailable(port) {
			t.Fatal("listener killed on refusal")
		}
	})
}

func TestFilterServices(t *testing.T) {
	cfg := &domain.VigiaConfig{
		ProjectName: "demo",
		Services: map[string]domain.ServiceConfig{
			"db":       {Command: []string{"db"}},
			"backend":  {Command: []string{"api"}, DependsOn: []string{"db"}},
			"frontend": {Command: []string{"web"}, DependsOn: []string{"backend"}},
			"worker":   {Command: []string{"worker"}, DependsOn: []string{"db"}},
			"extra":    {Command: []string{"extra"}},
		},
	}

	// 1. Sem targets retorna todos
	all, err := commands.FilterServices(cfg, nil)
	if err != nil || len(all.Services) != 5 {
		t.Fatalf("expected all services, got %v", all.Services)
	}

	// 2. Filtrando apenas "frontend": deve trazer "frontend", "backend" e "db" (transitivas)
	filtered, err := commands.FilterServices(cfg, []string{"frontend"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Services) != 3 {
		t.Fatalf("expected 3 services (frontend, backend, db), got %d: %v", len(filtered.Services), filtered.Services)
	}
	if _, ok := filtered.Services["worker"]; ok {
		t.Fatal("worker should not be in filtered services")
	}
	if _, ok := filtered.Services["extra"]; ok {
		t.Fatal("extra should not be in filtered services")
	}

	// 3. Serviço inexistente retorna erro com lista de disponíveis
	_, err = commands.FilterServices(cfg, []string{"nonexistent"})
	if err == nil || !strings.Contains(err.Error(), "Available services:") {
		t.Fatalf("expected error listing available services, got: %v", err)
	}
}
