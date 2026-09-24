package commands

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/adapters/config"
)

func TestDoctor_PortConflicts(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	dir := t.TempDir()
	yaml := fmt.Sprintf("version: 1\nproject_name: doctor-test\nservices:\n  api:\n    command: [\"true\"]\n    ports: [%d]\n", port)
	path := filepath.Join(dir, "vigiadev.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	conflicts := findPortConflicts(cfg)
	if len(conflicts) != 1 || conflicts[0].Service != "api" || conflicts[0].Port != port {
		t.Fatalf("expected occupied port %d for api, got %+v", port, conflicts)
	}
	if !strings.Contains(conflicts[0].Service, "api") {
		t.Fatal("service attribution missing")
	}
}
