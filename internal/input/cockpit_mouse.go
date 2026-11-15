package input

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// autoScrollTickCmd is the next tick of a drag held past an edge.
func autoScrollTickCmd() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg { return app.AutoScrollTickMsg{} })
}

// The mouse over the agents view's thread: a drag selects, a double click
// selects a word, a triple click a line, and shift+click moves the end of the
// selection. In copy mode a click moves the cursor. See app/cockpit_mouse.go.

// cockpitMousePress handles a left press in the thread. It reports whether
// the press is taken; a plain click outside copy mode is not, so the view's
// own click still runs, and only a move with the button down turns it into a
// selection.
func cockpitMousePress(msg tea.MouseClickMsg, o *app.OS) (bool, tea.Cmd) {
	x, y := msg.Mouse().X, msg.Mouse().Y
	o.CockpitLinkCancel()
	g := o.CockpitMouse()
	g.Armed = false
	if msg.Button != tea.MouseLeft || !o.CockpitThreadAt(x, y) {
		return false, nil
	}
	o.CancelPendingCopy()
	clicks := g.Register(x, y, o.DoubleClickWindow())
	g.X, g.Y, g.Dragged, g.Dir = x, y, false, 0
	shift := msg.Mod&tea.ModShift != 0
	on := o.CockpitCopyOn()
	if on && o.CockpitCopyImplicit() && clicks == 1 && !shift {
		// A plain click ends a selection the mouse made, and is the view's
		// click like any other.
		o.CockpitCopyEnd()
		on = false
	}
	if !on && clicks == 1 {
		// Shift with nothing selected, or a plain click: the view's own.
		g.Armed = !shift
		return false, nil
	}
	cm, grid, ok := o.CockpitMouseCopy()
	if !ok {
		return false, nil
	}
	cx, cy, rows := o.CockpitCopyCell(x, y)
	if shift {
		gridDrag(cm, grid, cx, cy, rows)
		g.Dragged = true
	} else {
		gridPress(cm, grid, cx, cy, rows, clicks, &o.Settings)
	}
	g.Armed = true
	return true, nil
}

// cockpitMouseMotion moves a selection while the button is held after a
// press in the thread. The first move of a plain click starts it at the
// press. It reports whether the motion was taken.
func cockpitMouseMotion(msg tea.MouseMotionMsg, o *app.OS) (bool, tea.Cmd) {
	g := o.CockpitMouse()
	if !g.Armed || msg.Button != tea.MouseLeft {
		return false, nil
	}
	x, y := msg.Mouse().X, msg.Mouse().Y
	if !g.Dragged && x == g.X && y == g.Y {
		return true, nil
	}
	if !o.CockpitCopyOn() {
		cm, grid, ok := o.CockpitMouseCopy()
		if !ok {
			g.Armed = false
			return false, nil
		}
		cx, cy, rows := o.CockpitCopyCell(g.X, g.Y)
		gridPress(cm, grid, cx, cy, rows, 1, &o.Settings)
	}
	cm, grid := o.CockpitCopy()
	if cm == nil {
		g.Armed = false
		return false, nil
	}
	g.Dragged = true
	cx, cy, rows := o.CockpitCopyCell(x, y)
	dir := gridDrag(cm, grid, cx, cy, rows)
	start := dir != 0 && g.Dir == 0
	g.Dir = dir
	if start {
		// Held past the edge, the view keeps scrolling on a tick until the
		// pointer comes back or the button goes up (app.cockpitAutoScroll).
		return true, autoScrollTickCmd()
	}
	return true, nil
}

// cockpitMouseRelease ends a gesture in the thread: a drag or a double or
// triple click copies what it selected. It reports whether the release was
// the gesture's.
func cockpitMouseRelease(o *app.OS) (bool, tea.Cmd) {
	g := o.CockpitMouse()
	if !g.Armed {
		return false, nil
	}
	g.Armed, g.Dir = false, 0
	if !o.CockpitCopyOn() || !g.Dragged && g.Clicks < 2 {
		return g.Dragged, nil
	}
	return true, o.CockpitCopyFinish(g.Clicks)
}

