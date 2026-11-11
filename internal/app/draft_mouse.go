package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
)

// The mouse in a draft: the editor openDraft names, the one that takes the
// keys, wherever it is drawn. Each host records where the editor's first
// line is (draftWires.at) for its pictures and caret, and the same place maps
// a click to the editor's frame.

// draftPress is a mouse press in a draft: the press count that makes a double
// or a triple click, and whether the button is still down.
type draftPress struct {
	comp  *composer.Model
	down  bool
	moved bool
	at    time.Time
	x, y  int
	count int
	shift bool
}

// NoteClickShift records whether shift was held with the click handled next:
// a shift click in a draft extends its selection.
func (m *OS) NoteClickShift(shift bool) { m.draftMouse.shift = shift }

// draftAt maps screen x, y onto the open draft drawn in the panel of kind: the
// line of its last frame and the column in it. inside says the point is on a
// line the panel shows. ok is false when no draft of that panel is on screen.
func (m *OS) draftAt(kind string, x, y int) (d openDraft, line, col int, inside, ok bool) {
	d, ok = m.openDraft()
	if !ok || d.kind != kind || !d.wires.at.drawn {
		return d, 0, 0, false, false
	}
	// The New agent sheet is drawn over the reply area: only its own task
	// takes a click there.
	if sp := m.Inbox.ck.spawn; sp != nil && kind == "inbox" && d.comp != sp.task {
		return d, 0, 0, false, false
	}
	h, found := m.overlayHitByKind(kind)
	if !found || !m.overlayOnTop(kind) {
		return d, 0, 0, false, false
	}
	at := d.wires.at
	line, col = y-h.OriginY-at.y, x-h.OriginX-at.x
	width, lines := d.comp.FrameSize()
	inside = line >= at.top && line < lines && col >= 0 && col < width
	return d, line, col, inside, true
}

// draftClick handles a press at screen x, y on the open draft of the panel
// of kind, and reports whether it was on the draft. A press close to the
// last one, soon after it, counts up: two select a word, three a line. An
// entry of the @ list is left to its host, which picks it.
func (m *OS) draftClick(kind string, x, y int) bool {
	d, line, col, inside, ok := m.draftAt(kind, x, y)
	if !ok || !inside {
		return false
	}
	if _, mention := d.comp.MentionAt(line); mention {
		return false
	}
	p := &m.draftMouse
	now := time.Now()
	if p.comp == d.comp && now.Sub(p.at) <= m.DoubleClickWindow() && y == p.y && abs(x-p.x) <= 1 && p.count < 3 {
		p.count++
	} else {
		p.count = 1
	}
	p.comp, p.at, p.x, p.y, p.down, p.moved = d.comp, now, x, y, true, false
	d.comp.Click(line, col, p.count, p.shift && p.count == 1)
	m.cockpitTouch()
	return true
}

// DraftDrag follows the pointer while a press in a draft is down, and
// reports whether the press took the motion. A drag past the editor's lines
// still maps to it: the editor clamps the point and scrolls toward it.
func (m *OS) DraftDrag(x, y int) bool {
	p := &m.draftMouse
	if !p.down {
		return false
	}
	d, ok := m.openDraft()
	if !ok || d.comp != p.comp {
		p.down = false
		return false
	}
	// Motion that stays on the pressed cell is no drag: it would undo the
	// word a double click just selected.
	if !p.moved && x == p.x && y == p.y {
		return true
	}
	_, line, col, _, ok := m.draftAt(d.kind, x, y)
	if !ok {
		return true
	}
	p.moved = true
	d.comp.Drag(line, col)
	m.cockpitTouch()
	return true
}

// DraftRelease ends a press in a draft, and reports whether one was down. The
// selection it made stays.
func (m *OS) DraftRelease() bool {
	p := &m.draftMouse
	if !p.down {
		return false
	}
	p.down = false
	return true
}
