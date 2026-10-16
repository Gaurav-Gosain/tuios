package theme

import (
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"strings"
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

// TestParseChromeColorSpellings: a chrome field reads every form ParseColor
// does, and keeps the bare hex theme files were written with before it.
func TestParseChromeColorSpellings(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"#ffb454", true},
		{"ffb454", true},
		{"#fb4", true},
		{"bright_red", true},
		{"accent", true},
		{"charple", true},
		{"", false},
		{"   ", false},
		{"not a colour", false},
		{"#ggg", false},
		{"#ffb4544", false},
		// An ink tier is measured from the surface, never set.
		{"text", false},
	} {
		got := !parseChromeSpec(tc.in).IsZero()
		if got != tc.want {
			t.Errorf("parseChromeSpec(%q) parsed = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestChromeNamesSlotsAndRoles: a chrome field may name one of the theme's
// own slots or another field, in any order, and a field that names a role the
// file leaves unset takes the slot that role derives from.
//
// Negative control: resolving references in file order (one pass, no
// recursion) leaves info unset, because accent is read after it.
func TestChromeNamesSlotsAndRoles(t *testing.T) {
	id := writeThemeFile(t, map[string]any{
		"id":            "refs",
		"bright_purple": "#bb9af7",
		"yellow":        "#e0af68",
		"chrome": map[string]string{
			"info":    "accent",
			"accent":  "bright_magenta",
			"success": "warning",
		},
	})
	restore := useTheme(t, id)
	defer restore()

	c := CurrentChrome()
	if c == nil {
		t.Fatal("the chrome object did not load")
	}
	for _, tc := range []struct {
		role string
		got  color.Color
		want string
	}{
		{"accent", c.Accent, "#bb9af7"},
		{"info", c.Info, "#bb9af7"},
		{"success", c.Success, "#e0af68"},
	} {
		if got := hexOf(t, tc.got); got != tc.want {
			t.Errorf("chrome.%s = %s, want %s", tc.role, got, tc.want)
		}
	}
}

// TestAChromeLoopFailsTheFile: roles that name each other have no colour to
// end on. The file fails to load with an error that names the roles, rather
// than recursing until the stack runs out.
func TestAChromeLoopFailsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loop.json")
	data := `{"id": "loop", "chrome": {"accent": "success", "success": "accent"}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCustomThemeFile(path)
	if err == nil {
		t.Fatal("a loop of chrome roles loaded")
	}
	for _, name := range []string{"chrome.accent", "chrome.success"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not name %s: %v", name, err)
		}
	}
}
