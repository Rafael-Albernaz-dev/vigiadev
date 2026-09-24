package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLog(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLogs_Dump(t *testing.T) {
	path := writeLog(t, t.TempDir(), "all.log", "2026-09-24T10:00:00Z\ta\tstdout\tone\n2026-09-24T10:00:01Z\tb\tstderr\ttwo\n")
	var out bytes.Buffer
	if err := streamLogFile(context.Background(), path, "all", 1, false, true, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "2026-09-24T10:00:01Z b two") || strings.Contains(got, "one") {
		t.Fatalf("unexpected tail dump: %q", got)
	}
}

func TestLogs_FilterService(t *testing.T) {
	dir := t.TempDir()
	a := writeLog(t, dir, "api.log", "api line\n")
	_ = writeLog(t, dir, "web.log", "web line\n")
	var out bytes.Buffer
	if err := streamLogFile(context.Background(), a, "api", 1, false, false, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "api line\n" {
		t.Fatalf("service logs were not filtered: %q", out.String())
	}
}

func TestLogs_Follow(t *testing.T) {
	path := writeLog(t, t.TempDir(), "api.log", "first\n")
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- streamLogFile(ctx, path, "api", 1, true, false, &out) }()
	deadline := time.Now().Add(time.Second)
	for out.String() == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("second\n")
	_ = f.Close()
	deadline = time.Now().Add(time.Second)
	for !strings.Contains(out.String(), "second") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("follow did not stop after cancellation")
	}
	if !strings.Contains(out.String(), "second") {
		t.Fatalf("follow missed appended line: %q", out.String())
	}
}
