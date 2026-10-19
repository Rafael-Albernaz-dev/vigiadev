package commands_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/cmd/vigiadev/commands"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/ports"
)

func TestDownWithoutManifestFreesAllResidualListeners(t *testing.T) {
	first, port := residualChild(t, "127.0.0.1:0", false)
	second, _ := residualChild(t, fmt.Sprintf("127.0.0.2:%d", port), true)
	_, reused := residualChild(t, "127.0.0.1:0", false)
	dir := t.TempDir()
	residualConfig(t, dir, port, fmt.Sprintf("  db:\n    command: [db]\n    port_policy: reuse\n    ports: [%d]\n", reused))
	var out bytes.Buffer
	if err := commands.DownProject(dir, "", &out); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int{first.Process.Pid, second.Process.Pid} {
		if !strings.Contains(out.String(), fmt.Sprintf("terminated PID %d", pid)) {
			t.Fatalf("missing feedback: %s", &out)
		}
	}
	if !ports.NewPortResolver("").IsPortAvailable(port) || ports.NewPortResolver("").IsPortAvailable(reused) {
		t.Fatal("residual/reuse ports mismatch")
	}
	if err := commands.DownProject(dir, "", &out); err != nil {
		t.Fatalf("second down failed: %v", err)
	}
}

func TestDownStopsTrackedProcessAndResidualPort(t *testing.T) {
	tracked, trackedPort := residualChild(t, "127.0.0.1:0", false)
	_, residual := residualChild(t, "127.0.0.1:0", false)
	dir := t.TempDir()
	residualConfig(t, dir, residual, "")
	m := &manifest.RunManifest{RunID: "down-test", ProjectName: "residual-test", Services: map[string]manifest.ServiceManifest{
		"tracked": {PID: tracked.Process.Pid, PGID: tracked.Process.Pid, Port: trackedPort},
	}}
	if err := manifest.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := commands.DownProject(dir, "", &out); err != nil {
		t.Fatal(err)
	}
	for _, port := range []int{trackedPort, residual} {
		if !ports.NewPortResolver("").IsPortAvailable(port) {
			t.Fatalf("port %d still occupied", port)
		}
	}
	if _, err := manifest.ReadManifest(dir); !os.IsNotExist(err) {
		t.Fatalf("manifest not removed: %v", err)
	}
}

func TestDownComposeStopAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "docker.log")
			script := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\" >> \"$VIGIA_DOCKER_LOG\"\n"
			if fail {
				script += "echo stop-failed >&2\nexit 1\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("VIGIA_DOCKER_LOG", log)
			cfg := "version: 1\nproject_name: compose-test\ncompose_file: stack.yml\nservices:\n  web:\n    compose_service: web\n    port_policy: fail\n  reused:\n    compose_service: db\n    port_policy: reuse\n"
			if err := os.WriteFile(filepath.Join(dir, "vigiadev.yaml"), []byte(cfg), 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			err := commands.DownProject(dir, "", &out)
			if (err != nil) != fail {
				t.Fatalf("down error=%v want failure=%v", err, fail)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			expected := dir + "\ncompose\n-f\nstack.yml\nstop\ndb\n" + dir + "\ncompose\n-f\nstack.yml\nstop\nweb\n"
			if string(data) != expected {
				t.Fatalf("unsafe compose call: %q expected %q", data, expected)
			}
			if fail && strings.Contains(out.String(), "Stopped Compose service") {
				t.Fatal("false Compose success")
			}
		})
	}

	t.Run("keep_containers", func(t *testing.T) {
		dir := t.TempDir()
		log := filepath.Join(dir, "docker.log")
		script := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\" >> \"$VIGIA_DOCKER_LOG\"\n"
		if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("VIGIA_DOCKER_LOG", log)
		cfg := "version: 1\nproject_name: compose-test\ncompose_file: stack.yml\nservices:\n  web:\n    compose_service: web\n    port_policy: fail\n  reused:\n    compose_service: db\n    port_policy: reuse\n"
		if err := os.WriteFile(filepath.Join(dir, "vigiadev.yaml"), []byte(cfg), 0600); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := commands.DownProject(dir, "", &out, true); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		expected := dir + "\ncompose\n-f\nstack.yml\nstop\nweb\n"
		if string(data) != expected {
			t.Fatalf("keep-containers compose call: %q expected %q", data, expected)
		}
	})
}

func TestDownRelativeExplicitConfig(t *testing.T) {
	_, port := residualChild(t, "127.0.0.1:0", false)
	dir := t.TempDir()
	sub := filepath.Join(dir, "config")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	residualConfig(t, sub, port, "")
	t.Chdir(dir)
	root := commands.GetRootCmd()
	root.SetArgs([]string{"down", "-c", "config/vigiadev.local.yaml"})
	var out bytes.Buffer
	root.SetOut(&out)
	t.Cleanup(func() { root.SetArgs(nil); root.SetOut(nil); _ = root.PersistentFlags().Set("config", "") })
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !ports.NewPortResolver("").IsPortAvailable(port) {
		t.Fatal("relative -c port still occupied")
	}
}

func TestDownStillResolvesHostPortsAfterTrackedContainerFailure(t *testing.T) {
	_, port := residualChild(t, "127.0.0.1:0", false)
	dir := t.TempDir()
	residualConfig(t, dir, port, "")
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\necho cannot-stop >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	m := &manifest.RunManifest{RunID: "container-failure", Containers: []string{"owned-container"}}
	if err := manifest.WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := commands.DownProject(dir, "", &out)
	if err == nil || !strings.Contains(err.Error(), "cannot-stop") {
		t.Fatalf("container failure hidden: %v", err)
	}
	if !ports.NewPortResolver("").IsPortAvailable(port) {
		t.Fatal("container failure prevented host port cleanup")
	}
	if _, err := manifest.ReadManifest(dir); err != nil {
		t.Fatalf("failed teardown discarded ownership manifest: %v", err)
	}
}