// quickLookCopyPress handles a left press in copy mode over quick look's
// text: the cursor goes to the cell, or a double or triple click selects, or
// shift moves the selection's end. It reports whether the press was taken.
func quickLookCopyPress(msg tea.MouseClickMsg, o *app.OS) bool {
	x, y := msg.Mouse().X, msg.Mouse().Y
	g := o.QuickLookMouse()
	if g == nil || msg.Button != tea.MouseLeft || !o.QuickLookCopyOnText(x, y) {
		return false
	}
	cm, grid := o.QuickLookCopy()
	cx, cy, rows, _ := o.QuickLookCopyCell(x, y)
	o.CancelPendingCopy()
	clicks := g.Register(x, y, o.DoubleClickWindow())
	g.X, g.Y, g.Dragged, g.Armed = x, y, false, true
	if msg.Mod&tea.ModShift != 0 {
		gridDrag(cm, grid, cx, cy, rows)
		g.Dragged = true
		return true
	}
	gridPress(cm, grid, cx, cy, rows, clicks, &o.Settings)
	return true
}

// quickLookCopyMotion moves the selection while the button is held.
func quickLookCopyMotion(msg tea.MouseMotionMsg, o *app.OS) bool {
	g := o.QuickLookMouse()
	if g == nil || !g.Armed || msg.Button != tea.MouseLeft {
		return false
	}
	x, y := msg.Mouse().X, msg.Mouse().Y
	if !g.Dragged && x == g.X && y == g.Y {
		return true
	}
	cm, grid := o.QuickLookCopy()
	cx, cy, rows, ok := o.QuickLookCopyCell(x, y)
	if cm == nil || !ok {
		g.Armed = false
		return false
	}
	g.Dragged = true
	gridDrag(cm, grid, cx, cy, rows)
	return true
}

// quickLookCopyRelease ends the gesture, and copies what a drag or a double
// or triple click selected.
func quickLookCopyRelease(o *app.OS) (bool, tea.Cmd) {
	g := o.QuickLookMouse()
	if g == nil || !g.Armed {
		return false, nil
	}
	g.Armed = false
	cm, grid := o.QuickLookCopy()
	if cm == nil || !cm.State.InVisual() || !g.Dragged && g.Clicks < 2 {
		return true, nil
	}
	return true, o.QuickLookCopyFinish(extractVisualText(cm, grid))
}

// handleSelectionCopy copies the thread's selection, the menu's Copy.
func handleSelectionCopy(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	text, _, _, ok := o.CockpitThreadSelection()
	if !ok {
		return o, nil
	}
	return o, tea.Batch(o.CopyToClipboard(text), o.SaveToPasteBuffers(text))
}

// handleSelectionCopyMarkdown copies the entries under the selection as
// Markdown, as Y does in copy mode.
func handleSelectionCopyMarkdown(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	_, y0, y1, ok := o.CockpitThreadSelection()
	if !ok {
		return o, nil
	}
	text := o.CockpitCopyMarkdown(y0, y1)
	if text == "" {
		return o, nil
	}
	o.ShowNotification(fmt.Sprintf("Copied as Markdown, %d chars", len(text)), "success", o.Settings.NotificationDuration)
	return o, o.CopyToClipboard(text)
}

// handleSelectionSearch searches the thread for the selection's first line,
// in copy mode, which then stays on so n and N step through the matches.
func handleSelectionSearch(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	text, _, _, ok := o.CockpitThreadSelection()
	cm, grid := o.CockpitCopy()
	if !ok || cm == nil {
		return o, nil
	}
	query, _, _ := strings.Cut(text, "\n")
	o.CockpitCopyKeep()
	cm.State = terminal.CopyModeNormal
	cm.BeginSearch(false, grid.ScrollbackLen())
	cm.Search.Set(query)
	executeSearch(cm, grid, o.Settings.SearchMaxMatches)
	cm.State = terminal.CopyModeNormal
	o.ShowNotification(fmt.Sprintf("/%s (%s matches)", query, terminal.SearchMatchCount(len(cm.SearchMatches), o.Settings.SearchMaxMatches)), "info", o.Settings.NotificationDuration)
	return o, nil
}
