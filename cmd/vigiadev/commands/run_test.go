package commands_test

import (
	"bytes"
	"context"
	"github.com/Rafael-Albernaz-dev/vigiadev/cmd/vigiadev/commands"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/application"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
)

func TestTaskRunner_Execution(t *testing.T) {
	tempDir := t.TempDir()

	content := `
version: 1
project_name: test-runner
services:
  echo-svc:
    command: ["echo", "svc-ready"]
tasks:
  test-task:
    command: ["echo", "task-executed"]
    depends_on: [echo-svc]
    env:
      SAMPLE_KEY: "sample_val"
`
	cfgFile := filepath.Join(tempDir, "vigiadev.yaml")
	if err := os.WriteFile(cfgFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadConfig(cfgFile)
	if err != nil {
		t.Fatalf("falha ao carregar config: %v", err)
	}

	bus := domain.NewEventBus()
	orch, err := application.NewOrchestrator(cfg, tempDir, bus)
	if err != nil {
		t.Fatalf("falha ao criar orchestrator: %v", err)
	}

	// 1. Executa task com dependência
	code, err := orch.RunTask(t.Context(), "test-task", []string{"extra-param"}, false)
	if err != nil {
		t.Fatalf("RunTask retornou erro: %v", err)
	}
	if code != 0 {
		t.Errorf("esperava exitCode 0, obteve %d", code)
	}

	// 2. Executa com --no-deps
	code, err = orch.RunTask(t.Context(), "test-task", nil, true)
	if err != nil {
		t.Fatalf("RunTask com noDeps retornou erro: %v", err)
	}
	if code != 0 {
		t.Errorf("esperava exitCode 0, obteve %d", code)
	}
}

// A subprocess exercises the real Cobra command, runner and exit-code path.
func TestRunCLIHelper(t *testing.T) {
	if os.Getenv("VIGIA009_CLI_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"vigiadev"}, os.Args[i+1:]...)
			break
		}
	}
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func runCLI(t *testing.T, dir, input string, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestRunCLIHelper$", "--"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "VIGIA009_CLI_HELPER=1")
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("CLI timeout: %s", output)
	}
	if err == nil {
		return string(output), 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return string(output), exit.ExitCode()
	}
	t.Fatal(err)
	return "", -1
}

func writeFixture(t *testing.T, dir, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0640); err != nil {
		t.Fatal(err)
	}
}

