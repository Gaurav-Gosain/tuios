package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
)

// ckEdPress is a mouse press in the agents view's reply editor: where the
// editor's frame was on screen, so a drag past its lines still maps to it,
// and the press count that makes a double or triple click.
type ckEdPress struct {
	ed *composer.Model
	// x0 is the screen column of the frame's left edge, line0 the screen
	// row of the frame's first line.
	x0, line0 int
	down      bool
	moved     bool
	at        time.Time
	x, y      int
	count     int
}

// NoteClickShift records whether shift was held with the click the overlays
// handle next: a shift click in an editor extends its selection.
func (m *OS) NoteClickShift(shift bool) { m.Inbox.ck.clickShift = shift }

// cockpitEditorPress handles a press on a line of the reply editor. A press
// close to the last one, soon after it, counts up: two select a word, three
// a line.
func (m *OS) cockpitEditorPress(h ckHit, x, y int) {
	ed := m.Inbox.reply.editor
	if ed == nil || ed.comp == nil {
		return
	}
	p := &m.Inbox.ck.ed
	now := time.Now()
	if p.ed == ed.comp && now.Sub(p.at) <= ckDoubleClick && y == p.y && abs(x-p.x) <= 1 && p.count < 3 {
		p.count++
	} else {
		p.count = 1
	}
	p.ed, p.at, p.x, p.y, p.down, p.moved = ed.comp, now, x, y, true, false
	p.x0, p.line0 = h.x0, h.y0-h.group
	ed.comp.Click(h.group, x-h.x0, p.count, m.Inbox.ck.clickShift && p.count == 1)
	m.cockpitTouch()
}

// cockpitEditorDrag follows the pointer while a press in the reply editor is
// down. It reports whether the press took the motion.
func (m *OS) cockpitEditorDrag(x, y int) bool {
	p := &m.Inbox.ck.ed
	if !p.down {
		return false
	}
	ed := m.Inbox.reply.editor
	if ed == nil || ed.comp != p.ed {
		p.down = false
		return false
	}
	// Motion that stays on the pressed cell is no drag: it would undo
	// the word a double click just selected.
	if !p.moved && x == p.x && y == p.y {
		return true
	}
	p.moved = true
	ed.comp.Drag(y-p.line0, x-p.x0)
	m.cockpitTouch()
	return true
}
