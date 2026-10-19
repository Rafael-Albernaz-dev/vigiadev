package commands

import (
	"runtime/debug"
	"testing"
)

func TestResolveBuildMetadata(t *testing.T) {
	clean := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "65a2c1d40443f8421532a76df14b1d3c2df6cd13"},
		{Key: "vcs.time", Value: "2026-10-19T12:00:00Z"},
		{Key: "vcs.modified", Value: "false"},
	}}
	dirty := &debug.BuildInfo{Settings: append(append([]debug.BuildSetting(nil), clean.Settings[:2]...), debug.BuildSetting{Key: "vcs.modified", Value: "true"})}
	for _, tc := range []struct {
		name, commit, date, wantCommit, wantDate string
		info                                     *debug.BuildInfo
	}{
		{"unavailable", "none", "unknown", "none", "unknown", nil},
		{"no vcs", "none", "unknown", "none", "unknown", &debug.BuildInfo{}},
		{"clean", "none", "unknown", clean.Settings[0].Value, clean.Settings[1].Value, clean},
		{"dirty", "none", "unknown", clean.Settings[0].Value + "-dirty", clean.Settings[1].Value, dirty},
		{"ldflags", "release-hash", "release-date", "release-hash", "release-date", clean},
		{"partial commit", "release-hash", "unknown", "release-hash", clean.Settings[1].Value, clean},
		{"partial date", "none", "release-date", clean.Settings[0].Value, "release-date", clean},
		{"already dirty", "release-dirty", "release-date", "release-dirty", "release-date", dirty},
		{"missing revision", "none", "unknown", "none", "unknown", &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			commit, date := resolveBuildMetadata(tc.commit, tc.date, tc.info)
			if commit != tc.wantCommit || date != tc.wantDate {
				t.Fatalf("got %q %q, want %q %q", commit, date, tc.wantCommit, tc.wantDate)
			}
		})
	}
}