func TestRun_AutoDiscovery(t *testing.T) {
	for _, section := range []string{"", "tasks: {} # keep inline\n", "tasks: # keep inline\n", "tasks: null # keep inline\n"} {
		for _, answer := range []string{"yes\n", "n\n", ""} {
			t.Run(section+answer, func(t *testing.T) {
				dir := t.TempDir()
				prefix := "# Project comment\nversion: 1\nproject_name: fixture\n"
				suffix := "# Services comment\nservices:\n  idle:\n    command: [echo, idle] # keep service\n"
				original := prefix + section + suffix
				writeFixture(t, dir, "vigiadev.yaml", original)
				writeFixture(t, dir, "go.mod", "module example.test/fixture\ngo 1.27.1\n")
				output, code := runCLI(t, dir, answer, "run")
				if code != 0 || !strings.Contains(output, "go test ./...") || !strings.Contains(output, "Save discovered tasks") {
					t.Fatalf("exit %d: %s", code, output)
				}
				data, err := os.ReadFile(filepath.Join(dir, "vigiadev.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				if answer != "yes\n" {
					if string(data) != original {
						t.Fatal("decline changed YAML")
					}
					return
				}
				if !strings.Contains(string(data), suffix) || !strings.HasPrefix(string(data), prefix) {
					t.Fatalf("unrelated YAML changed: %s", data)
				}
				if section != "" && !strings.Contains(string(data), "# keep inline") {
					t.Fatalf("comment lost: %s", data)
				}
				cfg, err := config.LoadConfig(filepath.Join(dir, "vigiadev.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Join(cfg.Tasks["test"].Command, " ") != "go test ./..." {
					t.Fatalf("tasks: %+v", cfg.Tasks)
				}
				info, err := os.Stat(filepath.Join(dir, "vigiadev.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0640 {
					t.Fatal("mode changed")
				}
				output, code = runCLI(t, dir, "", "run")
				if code != 0 || strings.Contains(output, "Save discovered tasks") {
					t.Fatalf("repeat: %s", output)
				}
			})
		}
	}
}

func TestRun_JustInTimeDiscovery(t *testing.T) {
	for _, answer := range []string{"y\n", "n\n"} {
		t.Run(answer, func(t *testing.T) {
			dir := t.TempDir()
			original := "version: 1\nproject_name: fixture\ntasks:\n  lint: # existing task\n    command: [echo, lint]\n# keep footer\n"
			writeFixture(t, dir, "vigiadev.yaml", original)
			writeFixture(t, dir, "go.mod", "module example.test/fixture\ngo 1.27.1\n")
			writeFixture(t, dir, "proof_test.go", `package fixture
import ("testing"; "os")
func TestProof(t *testing.T) { if err := os.WriteFile("executed", []byte("yes"), 0600); err != nil { t.Fatal(err) } }
func TestMustNotRun(t *testing.T) { t.Fatal("extra arguments were not forwarded") }
`)
			output, code := runCLI(t, dir, answer, "run", "test", "--no-deps", "--", "-run", "^TestProof$", "-count=1")
			if code != 0 || !strings.Contains(output, "Task 'test' auto-discovered: go test ./...") {
				t.Fatalf("exit %d: %s", code, output)
			}
			if _, err := os.Stat(filepath.Join(dir, "executed")); err != nil {
				t.Fatal("task was not executed", err)
			}
			data, err := os.ReadFile(filepath.Join(dir, "vigiadev.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if answer == "n\n" && string(data) != original {
				t.Fatal("decline changed YAML")
			}
			if !strings.Contains(string(data), "# existing task") || !strings.Contains(string(data), "# keep footer") {
				t.Fatalf("comments lost: %s", data)
			}
			cfg, err := config.LoadConfig(filepath.Join(dir, "vigiadev.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := cfg.Tasks["test"]; exists != (answer == "y\n") {
				t.Fatalf("unexpected persistence: %s", data)
			}
			// The real failing Go test propagates a nonzero status, including discovery.
			output, code = runCLI(t, dir, "n\n", "run", "test", "--", "-run", "^TestMustNotRun$", "-count=1")
			if code != 1 || !strings.Contains(output, "FAIL") {
				t.Fatalf("expected failure status 1, got %d: %s", code, output)
			}
		})
	}
}

func TestRun_DiscoveryBoundaries(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "vigiadev.yaml", "version: 1\nproject_name: fixture\n")
	output, code := runCLI(t, dir, "", "run", "unknown")
	if code != 1 || !strings.Contains(output, "not found") {
		t.Fatalf("exit %d: %s", code, output)
	}
	output, code = runCLI(t, dir, "", "run")
	if code != 0 || strings.Contains(output, "Save discovered") {
		t.Fatalf("empty repo: %s", output)
	}
	writeFixture(t, dir, "package.json", `{"scripts":{"test":"unused"}}`)
	writeFixture(t, dir, "vigiadev.local.yaml", "version: 1\nproject_name: local\ntasks:\n  test:\n    command: [echo, configured-wins]\n")
	output, code = runCLI(t, dir, "", "run", "test")
	if code != 0 || !strings.Contains(output, "configured-wins") || strings.Contains(output, "auto-discovered") {
		t.Fatalf("configured task: %s", output)
	}
	writeFixture(t, dir, "custom.yaml", "version: 1\nproject_name: custom\n")
	output, code = runCLI(t, dir, "y\n", "--config", "custom.yaml", "run")
	if code != 0 {
		t.Fatal(output)
	}
	cfg, err := config.LoadConfig(filepath.Join(dir, "custom.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tasks) != 1 {
		t.Fatal("explicit config not saved")
	}
	cfg, err = config.LoadConfig(filepath.Join(dir, "vigiadev.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tasks) != 0 {
		t.Fatal("wrong config modified")
	}
}

func TestSaveDiscoveredTasks_Preservation(t *testing.T) {
	for _, original := range []string{
		"version: 1\nproject_name: demo\n# footer\n...\n",
		"version: 1\nproject_name: demo",
		"version: 1\ntasks: {lint: {command: [echo, ok]}} # inline\n# service header\nservices: {}\nproject_name: demo\n",
		"version: 1\ntasks:\n  lint: # inline\n    command: [echo, ok]\n\n# service header\nservices: {}\nproject_name: demo\n",
	} {
		t.Run(original, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "vigiadev.yaml")
			writeFixture(t, dir, "vigiadev.yaml", original)
			tasks := map[string]domain.TaskConfig{"test": {Command: []string{"echo", "test"}}, "lint": {Command: []string{"false"}}}
			if err := config.SaveDiscoveredTasks(path, tasks); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Tasks) != 2 {
				t.Fatalf("tasks: %+v", cfg.Tasks)
			}
			if strings.Contains(original, "lint:") && cfg.Tasks["lint"].Command[0] != "echo" {
				t.Fatal("overwrote existing task")
			}
			first, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, comment := range []string{"# footer", "# inline", "# service header"} {
				if strings.Contains(original, comment) && bytes.Count(first, []byte(comment)) != 1 {
					t.Fatalf("comment missing or duplicated: %s", first)
				}
			}
			if err := config.SaveDiscoveredTasks(path, tasks); err != nil {
				t.Fatal(err)
			}
			second, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first, second) {
				t.Fatal("not idempotent")
			}
		})
	}
}

func TestInit_WithTasks(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.test/demo\n")
	output, code := runCLI(t, dir, "", "init")
	if code != 0 || !strings.Contains(output, "go test ./...") {
		t.Fatalf("exit %d: %s", code, output)
	}
	cfg, err := config.LoadConfig(filepath.Join(dir, "vigiadev.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Tasks["test"].Command, " ") != "go test ./..." {
		t.Fatalf("tasks: %+v", cfg.Tasks)
	}
}

func TestSaveDiscoveredTasks_AtomicFailureAndSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vigiadev.yaml")
	original := "version: 1\nproject_name: fixture\n"
	writeFixture(t, dir, "vigiadev.yaml", original)
	link := filepath.Join(dir, "linked.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	invalid := map[string]domain.TaskConfig{"test": {Command: []string{"echo"}, DependsOn: []string{"missing"}}}
	if err := config.SaveDiscoveredTasks(link, invalid); err == nil {
		t.Fatal("expected validation failure")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatal("failed save changed original")
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".vigiadev-tasks-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("temporary files leaked: %v", temps)
	}
	if err := config.SaveDiscoveredTasks(link, map[string]domain.TaskConfig{"test": {Command: []string{"echo", "ok"}}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced")
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tasks) != 1 {
		t.Fatal("target not updated")
	}
}

func TestSaveDiscoveredTasks_BlockScalar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vigiadev.yaml")
	original := "version: 1\nproject_name: fixture\ntasks:\n  lint:\n    command: [echo, lint]\n    env:\n      DESCRIPTION: |\n        text\n        # literal content\n# services comment\nservices: {}\n"
	writeFixture(t, dir, "vigiadev.yaml", original)
	before, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.SaveDiscoveredTasks(path, map[string]domain.TaskConfig{"test": {Command: []string{"echo", "ok"}}}); err != nil {
		t.Fatal(err)
	}
	after, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Tasks["lint"].Env["DESCRIPTION"] != after.Tasks["lint"].Env["DESCRIPTION"] {
		t.Fatal("literal content changed")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(data, []byte("# literal content")) != 1 {
		t.Fatalf("literal content duplicated: %s", data)
	}
}
