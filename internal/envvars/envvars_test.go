package envvars

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }

// notVariables are literals that begin with TUIOS_ and are not a variable: a
// prefix tested with HasPrefix.
var notVariables = map[string]bool{"TUIOS_PANE": true}

// TestEveryVariableInTheCodeIsInTheTable reads the non-test Go source and
// fails on a TUIOS_ variable that the table lacks, which is how a variable
// reaches users with no page: S20 found about forty. It also fails on a table
// entry that no code names, so the table cannot keep a variable that is gone.
func TestEveryVariableInTheCodeIsInTheTable(t *testing.T) {
	root := filepath.Join("..", "..")
	lit := regexp.MustCompile("[\"`](TUIOS_[A-Z0-9_]+)(=[^\"`]*)?[\"`]|[\"`](TUIOS_[A-Z0-9_]+)=")
	seen := map[string]string{}
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
			if rel == "e2e" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasPrefix(rel, "internal/envvars") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range lit.FindAllStringSubmatch(string(data), -1) {
			name := m[1]
			if name == "" {
				name = m[3]
			}
			if _, ok := seen[name]; !ok {
				seen[name] = rel
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) < 100 {
		t.Fatalf("found %d variables in the code, want over a hundred: the scan is broken", len(seen))
	}
	for name, where := range seen {
		if notVariables[name] {
			continue
		}
		if _, ok := ByName(name); !ok {
			t.Errorf("%s (%s) is not in the table: add it to internal/envvars/envvars.go and run go generate ./internal/envvars", name, where)
		}
	}
	for _, v := range All {
		if _, ok := seen[v.Name]; !ok && !slices.Contains(pairedNames, v.Name) {
			t.Errorf("%s is in the table and in no code: remove it", v.Name)
		}
	}
}

// pairedNames are table names the scan cannot see as a literal because the
// constants of this package carry them.
var pairedNames = []string{PaneID, WindowID, WindowName, Session, SessionRemote, Socket, PaneToken, PaneGrants, PaneHosted, PaneTTY, Restored, Host}

// TestTableIsWellFormed holds the table to its own rules: sorted names with no
// repeat, a kind, a meaning and no leftover marker.
func TestTableIsWellFormed(t *testing.T) {
	for i, v := range All {
		if i > 0 && All[i-1].Name >= v.Name {
			t.Errorf("%s is out of order or repeated after %s", v.Name, All[i-1].Name)
		}
		if !strings.HasPrefix(v.Name, "TUIOS_") || v.Kind == "" || v.Meaning == "" || v.Readers == "" {
			t.Errorf("%s: name, kind, meaning and readers are all required", v.Name)
		}
	}
}

// TestEnvironmentPageIsCurrent fails when docs/ENVIRONMENT.md is not what the
// table generates.
func TestEnvironmentPageIsCurrent(t *testing.T) {
	got, err := os.ReadFile(filepath.Join("..", "..", "docs", "ENVIRONMENT.md"))
	if err != nil {
		t.Fatalf("docs/ENVIRONMENT.md: %v: run go generate ./internal/envvars", err)
	}
	if string(got) != Page() {
		t.Error("docs/ENVIRONMENT.md is out of date: run go generate ./internal/envvars")
	}
}
