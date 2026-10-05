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
	for _, answer := range []string{"y", "yes", "r", "remap", "n", "no", "", "invalid", "EOF"} {
		t.Run(answer, func(t *testing.T) {
			child, port := conflictChild(t)
			cfg := conflictConfig(port)
			input := answer + "\n"
			if answer == "EOF" {
				input = "y"
			}
			var output bytes.Buffer
			expectedPort, err := ports.NewPortResolver("").FindAvailablePort(port, 50)
			if err != nil {
				t.Fatal(err)
			}
			events, err := commands.PreflightPorts(cfg, strings.NewReader(input), &output, true, false, false)
			if !strings.Contains(output.String(), fmt.Sprintf("PID %d", child.Process.Pid)) || !strings.Contains(output.String(), "[y/N/remap]") {
				t.Fatalf("missing prompt: %s", &output)
			}
			switch answer {
			case "y", "yes":
				if err != nil {
					t.Fatal(err)
				}
				if !ports.NewPortResolver("").IsPortAvailable(port) {
					t.Fatal("port not released")
				}
				if err := child.Wait(); err == nil {
					t.Fatal("process did not terminate by signal")
				}
			case "r", "remap":
				if err != nil {
					t.Fatal(err)
				}
				svc := cfg.Services["web"]
				if svc.Ports[0] != expectedPort || svc.Env["PORT"] != strconv.Itoa(expectedPort) || svc.Command[1] != strconv.Itoa(expectedPort) || svc.HealthCheck.Port != expectedPort || svc.HealthCheck.Command[1] != strconv.Itoa(expectedPort) || !strings.HasSuffix(svc.HealthCheck.URL, strconv.Itoa(expectedPort)) {
					t.Fatalf("remap incomplete: %+v", svc)
				}
				if len(events) != 1 || events[0].OriginalPort != port || events[0].TargetPort != expectedPort {
					t.Fatalf("remap event: %v", events)
				}
				if ports.NewPortResolver("").IsPortAvailable(port) {
					t.Fatal("remap killed original listener")
				}
			default:
				if err == nil || err.Error() != "Operation cancelled due to port conflict." {
					t.Fatalf("expected cancellation: %v", err)
				}
				if ports.NewPortResolver("").IsPortAvailable(port) {
					t.Fatal("cancel killed original listener")
				}
			}
		})
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
			wantsPrompt := tc.interactive && !tc.force
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
	cfg.Services["other"] = domain.ServiceConfig{Ports: []int{next}}
	_, err = commands.PreflightPorts(cfg, strings.NewReader("remap\n"), nil, true, false, false)
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
			cfg := fmt.Sprintf("version: 1\nproject_name: cli\nservices:\n  web:\n    command: [%q, '-test.run=^TestPortConflictListenerHelper$']\n    ports: [%d]\n    env:\n      VIGIA_CONFLICT_HELPER: '1'\n      PORT: '%d'\n", os.Args[0], port, port)
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
		if _, err := commands.PreflightPorts(cfg, nil, nil, false, true, false); err != nil {
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
