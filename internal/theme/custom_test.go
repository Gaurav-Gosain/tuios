package theme

import (
	"os"
	"path/filepath"
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
