package commands

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rafael-Albernaz-dev/vigiadev/internal/domain"
	"github.com/Rafael-Albernaz-dev/vigiadev/internal/ui/stream"
)

func TestValidateNavigationURLs(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{
		{"http", "http://platform.atlas.localhost:8080/", true},
		{"https", "https://example.com/systems?view=all", true},
		{"ipv6", "http://[::1]:8080/", true},
		{"relative", "/systems", false},
		{"protocol relative", "//example.com/systems", false},
		{"missing host", "http:///systems", false},
		{"missing hostname", "http://:8080/", false},
		{"unsupported scheme", "ftp://example.com/", false},
		{"opaque", "http:systems", false},
		{"invalid port", "http://localhost:wrong/", false},
		{"invalid escape", "https://example.com/%zz", false},
		{"space", "http://example.com/a b", false},
		{"terminal control", "http://example.com/\x1b", false},
	} {
		for _, field := range []string{"service", "link"} {
			t.Run(tc.name+"/"+field, func(t *testing.T) {
				serviceURL, links := "", ""
				if field == "service" {
					serviceURL = fmt.Sprintf("    url: %q\n", tc.value)
				} else {
					links = fmt.Sprintf("links:\n  - name: Portal\n    url: %q\n", tc.value)
				}
				path := filepath.Join(t.TempDir(), "vigiadev.yaml")
				if err := os.WriteFile(path, []byte("version: 1\nproject_name: urls\n"+links+"services:\n  web:\n    command: [true]\n"+serviceURL), 0600); err != nil {
					t.Fatal(err)
				}
				oldConfig := cfgFile
				cfgFile = path
				t.Cleanup(func() { cfgFile = oldConfig })
				err := validateCmd.RunE(validateCmd, nil)
				if (err == nil) != tc.valid {
					t.Fatalf("valid=%v, error=%v", tc.valid, err)
				}
				if !tc.valid {
					wantField := `service "web" url`
					if field == "link" {
						wantField = `links[0] ("Portal") url`
					}
					if !strings.Contains(err.Error(), wantField) || !strings.Contains(err.Error(), "absolute http:// or https://") {
						t.Fatalf("missing actionable URL context: %v", err)
					}
					// The real up command must reject this before session/port operations.
					if err := upCmd.RunE(upCmd, nil); err == nil || !strings.Contains(err.Error(), "failed to load configuration") {
						t.Fatalf("up did not reject invalid navigation at load time: %v", err)
					}
				}
			})
		}
	}
}

func TestNavigationOptionalServiceURLAndRequiredLinkURL(t *testing.T) {
	for _, tc := range []struct {
		links string
		valid bool
	}{
		{"", true},
		{"links:\n  - name: Missing URL\n", false},
	} {
		path := filepath.Join(t.TempDir(), "vigiadev.yaml")
		if err := os.WriteFile(path, []byte("version: 1\nproject_name: legacy\n"+tc.links+"services:\n  web:\n    command: [true]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		oldConfig := cfgFile
		cfgFile = path
		err := validateCmd.RunE(validateCmd, nil)
		cfgFile = oldConfig
		if (err == nil) != tc.valid {
			t.Fatalf("valid=%v, error=%v", tc.valid, err)
		}
	}
}

func TestNavigationShortcutsStreamAndProfile(t *testing.T) {
	links := []domain.LinkConfig{{Name: "Catálogo", URL: "http://platform.atlas.localhost:8080/systems"}}
	var out bytes.Buffer
	sv := stream.NewStreamView(&out)
	sv.PrintLinks(nil)
	if out.Len() != 0 {
		t.Fatalf("legacy stream output changed: %q", out.String())
	}
	sv.PrintLinks(links)
	if want := "NAVIGATION SHORTCUTS\n  Catálogo: http://platform.atlas.localhost:8080/systems\n\n"; out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
	cfg := &domain.VigiaConfig{Links: links, Profiles: map[string][]string{"web": {"web"}}, Services: map[string]domain.ServiceConfig{
		"web": {Command: []string{"true"}, URL: links[0].URL, Ports: []int{5174}},
	}}
	filtered, err := ResolveProfilesAndServicesWithAll(cfg, "web", nil, strings.NewReader(""), &out, false, false)
	if err != nil || len(filtered.Links) != 1 || filtered.Services["web"].URL != links[0].URL {
		t.Fatalf("profile lost navigation: %+v, %v", filtered, err)
	}
}
