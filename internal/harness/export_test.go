package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Seams the tests reach for. Production code calls the functions these wrap.

// ParseOptions reads the numbered menu nearest the bottom of lines: options
// numbered 1 to N with at most maxOptionGap other lines between two of them.
// A run that does not count down to 1 is not a menu and gives nothing, which
// is what keeps a numbered list in the transcript above from posing as one.
func ParseOptions(lines []string) []Option {
	out, _ := parseOptions(lines)
	return out
}

// manifestHead is the part of a test manifest every case shares.
const manifestHead = "schema_version = 1\nid = \"x-agent\"\n[detect]\ncomm = [\"x-agent\"]\n"

// mustManifest loads one manifest written in a test, next to the bundled
// ones, and fails the test when it does not load.
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

// testRegistry is the bundled manifests, which must all load.
func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r, errs := Load("")
	for _, err := range errs {
		t.Fatalf("the bundled manifests must parse: %v", err)
	}
	return r
}

// fixtureSeparator splits the fixtures of one file, as in the library's
// screen fixtures.
const fixtureSeparator = "\n=====\n"

// fixtureTail is the last n non-empty lines of a screen, bottom first, the
// way a pane's tail is read. It is the library's test helper of that name.
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
