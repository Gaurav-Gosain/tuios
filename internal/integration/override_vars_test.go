package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var dirVarRe = regexp.MustCompile(`(?:dirFromEnv|\.env)\("([A-Z_]+(?:_HOME|_DIR|APPDATA))"`)

func TestE2EClearsEveryDirOverride(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var read []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range dirVarRe.FindAllStringSubmatch(string(data), -1) {
			if !slices.Contains(read, m[1]) {
				read = append(read, m[1])
			}
		}
	}
	for _, known := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME"} {
		if !slices.Contains(read, known) {
			t.Fatalf("the scan did not find %s, so it finds nothing it should: %v", known, read)
		}
	}

	harness, err := os.ReadFile(filepath.Join("..", "..", "e2e", "tui", "harness_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	cleared := append(stringList(t, string(harness), "harnessDirKeys"), stringList(t, string(harness), "xdgKeys")...)
	for _, v := range read {
		if !slices.Contains(cleared, v) {
			t.Errorf("internal/integration reads %s for a directory, and e2e/tui/harness_test.go neither clears it (harnessDirKeys) nor redirects it (xdgKeys)", v)
		}
	}
}

func stringList(t *testing.T, src, name string) []string {
	t.Helper()
	start := strings.Index(src, "var "+name+" = []string{")
	if start < 0 {
		t.Fatalf("no %s list in e2e/tui/harness_test.go", name)
	}
	block := src[start:]
	block = block[:strings.Index(block, "\n}")]
	var out []string
	for _, m := range regexp.MustCompile(`"([A-Z_]+)"`).FindAllStringSubmatch(block, -1) {
		out = append(out, m[1])
	}
	return out
}
