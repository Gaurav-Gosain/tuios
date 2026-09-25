package capture

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAnUnreadableDirectoryIsNotFatal keeps the walk forgiving. A font
// directory that cannot be read is one place with no fonts in it.
func TestAnUnreadableDirectoryIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "truncated.ttf")
	if err := os.WriteFile(junk, []byte("not a font"), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := buildFontIndex([]string{
		filepath.Join(dir, "does-not-exist"),
		dir,
		"/proc/nonexistent/fonts",
	})
	if idx == nil {
		t.Fatal("the scan gave up entirely")
	}
	if len(idx.byFamily) != 0 {
		t.Errorf("a directory of junk produced %d families", len(idx.byFamily))
	}
}
