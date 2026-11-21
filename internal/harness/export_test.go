package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func ParseOptions(lines []string) []Option {
	out, _ := parseOptions(lines)
	return out
}

const manifestHead = "schema_version = 1\nid = \"x-agent\"\n[detect]\ncomm = [\"x-agent\"]\n"

func mustManifest(t *testing.T, body string) *Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.toml")
	if err := os.WriteFile(path, []byte(manifestHead+body), 0o644); err != nil {
		t.Fatal(err)
	}
	r, errs := Load(dir)
	for _, e := range errs {
		if e.Source == path {
			t.Fatalf("manifest refused: %v", e.Err)
		}
	}
	return r
}

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r, errs := Load("")
	for _, err := range errs {
		t.Fatalf("the bundled manifests must parse: %v", err)
	}
	return r
}

const fixtureSeparator = "\n=====\n"

func fixtureTail(screen []string, n int) []string {
	var out []string
	for i := len(screen) - 1; i >= 0 && len(out) < n; i-- {
		if line := strings.TrimRight(screen[i], " \t"); line != "" {
			out = append(out, line)
		}
	}
	slices.Reverse(out)
	return out
}
