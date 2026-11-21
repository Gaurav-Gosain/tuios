package app

import (
	"bytes"
	"sync"

	tea "charm.land/bubbletea/v2"
)

var (
	seqShowCursor     = []byte("\x1b[?25h")
	seqHideCursor     = []byte("\x1b[?25l")
	seqCursorVis      = []byte("\x1b[?25")
	seqLeaveAltScreen = []byte("\x1b[?1049l")
)

const stripKeepMax = 64 << 10

type cursorPark struct {
	mu             sync.Mutex
	want           bool
	hidePark       bool
	shown          bool
	at, next       tea.Position
	placed, nextOK bool
	buf            []byte
	out            func([]byte)
}

func (p *cursorPark) frame(frame []byte) (out, after []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.at, p.placed = p.next, p.nextOK
	if bytes.Contains(frame, seqLeaveAltScreen) {
		p.hidePark, p.placed = false, false
		p.noteVisibility(frame)
		if !p.shown {
			p.shown = true
			return frame, seqShowCursor
		}
		return frame, nil
	}
	if p.hidePark {
		out = frame
		if p.shown || bytes.Contains(frame, seqCursorVis) {
			out = p.strip(frame, p.shown)
		}
		p.shown = false
		return out, nil
	}
	p.noteVisibility(frame)
	return frame, p.settle()
}

func (p *cursorPark) noteVisibility(frame []byte) {
	if on, off := bytes.LastIndex(frame, seqShowCursor), bytes.LastIndex(frame, seqHideCursor); on > off {
		p.shown = true
	} else if off > on {
		p.shown = false
	}
}

func (p *cursorPark) strip(frame []byte, hide bool) []byte {
	buf := p.buf[:0]
	if len(frame) > stripKeepMax {
		buf = make([]byte, 0, len(frame)+len(seqHideCursor))
	}
	if hide {
		buf = append(buf, seqHideCursor...)
	}
	for {
		i := bytes.Index(frame, seqCursorVis)
		if i < 0 || i+len(seqCursorVis) >= len(frame) {
			buf = append(buf, frame...)
			break
		}
		end := i + len(seqCursorVis)
		if c := frame[end]; c == 'h' || c == 'l' {
			buf = append(buf, frame[:i]...)
		} else {
			buf = append(buf, frame[:end+1]...)
		}
		frame = frame[end+1:]
	}
	if cap(buf) <= stripKeepMax {
		p.buf = buf
	}
	return buf
}

func (p *cursorPark) settle() []byte {
	if p.shown == p.want {
		return nil
	}
	p.shown = p.want
	if p.want {
		return seqShowCursor
	}
	return seqHideCursor
}

func (p *cursorPark) set(cur *tea.Cursor, visible bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	has := cur != nil
	p.hidePark = has && !visible
	p.want = has && visible
	p.nextOK = has
	if has {
		p.next = cur.Position
	}
	var seq []byte
	if has && p.placed && p.at == cur.Position {
		seq = p.settle()
	}
	out := p.out
	p.mu.Unlock()
	if len(seq) > 0 && out != nil {
		out(seq)
	}
}

func (m *OS) viewCursor() (cur *tea.Cursor, visible bool) {
	cur, hidden := m.paneCursor()
	if cur != nil && !hidden {
		return cur, true
	}
	if m.cursorPark == nil {
		return nil, false
	}
	if c := m.draftCaret(); c != nil {
		return c, false
	}
	return cur, false
}

func (m *OS) draftCaret() *tea.Cursor {
	d, ok := m.openDraft()
	if !ok || !d.wires.at.drawn {
		return nil
	}
	c, ok := d.comp.Caret()
	if !ok || c.Row < d.wires.at.top {
		return nil
	}
	h, ok := m.overlayHitByKind(d.kind)
	if !ok || !m.overlayOnTop(d.kind) {
		return nil
	}
	return tea.NewCursor(h.OriginX+d.wires.at.x+c.Col, h.OriginY+d.wires.at.y+c.Row)
}

func (m *OS) connectCursorPark(w FrameWriter) *cursorPark {
	m.cursorPark = &cursorPark{shown: true, out: w.WriteOutside}
	return m.cursorPark
}
