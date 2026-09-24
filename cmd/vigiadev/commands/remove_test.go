package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/cmd/vigiadev/commands"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/manifest"
)

func TestRunRemove_AllFiles(t *testing.T) {
	tempDir := t.TempDir()

	// Create .vigiadev directory and dummy files
	vigiaDir := filepath.Join(tempDir, manifest.VigiaDir)
	if err := os.MkdirAll(vigiaDir, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(vigiaDir, "run.json"), []byte("{}"), 0644)

	// Create yaml configs
	cfg1 := filepath.Join(tempDir, "vigiadev.yaml")
	cfg2 := filepath.Join(tempDir, "vigiadev.local.yaml")
	_ = os.WriteFile(cfg1, []byte("version: 1"), 0644)
	_ = os.WriteFile(cfg2, []byte("version: 1"), 0644)

	// Run with force=true, keepConf=false
	err := commands.RunRemove(tempDir, true, false, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all deleted
	if _, err := os.Stat(vigiaDir); !os.IsNotExist(err) {
		t.Errorf("expected .vigiadev to be deleted, err: %v", err)
	}
	if _, err := os.Stat(cfg1); !os.IsNotExist(err) {
		t.Errorf("expected vigiadev.yaml to be deleted")
	}
	if _, err := os.Stat(cfg2); !os.IsNotExist(err) {
		t.Errorf("expected vigiadev.local.yaml to be deleted")
	}
}

func TestRunRemove_KeepConfig(t *testing.T) {
	tempDir := t.TempDir()

	vigiaDir := filepath.Join(tempDir, manifest.VigiaDir)
	_ = os.MkdirAll(vigiaDir, 0755)
	cfg := filepath.Join(tempDir, "vigiadev.yaml")
	_ = os.WriteFile(cfg, []byte("version: 1"), 0644)

	// Run with keepConf=true
	err := commands.RunRemove(tempDir, true, true, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// .vigiadev must be deleted, but config must remain
	if _, err := os.Stat(vigiaDir); !os.IsNotExist(err) {
		t.Errorf("expected .vigiadev to be deleted")
	}
	if _, err := os.Stat(cfg); os.IsNotExist(err) {
		t.Errorf("expected vigiadev.yaml to be preserved with --keep-config")
	}
}

func TestRunRemove_ExplicitConfig(t *testing.T) {
	tempDir := t.TempDir()

	customCfg := filepath.Join(tempDir, "custom.yaml")
	_ = os.WriteFile(customCfg, []byte("version: 1"), 0644)

	err := commands.RunRemove(tempDir, true, false, "custom.yaml", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(customCfg); !os.IsNotExist(err) {
		t.Errorf("expected custom.yaml to be deleted")
	}
}

func TestRunRemove_ConfirmationCancelled(t *testing.T) {
	tempDir := t.TempDir()

	cfg := filepath.Join(tempDir, "vigiadev.yaml")
	_ = os.WriteFile(cfg, []byte("version: 1"), 0644)

	// Answering "n\n"
	inReader := strings.NewReader("n\n")
	err := commands.RunRemove(tempDir, false, false, "", inReader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// File should still exist because user answered 'n'
	if _, err := os.Stat(cfg); os.IsNotExist(err) {
		t.Errorf("expected vigiadev.yaml to NOT be deleted on cancellation")
	}
}

func TestRunRemove_ConfirmationAccepted(t *testing.T) {
	tempDir := t.TempDir()

	cfg := filepath.Join(tempDir, "vigiadev.yaml")
	_ = os.WriteFile(cfg, []byte("version: 1"), 0644)

	// Answering "yes\n"
	inReader := strings.NewReader("yes\n")
	err := commands.RunRemove(tempDir, false, false, "", inReader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// File should be deleted
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Errorf("expected vigiadev.yaml to be deleted on confirmation")
	}
}
