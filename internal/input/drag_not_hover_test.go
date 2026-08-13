package input

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
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

// cursorCells returns the screen positions of the picker's swatch cursors in
// the composed frame, topmost first: the slot row's when it has one, then the
// hue strip's, then the grid's. Working off the drawn glyph rather than the
// picker's own rects is deliberate, since the claim under test is about where
// the mark the user is looking at ends up.
//
// The channel sliders wear the same mark on a track rather than on a swatch, so
// a mark with track glyph beside it is a thumb and not a cursor.
func cursorCells(t *testing.T, m *app.OS) [][2]int {
	t.Helper()
	onTrack := func(plain []rune, x int) bool {
		for _, at := range []int{x - 1, x + 1} {
			if at >= 0 && at < len(plain) && strings.ContainsRune("━─=-", plain[at]) {
				return true
			}
		}
		return false
	}
	var out [][2]int
	for y, line := range frameLines(m) {
		plain := []rune(stripSGR(line))
		for x, r := range plain {
			if r == '◆' && !onTrack(plain, x) {
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
	// Start at the left-hand end of the strip, so the drag below has strip to
	// travel over whatever hue the pane happened to be wearing and however many
	// cells the layout gives the strip.
	m.AccentPickerHueCell(0)
	marks := cursorCells(t, m)
	if len(marks) < 2 {
		t.Fatalf("the picker drew %d cursor marks", len(marks))
	}
	// The strip sits directly above the grid, whose mark is the lowest, so it is
	// the one before it however many rows are drawn over them.
	hx, hy := marks[len(marks)-2][0], marks[len(marks)-2][1]

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

// TestOverlayPanelNeedsAHeldButton: a grabbed panel follows the pointer while
// the button is down and stops the moment it is not.
func TestOverlayPanelNeedsAHeldButton(t *testing.T) {
	app.SetInputHandler(HandleInput)
	m := hoverOS(t)
	m.ShowHelp = true
	m.HelpCategory = 0
	_ = frameLines(m)
	if len(m.OverlayHits) == 0 {
		t.Fatal("the composed frame recorded no overlay panel to grab")
	}
	px, py := m.OverlayHits[0].OriginX+2, m.OverlayHits[0].OriginY+1

	// A right press anywhere on the panel grabs it.
	next, _ := m.Update(tea.MouseClickMsg{X: px, Y: py, Button: tea.MouseRight})
	m = next.(*app.OS)
	if !m.OverlayDragActive() {
		t.Fatal("a right press on the panel did not grab it")
	}
	m = dragged(m, px+12, py+4)
	_ = frameLines(m)
	moved := m.OverlayHits[0].OriginX
	if moved == px-2 {
		t.Fatal("a held drag did not move the panel")
	}

	m = motion(m, px-20, py-3)
	if m.OverlayDragActive() {
		t.Error("the panel grab outlived the button that started it")
	}
	_ = frameLines(m)
	if got := m.OverlayHits[0].OriginX; got != moved {
		t.Errorf("button-free motion moved the panel from x=%d to x=%d", moved, got)
	}
}

// TestSidebarEdgeResizeNeedsAHeldButton: the rail's width follows a held drag on
// its edge rule and stays put under bare hover.
func TestSidebarEdgeResizeNeedsAHeldButton(t *testing.T) {
	app.SetInputHandler(HandleInput)
	m := hoverOS(t)
	_ = frameLines(m)
	edgeX := config.SidebarWidth - 1

	m = pressed(m, edgeX, 5)
	if !m.SidebarEdgeActive() {
		t.Fatal("a press on the rail's edge rule did not arm the width resize")
	}
	m = dragged(m, edgeX-8, 5)
	dragged := config.SidebarWidth
	if dragged == edgeX+1 {
		t.Fatal("a held drag did not resize the rail")
	}

	m = motion(m, edgeX-20, 5)
	if m.SidebarEdgeActive() {
		t.Error("the rail's width drag outlived the button that started it")
	}
	if config.SidebarWidth != dragged {
		t.Errorf("button-free motion resized the rail from %d to %d", dragged, config.SidebarWidth)
	}
}

// TestCtrlDragNeedsAHeldButton: an armed ctrl-grab commits to a move only while
// the button is down. Ctrl alone is a modifier, and hovering with it held is
// not a request to move a pane.
func TestCtrlDragNeedsAHeldButton(t *testing.T) {
	app.SetInputHandler(HandleInput)
	o, wa, wb := twoPaneBSP(t)
	left, _ := leftPaneOf(wa, wb)
	cx, cy := left.X+5, left.Y+5

	next, _ := o.Update(tea.MouseClickMsg{X: cx, Y: cy, Button: tea.MouseLeft, Mod: tea.ModCtrl})
	o = next.(*app.OS)
	if !o.CtrlDragPending {
		t.Fatal("ctrl+left press on pane content did not arm a move")
	}

	before := left.X
	next, _ = o.Update(tea.MouseMotionMsg{X: cx + 20, Y: cy + 5, Mod: tea.ModCtrl})
	o = next.(*app.OS)
	if o.CtrlDragging {
		t.Error("button-free motion committed the armed ctrl-grab to a move")
	}
	if left.X != before {
		t.Errorf("button-free motion moved the pane from x=%d to x=%d", before, left.X)
	}

	// Held, the same motion does commit and move it.
	next, _ = o.Update(tea.MouseClickMsg{X: cx, Y: cy, Button: tea.MouseLeft, Mod: tea.ModCtrl})
	o = next.(*app.OS)
	next, _ = o.Update(tea.MouseMotionMsg{X: cx + 20, Y: cy + 5, Button: tea.MouseLeft, Mod: tea.ModCtrl})
	o = next.(*app.OS)
	if !o.CtrlDragging {
		t.Fatal("a held ctrl-drag past the threshold did not commit to a move")
	}
	if left.X == before {
		t.Errorf("a held ctrl-drag left the pane at x=%d", left.X)
	}
}

// TestSidebarReorderNeedsAHeldButton: a session row press that lost its release
// must not turn into a reorder that follows the pointer down the rail.
func TestSidebarReorderNeedsAHeldButton(t *testing.T) {
	app.SetInputHandler(HandleInput)
	m := hoverOS(t)
	client := session.NewTUIClient()
	client.UpdateSessionCache([]session.SessionInfo{
		{Name: "main"}, {Name: "scratch"}, {Name: "deploy"},
	})
	m.DaemonClient = client
	m.SessionName = "main"

	lines := frameLines(m)
	sx, sy := railCell(t, lines, "scratch")
	_, dy := railCell(t, lines, "deploy")

	m = pressed(m, sx, sy)
	if !m.SidebarDragActive() {
		t.Fatal("a press on a session row did not arm the reorder gesture")
	}

	before := append([]string(nil), m.SidebarOrder...)
	m = motion(m, sx, dy)
	if m.SidebarDragActive() {
		t.Error("the session reorder outlived the button that started it")
	}
	if !reflect.DeepEqual(m.SidebarOrder, before) {
		t.Errorf("button-free motion reordered the rail: %v -> %v", before, m.SidebarOrder)
	}
}

// TestCopyModeSelectionIgnoresButtonFreeMotion is the one that would go
// unnoticed: a selection that kept extending on hover would quietly copy the
// wrong text.
func TestCopyModeSelectionIgnoresButtonFreeMotion(t *testing.T) {
	app.SetInputHandler(HandleInput)
	o, win := selectPane(t, "hello world this is a line of text")

	pressAt(o, 2, 0)
	dragTo(o, 10, 0)
	want := selectedText(win)
	if want == "" {
		t.Fatal("a held drag selected nothing")
	}

	next, _ := o.Update(tea.MouseMotionMsg{X: 21, Y: 1})
	o = next.(*app.OS)
	if got := selectedText(win); got != want {
		t.Errorf("button-free motion extended the selection: %q -> %q", want, got)
	}
}
