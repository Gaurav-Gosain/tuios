package app_test

import (
	"reflect"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/apptest"
)

func cursorFixture(t *testing.T) (f *apptest.Fixture, x, y int) {
	t.Helper()
	f = apptest.New(t, apptest.Panes(1))
	f.Output("prompt$ ")
	x, y, ok := f.Cursor()
	if !ok {
		t.Fatal("no cursor before the panel opens; the test proves nothing")
	}
	return f, x, y
}

// TestOverlayDrawsNoCursor: the terminal's cursor is hidden while the command
// palette is open, and shown again once it closes. Negative control: drop the
// AnyOverlayOpen check from paneCursor.
func TestOverlayDrawsNoCursor(t *testing.T) {
	f, _, _ := cursorFixture(t)

	f.Keys("ctrl+p")
	if x, y, ok := f.Cursor(); ok {
		t.Errorf("cursor drawn at (%d,%d) while the command palette is open", x, y)
	}

	f.Keys("esc")
	if _, _, ok := f.Cursor(); !ok {
		t.Error("cursor not restored after the palette closed")
	}
}

// TestOverlayDrawsNoFakeCursorEither: hiding the terminal's cursor must not
// hand the job to the cell loop, which paints a cursor of its own whenever
// the terminal's is off. That would move the mark rather than remove it. The
// cell where the cursor was is drawn like the blank cell beside it. Negative
// control: drop the overlay term from drawFakeCursor in renderTerminal.
func TestOverlayDrawsNoFakeCursorEither(t *testing.T) {
	f, x, y := cursorFixture(t)

	f.Keys("ctrl+p")
	if _, _, ok := f.Cursor(); ok {
		t.Fatal("the palette did not hide the terminal's cursor; TestOverlayDrawsNoCursor says why")
	}
	f.Output("\x1b[m")
	at, beside := f.Cell(x, y), f.Cell(x+1, y)
	if at.Content != " " && at.Content != "" {
		t.Fatalf("the palette covers the cursor's cell (%q at %d,%d); the test needs it visible", at.Content, x, y)
	}
	if !reflect.DeepEqual(at.BG, beside.BG) || !reflect.DeepEqual(at.FG, beside.FG) || at.Attrs != beside.Attrs {
		t.Errorf("the cell under the palette where the cursor was is drawn as a cursor: %+v, the blank beside it %+v", at, beside)
	}
}
