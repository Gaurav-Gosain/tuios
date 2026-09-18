package theme

import (
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	tint "github.com/lrstanley/bubbletint/v2"
)

// useTheme registers a loaded theme and makes it current, restoring the
// previous state when the test ends.
func useTheme(t *testing.T, id string) func() {
	t.Helper()
	EnsureRegistry()
	wasEnabled := enabled
	prev := ""
	if cur := tint.Current(); cur != nil {
		prev = cur.ID
	}
	enabled = true
	if !tint.SetTintID(id) {
		t.Fatalf("theme %q did not register", id)
	}
	return func() {
		if prev != "" {
			tint.SetTintID(prev)
		}
		enabled = wasEnabled
	}
}

// writeThemeFile writes a theme JSON and loads it, returning its id.
func writeThemeFile(t *testing.T, body map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "amber.json")
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	tn, err := LoadCustomThemeFile(path)
	if err != nil {
		t.Fatalf("LoadCustomThemeFile: %v", err)
	}
	EnsureRegistry()
	tint.Register(tn)
	t.Cleanup(func() { registerChrome(tn.ID, nil) })
	return tn.ID
}

func hexOf(t *testing.T, c color.Color) string {
	t.Helper()
	if c == nil {
		return "<nil>"
	}
	r, g, b, _ := c.RGBA()
	const hex = "0123456789abcdef"
	out := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range []uint32{r >> 8, g >> 8, b >> 8} {
		out[1+i*2] = hex[(v>>4)&0xf]
		out[2+i*2] = hex[v&0xf]
	}
	return string(out)
}

// TestAThemeNamesItsOwnAccent is the feature from #186: a palette whose accent
// is amber should get amber chrome without amber landing in bright_blue, where
// it would recolour every bold blue a program prints inside a pane.
//
// Negative control: dropping the chrome arm from UI() left the accent at the
// theme's bright_blue and this failed.
func TestAThemeNamesItsOwnAccent(t *testing.T) {
	id := writeThemeFile(t, map[string]any{
		"id":          "amber",
		"bright_blue": "#5c5cff",
		"chrome":      map[string]string{"accent": "#ffb454"},
	})
	restore := useTheme(t, id)
	defer restore()

	if got := hexOf(t, UI().Accent); got != "#ffb454" {
		t.Errorf("UI().Accent = %s, want the named #ffb454", got)
	}
	if got := hexOf(t, DockColorWindow()); got != "#ffb454" {
		t.Errorf("DockColorWindow() = %s, want the named #ffb454", got)
	}
	// The pane palette is untouched: that is the whole point.
	if got := hexOf(t, Current().BrightBlue); got != "#5c5cff" {
		t.Errorf("bright_blue = %s, want the theme's own #5c5cff", got)
	}
}

// TestAnAbsentChromeFieldStillDerives keeps every theme written before this
// existed rendering exactly as it did.
func TestAnAbsentChromeFieldStillDerives(t *testing.T) {
	id := writeThemeFile(t, map[string]any{
		"id":           "partial",
		"bright_blue":  "#5c5cff",
		"bright_green": "#00ff00",
		"yellow":       "#cdcd00",
		"chrome":       map[string]string{"accent": "#ffb454"},
	})
	restore := useTheme(t, id)
	defer restore()

	if got := hexOf(t, DockColorTerminal()); got != "#00ff00" {
		t.Errorf("DockColorTerminal() = %s, want the derived bright_green", got)
	}
	if got := hexOf(t, DockColorCopy()); got != "#cdcd00" {
		t.Errorf("DockColorCopy() = %s, want the derived yellow", got)
	}
}

// TestAThemeWithNoChromeObjectIsUnchanged is the same guarantee one level up:
// a file with no chrome at all registers none, so every role derives.
func TestAThemeWithNoChromeObjectIsUnchanged(t *testing.T) {
	id := writeThemeFile(t, map[string]any{
		"id":          "plain",
		"bright_blue": "#5c5cff",
	})
	restore := useTheme(t, id)
	defer restore()

	if c := CurrentChrome(); c != nil {
		t.Fatalf("a theme with no chrome object registered %+v", c)
	}
	if got := hexOf(t, UI().Accent); got != "#5c5cff" {
		t.Errorf("UI().Accent = %s, want the derived bright_blue", got)
	}
}

// TestABadChromeColourCostsOnlyThatColour keeps a typo cheap. A theme is a file
// someone edits by hand, and losing the whole palette over one bad string is a
// worse answer than losing one role to the derivation it already had.
func TestABadChromeColourCostsOnlyThatColour(t *testing.T) {
	id := writeThemeFile(t, map[string]any{
		"id":          "typo",
		"bright_blue": "#5c5cff",
		"yellow":      "#cdcd00",
		"chrome":      map[string]string{"accent": "#ffb454", "warning": "not a colour"},
	})
	restore := useTheme(t, id)
	defer restore()

	if got := hexOf(t, UI().Accent); got != "#ffb454" {
		t.Errorf("UI().Accent = %s, want the good #ffb454", got)
	}
	if got := hexOf(t, DockColorCopy()); got != "#cdcd00" {
		t.Errorf("DockColorCopy() = %s, want the derived yellow after a bad warning", got)
	}
}

func TestParseChromeColorSpellings(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"#ffb454", true},
		{"ffb454", true},
		{"#fb4", true},
		{"", false},
		{"   ", false},
		{"not a colour", false},
		{"#ggg", false},
		{"#ffb4544", false},
	} {
		got := parseChromeColor(tc.in) != nil
		if got != tc.want {
			t.Errorf("parseChromeColor(%q) parsed = %v, want %v", tc.in, got, tc.want)
		}
	}
}
