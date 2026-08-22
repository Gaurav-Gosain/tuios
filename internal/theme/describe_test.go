package theme

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Describe has to read the theme's own colours, not the active one's. A caller
// comparing the theme it asked for against the theme in effect gets the same
// answer twice otherwise, which is the failure it was trying to detect.
func TestDescribeReadsTheNamedTheme(t *testing.T) {
	EnsureRegistry()

	mocha, ok := Describe("catppuccin_mocha")
	if !ok {
		t.Fatal("catppuccin_mocha is not registered")
	}
	latte, ok := Describe("catppuccin_latte")
	if !ok {
		t.Fatal("catppuccin_latte is not registered")
	}
	if mocha.Bg == latte.Bg {
		t.Fatalf("both themes describe as background %s", mocha.Bg)
	}
	if !mocha.Dark || latte.Dark {
		t.Errorf("dark is wrong: mocha %v, latte %v", mocha.Dark, latte.Dark)
	}
	if len(mocha.Swatches) != 18 {
		t.Errorf("got %d swatches, want the foreground, the cursor and the sixteen", len(mocha.Swatches))
	}
}

// The point of the ratios is that a colour nobody can see is reported as one.
func TestDescribeReportsAnIllegibleSwatch(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "invisible.json", `{
		"id": "invisible",
		"bg": "#000000",
		"fg": "#ffffff",
		"red": "#010101"
	}`)
	registerFrom(t, dir, "invisible.json")

	p, ok := Describe("invisible")
	if !ok {
		t.Fatal("the theme did not register")
	}
	if !slices.Contains(p.Illegible, "red") {
		t.Errorf("#010101 on #000000 was not called illegible; illegible = %v", p.Illegible)
	}
	if slices.Contains(p.Illegible, "fg") {
		t.Errorf("white on black was called illegible; illegible = %v", p.Illegible)
	}
	for _, s := range p.Swatches {
		if s.Name == "red" && s.Ratio > 1.1 {
			t.Errorf("red measured %v:1 against its own background", s.Ratio)
		}
	}
}

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

func writeTheme(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func registerFrom(t *testing.T, dir, name string) {
	t.Helper()
	if _, err := LoadCustomThemes(dir); err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
}
