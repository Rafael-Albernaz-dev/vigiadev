package ports

import (
	"bufio"
	"errors"
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
	"time"
)

func TestInspectorListenerHelper(t *testing.T) {
	if os.Getenv("VIGIA_INSPECTOR_HELPER") != "1" {
		return
	}
	if os.Getenv("VIGIA_IGNORE_TERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	network := os.Getenv("VIGIA_LISTEN_NETWORK")
	address := "127.0.0.1:0"
	if network == "tcp6" {
		address = "[::1]:0"
	}
	l, err := net.Listen(network, address)
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

func inspectorChild(t *testing.T, ignore bool, network string) (*exec.Cmd, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestInspectorListenerHelper$")
	cmd.Env = append(os.Environ(), "VIGIA_INSPECTOR_HELPER=1", "VIGIA_LISTEN_NETWORK="+network)
	if ignore {
		cmd.Env = append(cmd.Env, "VIGIA_IGNORE_TERM=1")
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

func TestFindProcessByPort(t *testing.T) {
	for _, network := range []string{"tcp4", "tcp6"} {
		t.Run(network, func(t *testing.T) {
			cmd, port := inspectorChild(t, false, network)
			info, err := FindProcessByPort(port)
			if err != nil {
				t.Fatal(err)
			}
			if info.PID != cmd.Process.Pid || info.Name == "" || !strings.Contains(info.CommandLine, "TestInspectorListenerHelper") {
				t.Fatalf("unexpected process: %+v", info)
			}
		})
	}
	for _, port := range []int{-1, 0, 65536} {
		if _, err := FindProcessByPort(port); err == nil {
			t.Fatalf("invalid port %d accepted", port)
		}
	}
}

func TestFindProcessByPort_Permissions(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "4242"), 0700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = findProcessOwner(5432, map[string]bool{"listener-inode": true}, entries, func(pid int) (map[string]bool, error) {
		if pid != 4242 {
			t.Fatalf("unexpected PID %d", pid)
		}
		return nil, os.ErrPermission
	})
	if !errors.Is(err, ErrProcessInaccessible) {
		t.Fatalf("expected inaccessible listener error, got %v", err)
	}
}

func TestKillProcessByPID(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		t.Run(fmt.Sprint("ignore-term=", ignore), func(t *testing.T) {
			cmd, port := inspectorChild(t, ignore, "tcp4")
			start := time.Now()
			if err := KillProcessByPID(cmd.Process.Pid, 100*time.Millisecond); err != nil {
				t.Fatal(err)
			}
			if ignore && time.Since(start) < 100*time.Millisecond {
				t.Fatal("SIGTERM grace period was not honored")
			}
			if !NewPortResolver("").IsPortAvailable(port) {
				t.Fatal("port still occupied")
			}
			if err := cmd.Wait(); err == nil {
				t.Fatal("expected child to terminate by signal")
			}
			if err := KillProcessByPID(cmd.Process.Pid, time.Millisecond); err != nil {
				t.Fatalf("not idempotent: %v", err)
			}
		})
	}
	for _, pid := range []int{-1, 0, 1, os.Getpid()} {
		if err := KillProcessByPID(pid, time.Millisecond); err == nil {
			t.Fatalf("unsafe PID %d accepted", pid)
		}
	}
}

func TestKillProcessOnPortRejectsChangedOwner(t *testing.T) {
	cmd, port := inspectorChild(t, false, "tcp4")
	wrong := &ProcessInfo{PID: cmd.Process.Pid + 1}
	if err := KillProcessOnPort(port, wrong, time.Millisecond); err == nil {
		t.Fatal("unconfirmed owner killed")
	}
	if NewPortResolver("").IsPortAvailable(port) {
		t.Fatal("listener lost after refused kill")
	}
}
