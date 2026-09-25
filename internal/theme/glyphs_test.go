package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/adrg/xdg"
)

// glyphsTempDir points XDG at a fresh directory and returns the glyphs
// directory inside it. xdg resolves the config home once at init, so setting
// the variable without reloading leaves it reading the real one.
func glyphsTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(xdg.Reload)
	t.Setenv("XDG_CONFIG_HOME", dir)
	xdg.Reload()
	glyphs := filepath.Join(dir, "tuios", "glyphs")
	if err := os.MkdirAll(glyphs, 0o755); err != nil {
		t.Fatal(err)
	}
	return glyphs
}

func TestAnInheritanceLoopResolvesRatherThanHangs(t *testing.T) {
	glyphs := glyphsTempDir(t)
	for _, pair := range [][2]string{{"a", "b"}, {"b", "a"}} {
		body := `{"inherits":"` + pair[1] + `","bullet":"` + pair[0] + `"}`
		if err := os.WriteFile(filepath.Join(glyphs, pair[0]+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ReloadGlyphSets()
	t.Cleanup(func() { SetActiveGlyphs(GlyphSetNone) })

	if got := ResolveGlyphSet("a").Bullet; got != "a" {
		t.Errorf("bullet = %q, want a: the nearer set wins and the loop stops", got)
	}
}

func TestABorderRuneWiderThanOneCellIsDropped(t *testing.T) {
	writeGlyphSet(t, "bad", map[string]any{
		"border": map[string]string{"top_left": "❌", "top_right": "╗"},
	})
	ReloadGlyphSets()
	set := ResolveGlyphSet("bad")
	if set.Border == nil {
		t.Fatal("border was dropped whole; only the bad rune should go")
	}
	if set.Border.TopLeft != "" {
		t.Errorf("top_left = %q, want it dropped", set.Border.TopLeft)
	}
	if set.Border.TopRight != "╗" {
		t.Errorf("top_right = %q, want the good rune kept", set.Border.TopRight)
	}
}

func TestEveryBuiltinSetIsDrawableWhereItSaysItIs(t *testing.T) {
	// A built-in never goes through the sanitizer a file does, because it is
	// written here rather than read from disk. So the widths a file would be
	// checked against are checked here instead: a built-in with a two-cell
	// glyph in a one-cell role would move a window control out from under the
	// pointer with nothing to catch it.
	for _, id := range []string{GlyphSetNone, "unicode", "heavy", "ascii"} {
		set := ResolveGlyphSet(id)
		probe := *set
		for _, problem := range sanitizeGlyphSet(&probe) {
			t.Errorf("built-in %s: %s", id, problem)
		}
		for role, glyph := range GlyphSetRoles(set) {
			if glyph == "" {
				continue
			}
			if set.ASCII && !overlay.IsASCII(glyph) {
				t.Errorf("%s: %s is %q, which is not 7-bit, but the set claims ascii", id, role, glyph)
			}
		}
	}
	if !ResolveGlyphSet("ascii").ASCII {
		t.Error("the ascii set does not measure as ascii")
	}
	if ResolveGlyphSet("heavy").ASCII {
		t.Error("the heavy set measures as ascii, which would offer it to a terminal that cannot draw it")
	}
}

func TestAMissingSetLeavesTheBuiltInsRatherThanBlankingTheChrome(t *testing.T) {
	writeGlyphSet(t, "present", map[string]any{"bullet": "\u25e6"})
	SetActiveGlyphs("nosuchset")
	if got := Glyphs().Bullet; got != "" {
		t.Errorf("bullet = %q, want empty so every accessor keeps its own default", got)
	}
}

// writeGlyphSet puts a set file in a temporary glyphs directory and points XDG
// at it, so a test exercises the same read path a user's file takes.
func writeGlyphSet(t *testing.T, id string, body map[string]any) {
	t.Helper()
	glyphs := glyphsTempDir(t)
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(glyphs, id+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { SetActiveGlyphs(GlyphSetNone) })
}
