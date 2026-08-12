package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// The host is held in all-motion tracking so hover and focus-follows-mouse work,
// which means every handler that means "drag" now receives motion with no button
// held too. These tests pin the two halves of that: a gesture only moves while a
// button is down, and the hover affordances still move without one.

// pressed, dragged and released drive the real Update path with a button state, the
// way motion() does without one.
func pressed(m *app.OS, x, y int) *app.OS {
	next, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return next.(*app.OS)
}

func dragged(m *app.OS, x, y int) *app.OS {
	next, _ := m.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft})
	return next.(*app.OS)
}

func released(m *app.OS, x, y int) *app.OS {
	next, _ := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	return next.(*app.OS)
}

// pickerOS opens the accent picker on the first pane.
func pickerOS(t *testing.T) *app.OS {
	t.Helper()
	m := hoverOS(t)
	m.OpenAccentPicker(m.Windows[0].ID)
	return m
}

// cursorCells returns the screen positions of the picker's cursor marks in the
// composed frame, topmost first: the hue strip's, then the grid's. Working off
// the drawn glyph rather than the picker's own rects is deliberate, since the
// claim under test is about where the mark the user is looking at ends up.
func cursorCells(t *testing.T, m *app.OS) [][2]int {
	t.Helper()
	var out [][2]int
	for y, line := range frameLines(m) {
		plain := []rune(stripSGR(line))
		for x, r := range plain {
			if r == '◆' {
				out = append(out, [2]int{x, y})
			}
		}
	}
	return out
}

// gridCell places the picker cursor on (col, row) and returns where its mark is
// drawn on screen.
func gridCell(t *testing.T, m *app.OS, col, row int) (x, y int) {
	t.Helper()
	m.AccentPickerCell(col, row)
	marks := cursorCells(t, m)
	if len(marks) < 2 {
		t.Fatalf("the picker drew %d cursor marks, want the hue strip's and the grid's:\n%s",
			len(marks), strings.Join(frameLines(m), "\n"))
	}
	// The grid sits under the strip, so its mark is the lower one.
	return marks[len(marks)-1][0], marks[len(marks)-1][1]
}

// TestAccentGridIgnoresButtonFreeMotion is the reported bug: crossing the
// dialog with nothing pressed kept repainting the accent, so the colour never
// settled anywhere the user meant it to.
func TestAccentGridIgnoresButtonFreeMotion(t *testing.T) {
	m := pickerOS(t)
	bx, by := gridCell(t, m, 8, 4)
	ax, ay := gridCell(t, m, 2, 1)

	want := m.AccentPicker.Cur
	wantMark := [2]int{ax, ay}

	m = motion(m, bx, by)
	if m.AccentPicker.Col != 2 || m.AccentPicker.Row != 1 {
		t.Errorf("button-free motion moved the grid cursor to (%d,%d), want (2,1)",
			m.AccentPicker.Col, m.AccentPicker.Row)
	}
	if m.AccentPicker.Cur != want {
		t.Errorf("button-free motion repainted the accent: %v -> %v", want, m.AccentPicker.Cur)
	}
	if marks := cursorCells(t, m); marks[len(marks)-1] != wantMark {
		t.Errorf("the drawn grid mark followed the pointer to %v, want it left at %v",
			marks[len(marks)-1], wantMark)
	}
}

// TestAccentGridFollowsAHeldDrag is the other half: press, drag, release lands
// on the cell the button came up over and stops there.
func TestAccentGridFollowsAHeldDrag(t *testing.T) {
	m := pickerOS(t)
	bx, by := gridCell(t, m, 8, 4)
	ax, ay := gridCell(t, m, 2, 1)

	m = pressed(m, ax, ay)
	m = dragged(m, bx, by)
	if m.AccentPicker.Col != 8 || m.AccentPicker.Row != 4 {
		t.Fatalf("a held drag left the grid cursor at (%d,%d), want (8,4)",
			m.AccentPicker.Col, m.AccentPicker.Row)
	}
	m = released(m, bx, by)
	locked := m.AccentPicker.Cur
	if m.AccentPicker.Col != 8 || m.AccentPicker.Row != 4 {
		t.Fatalf("the release moved the cursor off the cell it came up on: (%d,%d)",
			m.AccentPicker.Col, m.AccentPicker.Row)
	}

	// Released, the colour is locked: crossing the dialog again changes nothing.
	m = motion(m, ax, ay)
	if m.AccentPicker.Cur != locked {
		t.Errorf("the colour did not lock on release: %v -> %v", locked, m.AccentPicker.Cur)
	}
	if marks := cursorCells(t, m); marks[len(marks)-1] != [2]int{bx, by} {
		t.Errorf("the drawn grid mark moved after release to %v, want %v",
			marks[len(marks)-1], [2]int{bx, by})
	}
}

