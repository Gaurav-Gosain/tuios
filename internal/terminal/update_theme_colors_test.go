package terminal

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestUpdateThemeColors_InvalidatesCache verifies that pushing a new theme into
// a window drops the cached render (both the content string and the styled
// layer) and marks the window dirty, so the next render repaints with the new
// palette instead of returning the stale cache.
func TestUpdateThemeColors_InvalidatesCache(t *testing.T) {
	w := &Window{
		Terminal:      vt.NewEmulator(80, 24),
		CachedContent: "stale content",
		Dirty:         false,
		ContentDirty:  false,
	}

	w.UpdateThemeColors()

	if w.CachedContent != "" {
		t.Errorf("CachedContent should be cleared after theme change, got %q", w.CachedContent)
	}
	if w.CachedLayer != nil {
		t.Error("CachedLayer should be nil after theme change")
	}
	if !w.Dirty || !w.ContentDirty {
		t.Error("window should be marked Dirty and ContentDirty after theme change")
	}
}

// TestUpdateThemeColors_NilTerminal makes sure a theme change on a window that
// is being torn down (Terminal already nil) does not panic on the UI goroutine.
func TestUpdateThemeColors_NilTerminal(t *testing.T) {
	w := &Window{CachedContent: "stale"}
	w.UpdateThemeColors()
	if w.CachedContent != "" {
		t.Errorf("CachedContent should still be cleared with a nil Terminal, got %q", w.CachedContent)
	}
}
