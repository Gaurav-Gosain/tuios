package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exists is what makes "write the file, then select it" one round trip. Before
// it, the registry was built once per process and a theme authored afterwards
// could not be reached without a restart.
func TestExistsSeesAThemeWrittenAfterStartup(t *testing.T) {
	EnsureRegistry()

	if Exists("written_later") {
		t.Fatal("the theme exists before it was written")
	}

	dir, err := GetThemesDir()
	if err != nil {
		t.Skipf("no themes directory: %v", err)
	}
	path := filepath.Join(dir, "written_later.json")
	if err := os.WriteFile(path, []byte(`{"id":"written_later","bg":"#000000","fg":"#ffffff"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	if !Exists("written_later") {
		t.Error("a theme file written after startup is still unreachable")
	}
}

// A malformed file has to come back as a sentence, not a log line: the caller
// that has to fix it is outside the process.
func TestReloadReportsAMalformedFile(t *testing.T) {
	dir, err := GetThemesDir()
	if err != nil {
		t.Skipf("no themes directory: %v", err)
	}
	path := filepath.Join(dir, "malformed.json")
	if err := os.WriteFile(path, []byte(`{"id":"malformed","fg":"not-a-colour"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	_, problems := ReloadCustomThemes()
	found := false
	for _, p := range problems {
		if strings.Contains(p, "malformed.json") {
			found = true
		}
	}
	if !found {
		t.Errorf("the malformed file was not reported; problems = %v", problems)
	}
	if Exists("malformed") {
		t.Error("a file that failed to parse registered anyway")
	}
}
