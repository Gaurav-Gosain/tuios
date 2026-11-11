package app_test

import (
	"fmt"
	"image/color"
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/apptest"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The accent colour picker, opened from a pane's menu and driven with its
// keys, read off the panel and off the rail row it previews on. The hue
// strip's drag, the rail cache key and its allocation budget stay in package
// app: they read the picker's hit boxes and the rail's signature, which no
// screen shows.

func accentFixture(t *testing.T) *apptest.Fixture {
	t.Helper()
	return apptest.New(t, apptest.Panes(1), apptest.Size(120, 30),
		apptest.Config(func(c *config.UserConfig) { c.Appearance.FramePanels = true }))
}

// openAccentPicker opens the picker from the focused pane's menu.
func openAccentPicker(t *testing.T, f *apptest.Fixture) {
	t.Helper()
	px, py := f.PaneCenter()
	openMenu(f, px, py)
	box, _, rows := menuOnScreen(t, f)
	f.Click(box.X0+4, rowLabeled(t, rows, "Accent color").y)
	if shownHex(t, f) == "" {
		t.Fatalf("the accent picker did not open:\n%s", f.Text())
	}
}

// hexField reads the hex field; a focused field carries a › before the #.
var hexField = regexp.MustCompile(`hex +›?(#[0-9a-f]{6})`)

// shownHex is the colour the picker's hex field shows, "" with no picker.
func shownHex(t *testing.T, f *apptest.Fixture) string {
	t.Helper()
	if m := hexField.FindStringSubmatch(f.Text()); m != nil {
		return m[1]
	}
	return ""
}

// railMark is the colour of the identity mark on the pane's rail row.
func railMark(t *testing.T, f *apptest.Fixture) string {
	t.Helper()
	for y, row := range f.Screen() {
		if before, _, ok := strings.Cut(row, "▎  pane 1"); ok {
			return hexOf(f.Cell(len([]rune(before)), y).FG)
		}
	}
	t.Fatalf("no rail row for pane 1:\n%s", f.Text())
	return ""
}

func hexOf(c color.Color) string {
	if c == nil {
		return "none"
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// storedAccent is the accent the pane keeps, "" for none. It is what the
// rail draws once the picker is shut.
func storedAccent(f *apptest.Fixture) string {
	if a, ok := f.M.WindowAccent(f.M.GetFocusedWindow().ID); ok {
		return a.Hex()
	}
	return ""
}

// TestAccentPickerPreviewsOnTheRail: the rail row of the pane the picker is
// for shows the colour under the cursor as it moves, nothing is stored until
// it is applied, and closing puts the row back. Negative control: drop the
// picker's preview from the rail's mark colour.
func TestAccentPickerPreviewsOnTheRail(t *testing.T) {
	f := accentFixture(t)
	resting := railMark(t, f)

	openAccentPicker(t, f)
	f.Keys("right")
	first := shownHex(t, f)
	if got := railMark(t, f); got != first {
		t.Errorf("the rail row shows %s, the picker's cursor is on %s", got, first)
	}
	f.Keys("right")
	if second := shownHex(t, f); second == first {
		t.Fatal("a move on the grid did not change the colour")
	} else if got := railMark(t, f); got != second {
		t.Errorf("after a move the rail row shows %s, the picker's cursor is on %s", got, second)
	}
	if got := storedAccent(f); got != "" {
		t.Errorf("the preview stored %s before anything was applied", got)
	}

	f.Keys("esc")
	if got := railMark(t, f); got != resting {
		t.Errorf("closing the picker left the rail row at %s, it was %s", got, resting)
	}
}

// TestAccentPickerCancelRestores: esc puts back exactly the colour the pane
// had, however far the cursor went, and enter stores exactly the colour
// under the cursor. Negative control: make CloseAccentPicker keep the
// preview, or make AccentPickerApply store the colour the picker opened on.
func TestAccentPickerCancelRestores(t *testing.T) {
	f := accentFixture(t)
	openAccentPicker(t, f)
	f.Type("336699")
	f.Keys("enter")
	if got := storedAccent(f); got != "#336699" {
		t.Fatalf("typing 336699 and enter stored %q", got)
	}

	openAccentPicker(t, f)
	f.Keys("right", "down", "right")
	if shownHex(t, f) == "#336699" {
		t.Fatal("the moves did not change the colour, so cancelling proves nothing")
	}
	f.Keys("esc")
	if got := storedAccent(f); got != "#336699" {
		t.Errorf("esc left the accent as %q, want the exact colour it had, #336699", got)
	}
	if got := railMark(t, f); got != "#336699" {
		t.Errorf("after esc the rail row shows %s, want #336699", got)
	}

	openAccentPicker(t, f)
	f.Keys("right", "down")
	want := shownHex(t, f)
	f.Keys("enter")
	if shownHex(t, f) != "" {
		t.Error("enter left the picker open")
	}
	if got := storedAccent(f); got != want {
		t.Errorf("enter stored %q, the cursor was on %s", got, want)
	}
}

// TestAccentGridAndHexConverge: the two ways in name the same colour. The hex
// of a cell, typed back in after the cursor walked away, lands on that colour
// and puts the cursor back on that cell. Negative control: make the hex field
// set the colour without moving the grid cursor to its cell.
func TestAccentGridAndHexConverge(t *testing.T) {
	f := accentFixture(t)
	openAccentPicker(t, f)
	// The picker opens on the hue strip; tab to the grid, the control whose
	// arrows move the grid cursor.
	for i := 0; ; i++ {
		if i == 16 {
			t.Fatal("no tab stop moves the grid cursor under the arrows")
		}
		// The cursor may start against an edge, so either way counts.
		col := f.M.AccentPicker.Col
		f.Keys("left")
		if f.M.AccentPicker.Col != col {
			break
		}
		f.Keys("right")
		if f.M.AccentPicker.Col != col {
			break
		}
		f.Keys("tab")
	}
	for _, walk := range [][]string{{"left"}, {"down", "left", "left"}, {"down", "down", "down", "left"}} {
		f.Keys(walk...)
		want, col, row := shownHex(t, f), f.M.AccentPicker.Col, f.M.AccentPicker.Row
		f.Keys("right", "up")
		if shownHex(t, f) == want {
			t.Fatalf("walking away from %s did not change the colour", want)
		}
		f.Type(strings.TrimPrefix(want, "#"))
		if got := shownHex(t, f); got != want {
			t.Errorf("typing %s gave %q:\n%s", want, got, f.Text())
		}
		if f.M.AccentPicker.Col != col || f.M.AccentPicker.Row != row {
			t.Errorf("typing %s put the cursor on (%d,%d), the cell is (%d,%d)",
				want, f.M.AccentPicker.Col, f.M.AccentPicker.Row, col, row)
		}
	}
}

// TestAccentPickerKeyboardReachesEveryControl: tab walks the picker's
// controls and comes back round, and every control it stops on moves under
// the arrows, so nothing is mouse-only. A stop the screen does not change for
// is a control the keyboard cannot use. The arrows can move the focus too
// (down from one slider to the next), so the cycle is walked with tab alone,
// and each stop is tried on a picker opened afresh. Negative control: make
// AccentPickerMove ignore the harmony row.
func TestAccentPickerKeyboardReachesEveryControl(t *testing.T) {
	f := accentFixture(t)
	openAccentPicker(t, f)
	first := fmt.Sprint(f.M.AccentPicker.Focus)
	stops := 0
	for stops < 40 {
		f.Keys("tab")
		stops++
		if fmt.Sprint(f.M.AccentPicker.Focus) == first {
			break
		}
	}
	if fmt.Sprint(f.M.AccentPicker.Focus) != first {
		t.Fatalf("tab never came back to the first control after %d presses", stops)
	}
	if stops < 4 {
		t.Errorf("tab stopped on %d controls; the grid, the hue strip, the sliders and the harmony row are four", stops)
	}

	for i := range stops {
		f.Keys("esc")
		openAccentPicker(t, f)
		for range i {
			f.Keys("tab")
		}
		stop := fmt.Sprint(f.M.AccentPicker.Focus)
		before := f.Text()
		f.Keys("right")
		if f.Text() == before {
			f.Keys("down")
		}
		if f.Text() == before {
			t.Errorf("tab stop %d (focus %s) does not move under the arrows:\n%s", i, stop, before)
		}
	}
}
