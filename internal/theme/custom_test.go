package theme

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	tint "github.com/lrstanley/bubbletint/v2"
)

// TestLoadCustomThemeFile_Partial tests loading a minimal theme (only fg/bg).
func TestLoadCustomThemeFile_Partial(t *testing.T) {
	dir := t.TempDir()
	themeJSON := `{
		"id": "minimal-dark",
		"fg": "#c0c0c0",
		"bg": "#1a1a1a"
	}`

	path := filepath.Join(dir, "minimal-dark.json")
	if err := os.WriteFile(path, []byte(themeJSON), 0600); err != nil {
		t.Fatal(err)
	}

	theme, err := LoadCustomThemeFile(path)
	if err != nil {
		t.Fatalf("LoadCustomThemeFile failed: %v", err)
	}

	if theme.ID != "minimal-dark" {
		t.Errorf("expected ID 'minimal-dark', got %q", theme.ID)
	}

	// fillDefaults should have populated all ANSI colors
	colors := map[string]*tint.Color{
		"Cursor":      theme.Cursor,
		"Black":       theme.Black,
		"Red":         theme.Red,
		"Green":       theme.Green,
		"Yellow":      theme.Yellow,
		"Blue":        theme.Blue,
		"Purple":      theme.Purple,
		"Cyan":        theme.Cyan,
		"White":       theme.White,
		"BrightBlack": theme.BrightBlack,
		"BrightRed":   theme.BrightRed,
		"BrightGreen": theme.BrightGreen,
	}
	for name, c := range colors {
		if c == nil {
			t.Errorf("fillDefaults should have set %s, got nil", name)
		}
	}

	// Cursor should default to Fg color
	if theme.Cursor.R != theme.Fg.R || theme.Cursor.G != theme.Fg.G || theme.Cursor.B != theme.Fg.B {
		t.Error("Cursor should default to Fg color")
	}

	// Bright variants should default to normal variants
	if theme.BrightBlack.R != theme.Black.R {
		t.Error("BrightBlack should default to Black")
	}
}

// TestLoadCustomThemeFile_IDFromFilename tests ID derivation from filename.
func TestLoadCustomThemeFile_IDFromFilename(t *testing.T) {
	dir := t.TempDir()
	themeJSON := `{
		"fg": "#ffffff",
		"bg": "#000000"
	}`

	path := filepath.Join(dir, "My-Cool-Theme.json")
	if err := os.WriteFile(path, []byte(themeJSON), 0600); err != nil {
		t.Fatal(err)
	}

	theme, err := LoadCustomThemeFile(path)
	if err != nil {
		t.Fatalf("LoadCustomThemeFile failed: %v", err)
	}

	if theme.ID != "my-cool-theme" {
		t.Errorf("expected ID 'my-cool-theme' (derived from filename), got %q", theme.ID)
	}
	if theme.DisplayName != "my-cool-theme" {
		t.Errorf("expected DisplayName 'my-cool-theme', got %q", theme.DisplayName)
	}
}

// TestLoadCustomThemes_IgnoresNonJSON tests that non-JSON files are skipped.
func TestLoadCustomThemes_IgnoresNonJSON(t *testing.T) {
	dir := t.TempDir()

	// Create non-JSON files
	for _, name := range []string{"readme.txt", "notes.md", ".hidden"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("not a theme"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := LoadCustomThemes(dir)
	if err != nil {
		t.Fatalf("LoadCustomThemes should not error: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected 0 loaded themes, got %d", len(loaded))
	}
}

// TestLoadCustomThemes_Registration tests that loaded themes appear in TintIDs().
func TestLoadCustomThemes_Registration(t *testing.T) {
	dir := t.TempDir()
	themeJSON := `{
		"id": "test-registration-unique",
		"fg": "#ffffff",
		"bg": "#000000"
	}`

	path := filepath.Join(dir, "test-registration-unique.json")
	if err := os.WriteFile(path, []byte(themeJSON), 0600); err != nil {
		t.Fatal(err)
	}

	// Initialize registry first
	tint.NewDefaultRegistry()

	loaded, err := LoadCustomThemes(dir)
	if err != nil {
		t.Fatalf("LoadCustomThemes failed: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 loaded theme, got %d", len(loaded))
	}

	// Check that the theme appears in the registry
	ids := tint.TintIDs()
	if !slices.Contains(ids, "test-registration-unique") {
		t.Error("custom theme 'test-registration-unique' not found in TintIDs()")
	}
}
