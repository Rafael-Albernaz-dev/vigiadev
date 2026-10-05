package commands_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/cmd/vigiadev/commands"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
	"github.com/spf13/cobra"
)

func readInitFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEnsureGitignore(t *testing.T) {
	const header = "# vigiaDev runtime, sessions & local configs"
	for _, tc := range []struct {
		name, original, addition string
		exists                   bool
	}{
		{"new", "", header + "\n.vigiadev/\nvigiadev.local.yaml\n", false},
		{"empty", "", header + "\n.vigiadev/\nvigiadev.local.yaml\n", true},
		{"preserve", "# user rules\nnode_modules/\n!keep.log\n", header + "\n.vigiadev/\nvigiadev.local.yaml\n", true},
		{"no final newline", "*.log", "\n" + header + "\n.vigiadev/\nvigiadev.local.yaml\n", true},
		{"runtime present", ".vigiadev/\n", header + "\nvigiadev.local.yaml\n", true},
		{"local present", "vigiadev.local.yaml\n", header + "\n.vigiadev/\n", true},
		{"both present", ".vigiadev/\nvigiadev.local.yaml", "", true},
		{"header present", header + "\n.vigiadev/\n", "vigiadev.local.yaml\n", true},
		{"CRLF", "# user\r\n.vigiadev/\r\n", header + "\r\nvigiadev.local.yaml\r\n", true},
		{"similar rules", "# .vigiadev/\n!.vigiadev/\nvigiadev.local.yaml.bak\n", header + "\n.vigiadev/\nvigiadev.local.yaml\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.exists {
				writeFixture(t, dir, ".gitignore", tc.original)
			}
			changed, err := commands.EnsureGitignore(dir)
			if err != nil {
				t.Fatal(err)
			}
			if changed != (tc.addition != "") {
				t.Fatalf("changed = %v", changed)
			}
			first := readInitFile(t, dir, ".gitignore")
			if first != tc.original+tc.addition {
				t.Fatalf("unexpected content: %q", first)
			}
			changed, err = commands.EnsureGitignore(dir)
			if err != nil || changed {
				t.Fatalf("second call: changed=%v, err=%v", changed, err)
			}
			if readInitFile(t, dir, ".gitignore") != first {
				t.Fatal("second call modified content")
			}
			if tc.exists {
				info, err := os.Stat(filepath.Join(dir, ".gitignore"))
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0640 {
					t.Fatalf("permissions changed: %v", info.Mode())
				}
			}
		})
	}
}

func TestEnsureGitignore_Error(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".gitignore"), 0755); err != nil {
		t.Fatal(err)
	}
	if changed, err := commands.EnsureGitignore(dir); err == nil || changed {
		t.Fatalf("expected read error, got %v, %v", changed, err)
	}
	if changed, err := commands.EnsureGitignore(filepath.Join(dir, "missing")); err == nil || changed {
		t.Fatalf("expected open error, got %v, %v", changed, err)
	}
}

func assertInitTarget(t *testing.T, dir, target string) {
	t.Helper()
	cfg, err := config.LoadConfig(filepath.Join(dir, target))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Tasks["test"].Command, " ") != "go test ./..." {
		t.Fatalf("detected tasks not preserved: %+v", cfg.Tasks)
	}
	other := "vigiadev.yaml"
	if target == other {
		other = "vigiadev.local.yaml"
	}
	if _, err := os.Stat(filepath.Join(dir, other)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected other target: %v", err)
	}
	ignore := readInitFile(t, dir, ".gitignore")
	for _, rule := range []string{".vigiadev/", "vigiadev.local.yaml"} {
		if strings.Count(ignore, rule+"\n") != 1 {
			t.Fatalf("missing/duplicate rule: %q", ignore)
		}
	}
}

func TestInit_LocalFlag(t *testing.T) {
	for _, flag := range []string{"--local", "-l"} {
		t.Run(flag, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, "go.mod", "module example.test/demo\n")
			out, code := runCLI(t, dir, "2\n", "init", flag)
			if code != 0 || !strings.Contains(out, "'vigiadev.local.yaml' generated successfully!") || strings.Contains(out, "Select configuration target") {
				t.Fatalf("exit %d: %s", code, out)
			}
			assertInitTarget(t, dir, "vigiadev.local.yaml")
			ignore := readInitFile(t, dir, ".gitignore")
			original := readInitFile(t, dir, "vigiadev.local.yaml")
			out, code = runCLI(t, dir, "", "init", flag)
			if code == 0 || !strings.Contains(out, "use --force") {
				t.Fatalf("exit %d: %s", code, out)
			}
			if readInitFile(t, dir, ".gitignore") != ignore || readInitFile(t, dir, "vigiadev.local.yaml") != original {
				t.Fatal("repeated init modified existing files")
			}
		})
	}
}

func TestInit_Force(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "team", true: "local"}[local], func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, "go.mod", "module example.test/demo\n")
			name := "vigiadev.yaml"
			args := []string{"init"}
			if local {
				name = "vigiadev.local.yaml"
				args = append(args, "--local")
			}
			writeFixture(t, dir, name, "# existing user configuration\n")
			out, code := runCLI(t, dir, "", args...)
			if code == 0 || !strings.Contains(out, "use --force") {
				t.Fatalf("exit %d: %s", code, out)
			}
			if readInitFile(t, dir, name) != "# existing user configuration\n" {
				t.Fatal("overwritten without force")
			}
			out, code = runCLI(t, dir, "", append(args, "--force")...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, out)
			}
			assertInitTarget(t, dir, name)
		})
	}
}