// TestAccentHueStripIgnoresButtonFreeMotion covers the strip, which is the same
// code path as the grid and fails the same way.
func TestAccentHueStripIgnoresButtonFreeMotion(t *testing.T) {
	m := pickerOS(t)
	marks := cursorCells(t, m)
	if len(marks) < 2 {
		t.Fatalf("the picker drew %d cursor marks", len(marks))
	}
	hx, hy := marks[0][0], marks[0][1]

	hue := m.AccentPicker.Hue
	m = motion(m, hx+6, hy)
	if m.AccentPicker.Hue != hue {
		t.Errorf("button-free motion turned the hue: %v -> %v", hue, m.AccentPicker.Hue)
	}

	m = pressed(m, hx, hy)
	m = dragged(m, hx+6, hy)
	if m.AccentPicker.Hue == hue {
		t.Error("a held drag along the strip did not turn the hue")
	}
	turned := m.AccentPicker.Hue
	m = released(m, hx+6, hy)
	m = motion(m, hx, hy)
	if m.AccentPicker.Hue != turned {
		t.Errorf("the hue did not lock on release: %v -> %v", turned, m.AccentPicker.Hue)
	}
}

// TestScrollbarThumbNeedsAHeldButton is the same class of bug one layer down. A
// release lost while the pointer is off the surface leaves ScrollbarDragging
// set, and under all-motion tracking every hover after it drags the thumb, so
// the pane scrolls to wherever the pointer happens to be.
func TestScrollbarThumbNeedsAHeldButton(t *testing.T) {
	app.SetInputHandler(HandleInput)
	o, win, rect := scrolledBackPane(t)

	top := rect.TrackY
	bottom := rect.TrackY + rect.TrackH - 1

	o = pressed(o, rect.X, app.ScrollbarThumbRow(win))
	if !o.ScrollbarDragging {
		t.Fatal("a press on the thumb did not start a drag")
	}
	o = dragged(o, rect.X, bottom)
	moved := app.ScrollbarThumbRow(win)

	// The release never arrives; what comes back is motion reporting no button.
	o = motion(o, rect.X, top)
	if got := app.ScrollbarThumbRow(win); got != moved {
		t.Errorf("button-free motion dragged the thumb from row %d to %d", moved, got)
	}
	if o.ScrollbarDragging {
		t.Error("the scrollbar drag outlived the button that started it")
	}

	// And hover is reachable again: it is no longer swallowed by a drag that
	// never ended.
	o = motion(o, rect.X, bottom)
	if got := app.ScrollbarThumbRow(win); got != moved {
		t.Errorf("a second button-free motion moved the thumb to %d, want %d", got, moved)
	}
}

// TestScrollbarThumbLocksOnRelease is the ordinary path: the thumb stops where
// the button came up and later hover leaves it there.
func TestScrollbarThumbLocksOnRelease(t *testing.T) {
	app.SetInputHandler(HandleInput)
	o, win, rect := scrolledBackPane(t)

	bottom := rect.TrackY + rect.TrackH - 1
	o = pressed(o, rect.X, app.ScrollbarThumbRow(win))
	o = dragged(o, rect.X, bottom)
	o = released(o, rect.X, bottom)
	locked := app.ScrollbarThumbRow(win)

	o = motion(o, rect.X, rect.TrackY)
	if got := app.ScrollbarThumbRow(win); got != locked {
		t.Errorf("the thumb did not lock on release: row %d -> %d", locked, got)
	}
}
