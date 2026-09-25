package main

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
	tint "github.com/lrstanley/bubbletint/v2"
)

// TestEveryThemeReachesTheBrowser runs the whole roster through. A colour sip
// refuses stops the server from starting, so one bad theme in 342 is a tuios
// that will not serve.
func TestEveryThemeReachesTheBrowser(t *testing.T) {
	theme.EnsureRegistry()
	t.Cleanup(func() { _ = theme.Initialize("") })

	var withNilSelection, withNilCursor int
	for _, cur := range tint.Tints() {
		th := browserTheme(cur)
		if err := th.Validate(); err != nil {
			t.Errorf("theme %s: %v", cur.ID, err)
		}
		if cur.SelectionBg == nil {
			withNilSelection++
			if th.SelectionBackground != "" {
				t.Errorf("theme %s sets no selection colour but sent %q", cur.ID, th.SelectionBackground)
			}
		}
		if cur.Cursor == nil {
			withNilCursor++
			if th.Cursor != "" || th.CursorAccent != "" {
				t.Errorf("theme %s sets no cursor but sent cursor %q accent %q", cur.ID, th.Cursor, th.CursorAccent)
			}
		}
	}
	if withNilSelection == 0 || withNilCursor == 0 {
		t.Errorf("no theme in the roster leaves a colour unset (%d without a selection, %d without a cursor), so this test proves nothing about the nil rule", withNilSelection, withNilCursor)
	}
	t.Logf("%d themes, %d without a selection colour, %d without a cursor colour", len(tint.Tints()), withNilSelection, withNilCursor)
}
