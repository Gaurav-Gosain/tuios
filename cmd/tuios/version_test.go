package main

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// The two builds install under the same name, so the only thing that tells
// them apart is this line. It has to come from the build tag: a test that
// accepted any backend name would pass on a binary that names the wrong one.
func TestVersionNamesTheCompiledBackend(t *testing.T) {
	want := "[" + vt.Backend + " backend]"
	if got := versionReport(); !strings.Contains(got, want) {
		t.Errorf("version does not name the backend\ngot:\n%s\nwant substring: %s", got, want)
	}
}

func TestVersionFallsBackToEmbeddedStamps(t *testing.T) {
	tests := []struct {
		name                          string
		v, commit, date, by           string
		rev, when                     string
		dirty                         bool
		wantContains, wantNotContains []string
	}{
		{
			name:            "release ldflags win over the embedded stamps",
			v:               "v1.2.3",
			commit:          "abc123",
			date:            "2026-01-01T00:00:00Z",
			by:              "goreleaser",
			rev:             "def456",
			when:            "2025-01-01T00:00:00Z",
			wantContains:    []string{"v1.2.3", "Commit: abc123", "Built: 2026-01-01T00:00:00Z", "By: goreleaser"},
			wantNotContains: []string{"def456", "2025-01-01"},
		},
		{
			name:         "a local build reports the checkout it came from",
			v:            "dev",
			commit:       "none",
			date:         "unknown",
			by:           "unknown",
			rev:          "def456",
			when:         "2025-01-01T00:00:00Z",
			dirty:        true,
			wantContains: []string{"Commit: def456-dirty", "Built: 2025-01-01T00:00:00Z"},
		},
		{
			name:            "nothing to fall back to leaves the placeholders",
			v:               "dev",
			commit:          "none",
			date:            "unknown",
			by:              "unknown",
			wantContains:    []string{"Commit: none", "Built: unknown"},
			wantNotContains: []string{"dirty"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatVersion(tt.v, tt.commit, tt.date, tt.by, tt.rev, tt.when, tt.dirty)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
			for _, bad := range tt.wantNotContains {
				if strings.Contains(got, bad) {
					t.Errorf("unexpected %q in:\n%s", bad, got)
				}
			}
		})
	}
}
