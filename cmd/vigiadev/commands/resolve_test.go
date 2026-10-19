package commands_test

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/cmd/vigiadev/commands"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ports"
)

func TestResidualListenerHelper(t *testing.T) {
	address := os.Getenv("VIGIA_RESIDUAL_ADDRESS")
	if address == "" {
		return
	}
	if os.Getenv("VIGIA_RESIDUAL_IGNORE_TERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		panic(err)
	}
	fmt.Println(listener.Addr().(*net.TCPAddr).Port)
	for {
		conn, err := listener.Accept()
		if err != nil {
			panic(err)
		}
		conn.Close()
	}
}

func residualChild(t *testing.T, address string, ignore bool) (*exec.Cmd, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestResidualListenerHelper$")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "VIGIA_RESIDUAL_ADDRESS="+address)
	if ignore {
		cmd.Env = append(cmd.Env, "VIGIA_RESIDUAL_IGNORE_TERM=1")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
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

func residualConfig(t *testing.T, dir string, port int, extra string) string {
	t.Helper()
	path := filepath.Join(dir, "vigiadev.local.yaml")
	content := fmt.Sprintf("version: 1\nproject_name: residual-test\nservices:\n  web:\n    command: [server]\n    port_policy: remap\n    ports: [%d]\n%s", port, extra)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveProjectPortsMultipleListeners(t *testing.T) {
	first, port := residualChild(t, "127.0.0.1:0", false)
	second, _ := residualChild(t, fmt.Sprintf("127.0.0.2:%d", port), true)
	_, unrelated := residualChild(t, "127.0.0.1:0", false)
	dir := t.TempDir()
	residualConfig(t, dir, port, "")
	var out bytes.Buffer
	if err := commands.ResolveProjectPorts(dir, "", &out); err != nil {
		t.Fatal(err)
	}
	for _, child := range []*exec.Cmd{first, second} {
		if !strings.Contains(out.String(), fmt.Sprintf("Terminated PID %d", child.Process.Pid)) {
			t.Fatalf("missing PID report: %s", &out)
		}
		if err := child.Wait(); err == nil {
			t.Fatal("child not terminated")
		}
	}
	if !ports.NewPortResolver("").IsPortAvailable(port) || ports.NewPortResolver("").IsPortAvailable(unrelated) {
		t.Fatal("cleanup scope mismatch")
	}
	out.Reset()
	if err := commands.ResolveProjectPorts(dir, "", &out); err != nil || !strings.Contains(out.String(), "No port conflicts detected") {
		t.Fatalf("idempotence: %v %s", err, &out)
	}
}

func TestResolvePreservesReuseComposeAndSharedPorts(t *testing.T) {
	for _, declaration := range []string{
		"command: [server]\n    port_policy: reuse",
		"compose_service: postgres\n    port_policy: fail",
		"command: [server]", // omitted policy defaults to reuse
	} {
		t.Run(declaration, func(t *testing.T) {
			_, port := residualChild(t, "127.0.0.1:0", false)
			dir := t.TempDir()
			residualConfig(t, dir, port, fmt.Sprintf("  protected:\n    %s\n    ports: [%d]\n", declaration, port))
			var out bytes.Buffer
			if err := commands.ResolveProjectPorts(dir, "", &out); err != nil {
				t.Fatal(err)
			}
			if ports.NewPortResolver("").IsPortAvailable(port) {
				t.Fatal("protected listener killed")
			}
		})
	}
}

func TestResolveConfigSelectionAndAliases(t *testing.T) {
	for _, alias := range []string{"resolve", "clean-ports", "resolve-ports"} {
		t.Run(alias, func(t *testing.T) {
			_, port := residualChild(t, "127.0.0.1:0", false)
			dir := t.TempDir()
			file := residualConfig(t, dir, port, "")
			custom := filepath.Join(dir, "custom.yaml")
			if err := os.Rename(file, custom); err != nil {
				t.Fatal(err)
			}
			root := commands.GetRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs([]string{alias, "-c", custom})
			t.Cleanup(func() { root.SetArgs(nil); root.SetOut(nil); _ = root.PersistentFlags().Set("config", "") })
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if !ports.NewPortResolver("").IsPortAvailable(port) {
				t.Fatal("explicit config not honored")
			}
		})
	}
}

func TestResolveInvalidConfigurationDoesNotKill(t *testing.T) {
	_, port := residualChild(t, "127.0.0.1:0", false)
	dir := t.TempDir()
	file := residualConfig(t, dir, port, "")
	if err := os.WriteFile(file, []byte("version: invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := commands.ResolveProjectPorts(dir, "", nil); err == nil {
		t.Fatal("invalid config accepted")
	}
	if ports.NewPortResolver("").IsPortAvailable(port) {
		t.Fatal("killed before config validation")
	}
}

func TestResolveAllFlagStopsComposeAndFreesAllPorts(t *testing.T) {
	_, port := residualChild(t, "127.0.0.1:0", false)
	dir := t.TempDir()
	log := filepath.Join(dir, "docker.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\" >> \"$VIGIA_DOCKER_LOG\"\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VIGIA_DOCKER_LOG", log)

	cfg := fmt.Sprintf("version: 1\nproject_name: resolve-all-test\ncompose_file: stack.yml\nservices:\n  db:\n    compose_service: db\n    port_policy: reuse\n    ports: [%d]\n", port)
	if err := os.WriteFile(filepath.Join(dir, "vigiadev.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}

	// Without --all: preserves reuse/compose ports and prints hint
	var out bytes.Buffer
	if err := commands.ResolveProjectPorts(dir, "", &out, false); err != nil {
		t.Fatal(err)
	}
	if ports.NewPortResolver("").IsPortAvailable(port) {
		t.Fatal("port should be preserved without --all")
	}
	if !strings.Contains(out.String(), "Reusable Docker Compose containers or ports are active") {
		t.Fatalf("expected hint in output: %s", out.String())
	}

	// With --all: stops Compose service and resolves port
	out.Reset()
	if err := commands.ResolveProjectPorts(dir, "", &out, true); err != nil {
		t.Fatal(err)
	}
	if !ports.NewPortResolver("").IsPortAvailable(port) {
		t.Fatal("port should be freed with --all")
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	expected := dir + "\ncompose\n-f\nstack.yml\nstop\ndb\n"
	if string(data) != expected {
		t.Fatalf("unexpected compose call: %q expected %q", data, expected)
	}
}
