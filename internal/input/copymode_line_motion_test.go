package input

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The line motions of copy mode (#980): 0 goes to column zero, ^ to the first
// nonblank character and $ to the last one, not to the blank padding up to
// the pane edge. The bound line actions do the same.

// lineMotionWindow is a pane in copy mode holding an indented line, a blank
// line and a line that ends in wide characters.
func lineMotionWindow(t *testing.T) *terminal.Window {
	t.Helper()
	w := multiPaneWindow(t, "lm", "line motions", "    alpha beta", "", "ab 中文")
	w.EnterCopyMode()
	w.CopyMode.ScrollOffset = 0
	return w
}

// lineKeysAt puts the copy cursor on row y, column x, presses keys and returns
// the column the cursor ends on.
func lineKeysAt(w *terminal.Window, y, x int, keys ...string) int {
	w.CopyMode.CursorY, w.CopyMode.CursorX = y, x
	for _, k := range keys {
		HandleCopyModeKey(multiKey(k), &app.OS{Settings: config.Global}, w)
	}
	return w.CopyMode.CursorX
}

func TestCopyModeLineMotionsStopAtTheText(t *testing.T) {
	w := lineMotionWindow(t)
	for _, tc := range []struct {
		name string
		y, x int
		keys []string
		want int
	}{
		{"0 goes to column zero", 0, 6, []string{"0"}, 0},
		{"^ skips the indentation", 0, 6, []string{"^"}, 4},
		{"$ stops on the last character", 0, 6, []string{"$"}, 13},
		{"^ in a selection", 0, 9, []string{"v", "^"}, 4},
		{"$ in a selection", 0, 6, []string{"v", "$"}, 13},
		{"0 in a selection", 0, 6, []string{"v", "0"}, 0},
		{"^ on a blank line", 1, 5, []string{"^"}, 0},
		{"$ on a blank line", 1, 5, []string{"$"}, 0},
		{"$ lands on the first cell of a wide character", 2, 0, []string{"$"}, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := lineKeysAt(w, tc.y, tc.x, tc.keys...); got != tc.want {
				t.Errorf("%v from row %d column %d put the cursor on column %d, want %d", tc.keys, tc.y, tc.x, got, tc.want)
			}
			if w.CopyMode.State != terminal.CopyModeNormal {
				lineKeysAt(w, tc.y, w.CopyMode.CursorX, "esc")
			}
		})
	}
}

func TestCopyModeLineActionsStopAtTheText(t *testing.T) {
	w := lineMotionWindow(t)
	for _, tc := range []struct {
		action string
		want   int
	}{
		{config.ActionCopyModeLineStart, 0},
		{config.ActionCopyModeFirstNonBlank, 4},
		{config.ActionCopyModeLineEnd, 13},
	} {
		w.CopyMode.CursorY, w.CopyMode.CursorX = 0, 6
		if !runCopyModeAction(tc.action, w.CopyMode, w, &copyModeEffects{}) {
			t.Fatalf("%s did not run", tc.action)
		}
		if got := w.CopyMode.CursorX; got != tc.want {
			t.Errorf("%s put the cursor on column %d, want %d", tc.action, got, tc.want)
		}
	}
}

// A row in the scrollback answers the same way as a row on the screen.
func TestCopyModeLineMotionsInTheScrollback(t *testing.T) {
	lines := []string{"  ab 中文"}
	for i := range 60 {
		lines = append(lines, fmt.Sprintf("row %d", i))
	}
	w := multiPaneWindow(t, "lm-sb", "scrollback", lines...)
	w.EnterCopyMode()
	if w.ScrollbackLen() == 0 {
		t.Fatal("the output did not reach the scrollback")
	}
	w.CopyMode.ScrollOffset = w.ScrollbackLen()
	w.ScrollbackOffset = w.CopyMode.ScrollOffset
	if got := lineKeysAt(w, 0, 6, "^"); got != 2 {
		t.Errorf("^ on the oldest scrollback row put the cursor on column %d, want 2", got)
	}
	if got := lineKeysAt(w, 0, 0, "$"); got != 7 {
		t.Errorf("$ on the oldest scrollback row put the cursor on column %d, want 7", got)
	}
}