func TestInit_NonTTY(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.test/demo\n")
	out, code := runCLI(t, dir, "2\n", "init")
	if code != 0 || strings.Contains(out, "Select configuration target") {
		t.Fatalf("exit %d: %s", code, out)
	}
	assertInitTarget(t, dir, "vigiadev.yaml")
}

func TestInit_InteractiveSelection(t *testing.T) {
	for _, tc := range []struct{ name, input, target string }{
		{"default", "\n", "vigiadev.yaml"},
		{"team", "1\n", "vigiadev.yaml"},
		{"local", "2\n", "vigiadev.local.yaml"},
		{"EOF default", "", "vigiadev.yaml"},
		{"retry", "bad\n2\n", "vigiadev.local.yaml"},
		{"EOF local", "2", "vigiadev.local.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, "go.mod", "module example.test/demo\n")
			cmd := &cobra.Command{}
			var out bytes.Buffer
			cmd.SetIn(strings.NewReader("yes\n" + tc.input))
			cmd.SetOut(&out)
			if err := commands.RunInit(cmd, dir, false, false, true); err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"Select configuration target:", "  [1] Team-shared (vigiadev.yaml - committed to repository) [default]", "  [2] Personal/Local (vigiadev.local.yaml - ignored by git)"} {
				if !strings.Contains(out.String(), text) {
					t.Fatalf("prompt missing %q: %s", text, out.String())
				}
			}
			assertInitTarget(t, dir, tc.target)
		})
	}
}

type initErrorReader struct{}

func (initErrorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestInit_PromptErrorsAndBypass(t *testing.T) {
	for _, input := range []io.Reader{initErrorReader{}, strings.NewReader("invalid")} {
		dir := t.TempDir()
		cmd := &cobra.Command{}
		cmd.SetIn(input)
		cmd.SetOut(io.Discard)
		if err := commands.RunInit(cmd, dir, false, false, true); err == nil {
			t.Fatal("expected prompt error")
		}
		if _, err := os.Stat(filepath.Join(dir, "vigiadev.yaml")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("prompt error created config")
		}
	}
	for _, tc := range []struct {
		force, local bool
		target       string
	}{
		{true, false, "vigiadev.yaml"}, {false, true, "vigiadev.local.yaml"}, {true, true, "vigiadev.local.yaml"},
	} {
		dir := t.TempDir()
		writeFixture(t, dir, "go.mod", "module example.test/demo\n")
		writeFixture(t, dir, ".gitignore", ".vigiadev/\nvigiadev.local.yaml\n")
		cmd := &cobra.Command{}
		cmd.SetIn(initErrorReader{})
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := commands.RunInit(cmd, dir, tc.force, tc.local, true); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "Select configuration target") {
			t.Fatal("unexpected prompt")
		}
		assertInitTarget(t, dir, tc.target)
	}
}

func TestInit_GitignorePrompt(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		update      bool
	}{
		{"Y", "Y\n", true},
		{"yes", "yes\n", true},
		{"enter", "\n", true},
		{"n", "n\n", false},
		{"no", "no\n", false},
		{"retry", "invalid\ny\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, "go.mod", "module example.test/demo\n")
			writeFixture(t, dir, ".gitignore", "# existing\n")
			cmd := &cobra.Command{}
			cmd.SetIn(strings.NewReader(tc.input + "2\n"))
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := commands.RunInit(cmd, dir, false, false, true); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "Add '.vigiadev/' and 'vigiadev.local.yaml' to .gitignore? [Y/n]: ") {
				t.Fatalf("missing prompt: %s", out.String())
			}
			ignore := readInitFile(t, dir, ".gitignore")
			if tc.update {
				if !strings.Contains(out.String(), "Updated .gitignore with vigiaDev rules.") || !strings.Contains(ignore, ".vigiadev/\nvigiadev.local.yaml\n") {
					t.Fatalf("update failed: %s, %q", out.String(), ignore)
				}
			} else if ignore != "# existing\n" || !strings.Contains(out.String(), "Skipped .gitignore update. Ensure .vigiadev/ is ignored manually.") {
				t.Fatalf("refusal modified .gitignore: %s, %q", out.String(), ignore)
			}
			if _, err := os.Stat(filepath.Join(dir, "vigiadev.local.yaml")); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, force := range []bool{false, true} {
		dir := t.TempDir()
		writeFixture(t, dir, "go.mod", "module example.test/demo\n")
		cmd := &cobra.Command{}
		cmd.SetIn(initErrorReader{})
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := commands.RunInit(cmd, dir, force, false, !force); force && err != nil {
			t.Fatal(err)
		} else if !force && err == nil {
			t.Fatal("interactive reader error was ignored")
		}
		if force {
			if strings.Contains(out.String(), "Add '.vigiadev/'") || !strings.Contains(readInitFile(t, dir, ".gitignore"), "vigiadev.local.yaml") {
				t.Fatal("force did not update automatically")
			}
		} else if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("prompt error modified .gitignore")
		}
	}
	{
		dir := t.TempDir()
		writeFixture(t, dir, "go.mod", "module example.test/demo\n")
		cmd := &cobra.Command{}
		cmd.SetIn(strings.NewReader("n\n"))
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := commands.RunInit(cmd, dir, false, false, false); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "Add '.vigiadev/'") || !strings.Contains(readInitFile(t, dir, ".gitignore"), "vigiadev.local.yaml") {
			t.Fatal("non-interactive init did not update automatically")
		}
	}
}

func TestInit_GitignoreFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".gitignore"), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	if err := commands.RunInit(cmd, dir, true, true, false); err == nil {
		t.Fatal("ignored hygiene failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "vigiadev.local.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created unprotected local config")
	}
}
