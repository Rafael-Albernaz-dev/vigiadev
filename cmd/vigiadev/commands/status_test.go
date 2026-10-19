package commands_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/cmd/vigiadev/commands"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
)

func TestStatusUntrackedMultipleListenersAndAlert(t *testing.T) {
	first, port := residualChild(t, "127.0.0.1:0", false)
	second, _ := residualChild(t, fmt.Sprintf("127.0.0.2:%d", port), false)
	dir := t.TempDir()
	cfg := residualConfig(t, dir, port, "")
	var out bytes.Buffer
	if err := commands.InspectProjectStatus(dir, cfg, &out); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"UNTRACKED / ORPHAN", strconv.Itoa(first.Process.Pid), strconv.Itoa(second.Process.Pid), "┌", "└", "UNTRACKED PROCESSES DETECTED ON PROJECT PORTS", "vigiadev resolve", "vigiadev down", "1 occupied port(s)"} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("missing %q: %s", expected, &out)
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "• web") && strings.Contains(line, "STOPPED") {
			t.Fatalf("live service reported stopped: %s", line)
		}
	}
}

func TestStatusAlertAlsoWithActiveSession(t *testing.T) {
	tracked, port := residualChild(t, "127.0.0.1:0", false)
	_, external := residualChild(t, "127.0.0.1:0", false)
	dir := t.TempDir()
	cfg := residualConfig(t, dir, port, fmt.Sprintf("  outside:\n    command: [server]\n    ports: [%d]\n    port_policy: fail\n", external))
	m := &manifest.RunManifest{RunID: "active-test", Services: map[string]manifest.ServiceManifest{"web": {PID: tracked.Process.Pid, PGID: tracked.Process.Pid}}}
	if err := manifest.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.VigiaDir, manifest.LockFile), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := commands.InspectProjectStatus(dir, cfg, &out); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Session: ACTIVE", "active (session PID", "UNTRACKED PROCESSES DETECTED", "1 occupied port(s)"} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("missing %q: %s", expected, &out)
		}
	}
}

func TestStatusReuseAndFreePorts(t *testing.T) {
	child, port := residualChild(t, "127.0.0.1:0", false)
	dir := t.TempDir()
	cfg := residualConfig(t, dir, port, "")
	content, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	content = bytes.ReplaceAll(content, []byte("port_policy: remap"), []byte("port_policy: reuse"))
	if err := os.WriteFile(cfg, content, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := commands.InspectProjectStatus(dir, cfg, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "EXTERNAL / REUSED") || strings.Contains(out.String(), "UNTRACKED PROCESSES DETECTED") {
		t.Fatalf("reuse status incorrect: %s", &out)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	out.Reset()
	if err := commands.InspectProjectStatus(dir, cfg, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), fmt.Sprintf(":%d free", port)) || strings.Contains(out.String(), "UNTRACKED") {
		t.Fatalf("free port status incorrect: %s", &out)
	}
}
