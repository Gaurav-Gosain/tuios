package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestAGlyphSetInheritsWhatItDoesNotSay(t *testing.T) {
	writeGlyphSet(t, "mine", map[string]any{
		"inherits": "heavy",
		"bullet":   "◦",
	})
	if _, problems := ReloadGlyphSets(); len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}

	set := ResolveGlyphSet("mine")
	if set.Bullet != "◦" {
		t.Errorf("bullet = %q, want the set's own ◦", set.Bullet)
	}
	if set.Rule != "━" {
		t.Errorf("rule = %q, want heavy's ━ carried through the inherit", set.Rule)
	}
	if set.Border == nil || set.Border.TopLeft != "┏" {
		t.Errorf("border = %+v, want heavy's corners carried through", set.Border)
	}
}

func TestAGlyphTheLayoutCannotFitIsDroppedAndReported(t *testing.T) {
	// A two-cell close would shift every cell after it in the title bar, so the
	// button would no longer sit under the rectangle the pointer is tested
	// against. Dropped rather than drawn, and said out loud rather than
	// silently, because on screen it looks like a set that half applied.
	writeGlyphSet(t, "wide", map[string]any{"close": "❌", "ellipsis": "..>"})
	_, problems := ReloadGlyphSets()

	set := ResolveGlyphSet("wide")
	if set.Close != "" {
		t.Errorf("close = %q, want it dropped for being two cells", set.Close)
	}
	if set.Ellipsis != "..>" {
		t.Errorf("ellipsis = %q, want it kept: the role has no width to keep", set.Ellipsis)
	}
	var said bool
	for _, p := range problems {
		if strings.Contains(p, "close") && strings.Contains(p, "2 cells") {
			said = true
		}
	}
	if !said {
		t.Errorf("problems = %v, want one naming close and its width", problems)
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

func TestSelectingASetPushesTheOverlayFamilysShare(t *testing.T) {
	// The overlay package depends on nothing inside tuios so that it can be
	// lifted out, so it cannot read the registry and has to be told.
	writeGlyphSet(t, "arrows", map[string]any{"ellipsis": "~", "arrow_left": "«"})
	ReloadGlyphSets()

	SetActiveGlyphs("arrows")
	if got := overlay.Ellipsis(); got != "~" {
		t.Errorf("overlay.Ellipsis() = %q, want the set's ~", got)
	}
	if got := overlay.ArrowLeft(); got != "«" {
		t.Errorf("overlay.ArrowLeft() = %q, want the set's «", got)
	}

	SetActiveGlyphs(GlyphSetNone)
	if got := overlay.Ellipsis(); got != "…" {
		t.Errorf("overlay.Ellipsis() = %q, want the built-in back", got)
	}
}

func TestASetsNonASCIIGlyphLosesToASCIIModePerRole(t *testing.T) {
	// Per role rather than per set: a hand-written set is mostly 7-bit and
	// giving the whole of it up over one arrow is a worse answer than giving up
	// the arrow.
	writeGlyphSet(t, "mixed", map[string]any{"ellipsis": "..", "arrow_left": "«"})
	ReloadGlyphSets()
	SetActiveGlyphs("mixed")

	overlay.SetASCII(true)
	t.Cleanup(func() { overlay.SetASCII(false) })

	if got := overlay.Ellipsis(); got != ".." {
		t.Errorf("overlay.Ellipsis() = %q, want the set's .. kept: it is 7-bit", got)
	}
	if got := overlay.ArrowLeft(); got != "<" {
		t.Errorf("overlay.ArrowLeft() = %q, want the ASCII default: « is not", got)
	}
}

func TestAnUnknownSetIsRefusedRatherThanSilentlyIgnored(t *testing.T) {
	writeGlyphSet(t, "real", map[string]any{"bullet": "◦"})
	ReloadGlyphSets()
	if !GlyphSetExists("real") {
		t.Error("GlyphSetExists(real) = false, want true")
	}
	if GlyphSetExists("nope") {
		t.Error("GlyphSetExists(nope) = true, want false")
	}
	if !GlyphSetExists(GlyphSetNone) {
		t.Error("GlyphSetExists(default) = false; the default always resolves")
	}
}

func TestEveryBuiltinSetIsDrawableWhereItSaysItIs(t *testing.T) {
	for _, id := range []string{GlyphSetNone, "unicode", "heavy", "ascii"} {
		set := ResolveGlyphSet(id)
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
