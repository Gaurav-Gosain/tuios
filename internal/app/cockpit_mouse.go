package app

import (
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Selecting the agents view's thread with the mouse.
//
// The mouse works through the thread's copy mode (cockpit_copy.go), the grid
// whose chrome is blank, so a selection never holds a card's edge, a mark or a
// fold glyph, and a copy joins wrapped rows the way the y key does. A drag or a
// double or triple click starts copy mode by itself, implicit, and the
// internal/input side moves the cursor and the selection with the same code
// that moves them for a key (copymode_grid_mouse.go). The release copies the
// selection when appearance.copy_on_select is on, as a pane's does, and the
// selection stays drawn. The next key or plain click ends an implicit copy
// mode; copy mode the key started stays until esc.
//
// A plain click that does not move is still the view's own click: it opens a
// fold, picks a row, follows a link. Only a press that moves, or a second and
// third click, becomes a selection.

// CopyMouseGesture is a press on a copy grid that is not a pane (the
// thread, quick look's text) and what came of it.
type CopyMouseGesture struct {
	// Armed says the left button is down after a press in the thread, and
	// X, Y is where it went down.
	Armed bool
	X, Y  int
	// Dragged says the pointer moved with the button down, and Clicks how
	// many clicks this press makes (1, 2 or 3).
	Dragged bool
	Clicks  int
	// Dir is the way the selection scrolls while the pointer is held past
	// the top (-1) or the bottom (1) of the thread.
	Dir int
	// lastAt, lastX and lastY are the last press, for the click count.
	lastAt       time.Time
	lastX, lastY int
}

// CockpitMouse is the thread's mouse gesture.
func (m *OS) CockpitMouse() *CopyMouseGesture { return &m.Inbox.ck.mouse }

// Register counts the press at x, y: a press near the last one, inside the
// double click time, is its second or third click, and a fourth starts again.
func (g *CopyMouseGesture) Register(x, y int, interval time.Duration) int {
	near := y == g.lastY && x-g.lastX <= 1 && g.lastX-x <= 1
	if !near || time.Since(g.lastAt) > interval || g.Clicks >= 3 {
		g.Clicks = 1
	} else {
		g.Clicks++
	}
	g.lastAt, g.lastX, g.lastY = time.Now(), x, y
	return g.Clicks
}

// CockpitThreadAt reports whether screen cell x, y is on the thread's text in
// the last frame, with nothing over it.
func (m *OS) CockpitThreadAt(x, y int) bool {
	if !m.ShowInbox || m.inboxCovered() || m.CockpitTop() != CockpitLayerCopy && m.CockpitTop() != CockpitBase {
		return false
	}
	if m.cockpitTab() != "Thread" || !m.threadShown() {
		return false
	}
	t := m.cockpitThread()
	if t == nil || t.viewW <= 0 || t.viewH <= 0 {
		return false
	}
	return x >= t.viewX && x < t.viewX+t.viewW && y >= t.viewY && y < t.viewY+t.viewH
}

// CockpitMouseCopy is copy mode over the thread for a mouse gesture, started
// implicit when it is off. ok is false when it cannot start.
func (m *OS) CockpitMouseCopy() (*terminal.CopyMode, terminal.CopyGrid, bool) {
	if m.threadCopy() == nil && !m.cockpitCopyBegin(true) {
		return nil, nil, false
	}
	cm, grid := m.CockpitCopy()
	return cm, grid, cm != nil
}

// CockpitCopyImplicit reports whether copy mode over the thread is one a
// mouse selection started.
func (m *OS) CockpitCopyImplicit() bool {
	q := m.threadCopy()
	return q != nil && q.implicit
}

// CockpitCopyCell is screen cell x, y as a cell of copy mode's view, and the
// rows the view shows. The cell may lie outside the view, above it or below,
// which a drag reads as a way to scroll.
func (m *OS) CockpitCopyCell(x, y int) (cx, cy, rows int) {
	q := m.threadCopy()
	t := m.cockpitThread()
	if q == nil || t == nil {
		return 0, 0, 0
	}
	// A thread shorter than its view sits on the view's bottom
	// (drawThreadCopy).
	top := t.viewY + t.viewH - q.visible()
	return x - t.viewX, y - top, q.visible()
}

// CockpitCopyFinish copies the selection a mouse gesture made, when
// appearance.copy_on_select is on, and says how many lines it took.
func (m *OS) CockpitCopyFinish(clicks int) tea.Cmd {
	q := m.threadCopy()
	if q == nil || !q.cm.State.InVisual() || !m.Settings.CopyOnSelect {
		return nil
	}
	cm := &q.cm
	text := m.CockpitCopyText(cm.VisualStart, cm.VisualEnd, cm.State == terminal.CopyModeVisualLine, cm.State == terminal.CopyModeVisualBlock)
	if text == "" {
		return nil
	}
	lines := 1
	for i := range len(text) {
		if text[i] == '\n' {
			lines++
		}
	}
	note := "Copied " + strconv.Itoa(lines) + " lines"
	if lines == 1 {
		note = "Copied 1 line"
	}
	if clicks == 2 {
		// A double click is the first two thirds of a triple click: the
		// copy waits out the rest of the click time.
		m.ShowNotification(note, "success", m.Settings.NotificationDuration)
		return m.DeferCopyToClipboard(text, m.DoubleClickWindow()-time.Since(m.Inbox.ck.mouse.lastAt))
	}
	cmd := tea.Batch(m.CopyToClipboard(text), m.SaveToPasteBuffers(text))
	m.ShowNotification(note, "success", m.Settings.NotificationDuration)
	return cmd
}

// CockpitCopyKeep makes a copy mode the mouse started one that stays, as
// the key starts it: a search in it is the person's to step through.
func (m *OS) CockpitCopyKeep() {
	if q := m.threadCopy(); q != nil {
		q.implicit, q.cm.Implicit = false, false
	}
}

// CockpitThreadSelection is the text the thread's selection holds, as y copies it,
// and the rows it spans. ok is false with no selection.
func (m *OS) CockpitThreadSelection() (text string, y0, y1 int, ok bool) {
	q := m.threadCopy()
	if q == nil || !q.cm.State.InVisual() {
		return "", 0, 0, false
	}
	cm := &q.cm
	text = m.CockpitCopyText(cm.VisualStart, cm.VisualEnd, cm.State == terminal.CopyModeVisualLine, cm.State == terminal.CopyModeVisualBlock)
	return text, cm.VisualStart.Y, cm.VisualEnd.Y, text != ""
}

// OpenCockpitSelectionMenu opens the menu of a right click on the thread's
// selection: Copy, Copy as Markdown and Search for it. It reports false, and
// opens nothing, when the click is off the thread or nothing is selected.
func (m *OS) OpenCockpitSelectionMenu(x, y int) bool {
	if !m.CockpitThreadAt(x, y) {
		return false
	}
	if _, _, _, ok := m.CockpitThreadSelection(); !ok {
		return false
	}
	cm := &ContextMenu{AnchorX: x, AnchorY: y, ItemH: 1, WindowIndex: -1, Title: "Selection"}
	cm.Items = []ContextMenuItem{
		{Label: "Copy", Action: "selection_copy"},
		{Label: "Copy as Markdown", Action: "selection_copy_markdown"},
		{Label: "Search for it", Action: "selection_search"},
	}
	cm.home()
	m.ContextMenu = cm
	return true
}

// cockpitCopyWheel scrolls copy mode's view by delta rows (up is negative)
// when the wheel turns over the thread. Copy mode stays on.
func (m *OS) cockpitCopyWheel(x, y, delta int) bool {
	q := m.threadCopy()
	if q == nil || !m.CockpitThreadAt(x, y) {
		return false
	}
	off := min(max(q.offset-delta, 0), q.ScrollbackLen())
	q.SetScrollOffset(off)
	q.cm.ScrollOffset = q.offset
	m.cockpitTouch()
	return true
}

// cockpitAutoScroll moves copy mode's view a row while a drag is held past
// the thread's top or bottom, and the selection's end with it. The tick that
// calls it runs only while the drag is held there.
func (m *OS) cockpitAutoScroll() bool {
	g := &m.Inbox.ck.mouse
	q := m.threadCopy()
	if !g.Armed || g.Dir == 0 || q == nil {
		return false
	}
	off := min(max(q.offset-g.Dir, 0), q.ScrollbackLen())
	q.SetScrollOffset(off)
	cm := &q.cm
	cm.ScrollOffset = q.offset
	if g.Dir < 0 {
		cm.CursorY = 0
	} else {
		cm.CursorY = max(q.visible()-1, 0)
	}
	if cm.State.InVisual() {
		cm.VisualEnd.Y = q.top() + cm.CursorY
		if cm.State == terminal.CopyModeVisualChar {
			cm.VisualEnd.X = cm.CursorX
		}
	}
	m.cockpitTouch()
	return true
}
