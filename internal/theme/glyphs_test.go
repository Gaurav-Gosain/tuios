package theme

import (
	"os"
	"path/filepath"
	"testing"

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
