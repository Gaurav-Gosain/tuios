package lint

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// paneEnvLiteral matches a string literal that names one of the variables tuios
// sets in every pane. Every reader takes the name from internal/envvars, so a
// rename or a removal is one edit and the table stays the one list (S25). A
// literal for TUIOS_PANE alone is a prefix test and is not matched.
var paneEnvLiteral = regexp.MustCompile("[\"`]TUIOS_(PANE_ID|WINDOW_ID|WINDOW_NAME|SESSION|SESSION_REMOTE|SOCKET|PANE_TOKEN|PANE_GRANTS|PANE_HOSTED|PANE_TTY|RESTORED|HOST)=?[\"`]")

// TestPaneEnvNamesComeFromTheTable fails on a non-test Go file outside
// internal/envvars that spells a pane variable as a literal.
func TestPaneEnvNamesComeFromTheTable(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", ".dev", "testdata", "node_modules":
				return fs.SkipDir
			}
			if rel == "e2e" || rel == filepath.Join("internal", "envvars") {
				return fs.SkipDir
			}
			return nil
		}
		// testutil cannot import envvars: envvars tests import testutil.
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || rel == filepath.Join("internal", "testutil", "xdgisolate.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range paneEnvLiteral.FindAllString(string(data), -1) {
			t.Errorf("%s spells %s as a literal: use the constant from internal/envvars", rel, m)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestPaneEnvLintCatchesALiteral keeps the pattern honest.
func TestPaneEnvLintCatchesALiteral(t *testing.T) {
	for _, bad := range []string{`os.Getenv("TUIOS_PANE_ID")`, "`TUIOS_SESSION`", `"TUIOS_SOCKET="`} {
		if !paneEnvLiteral.MatchString(bad) {
			t.Errorf("%s is not caught", bad)
		}
	}
	for _, ok := range []string{`"TUIOS_PANE"`, `envvars.PaneID`, `"TUIOS_SESSION_NAME"`} {
		if paneEnvLiteral.MatchString(ok) {
			t.Errorf("%s is wrongly caught", ok)
		}
	}
}
