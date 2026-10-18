package app

import (
	"bytes"
	"sync"

	tea "charm.land/bubbletea/v2"
)

// Parking the host cursor for input methods (#649).
//
// An input method (a Chinese or Japanese IME, the macOS accent popup) opens
// its candidate window at the terminal's real cursor, and it does that even
// when the cursor is hidden. A guest such as Claude Code hides the cursor and
// draws its own, but it still moves the hidden cursor to its prompt for this
// reason. tuios used to answer a hidden guest cursor with a nil tea.Cursor,
// and Bubble Tea then leaves the host cursor wherever the frame's last write
// ended, which is the bottom-right of the screen more often than not. The
// candidate window opened there.
//
// Bubble Tea has no hidden cursor with a position: a nil Cursor is hidden and
// unmoved, a non-nil one is shown and moved. So tuios hands it a non-nil
// cursor at the hidden caret, which makes the renderer end the frame with the
// cursor there and keeps its own idea of the cursor position true. The frame
// hook (the same one that writes sixel images after a frame) then takes the
// renderer's show sequence out of the frame, so the cursor is never shown at
// all. A frame the hook takes it out of costs fewer bytes than before, which
// matters most over SSH and tuios-web, where frames have no synchronized
// update around them.
//
// Only a transport that installs the hook parks the cursor: the local client,
// the attach client, the SSH server and tuios-web. The others (tape playback,
// the browser build, the GUI bridge) keep the nil cursor.

// Cursor visibility (DECTCEM) and the alternate screen exit, as the renderer
// writes them.
var (
	seqShowCursor     = []byte("\x1b[?25h")
	seqHideCursor     = []byte("\x1b[?25l")
	seqCursorVis      = []byte("\x1b[?25")
	seqLeaveAltScreen = []byte("\x1b[?1049l")
)

// stripKeepMax is the largest edited frame whose buffer is kept for the next
// one. A full repaint is larger and gets a buffer of its own, so one large
// frame does not pin its size for the life of the client.
const stripKeepMax = 64 << 10

// cursorPark keeps the host cursor hidden while tuios parks it. It sits
// between the model, which says what it wants, and the renderer's writer,
// which reports what each frame did.
type cursorPark struct {
	mu sync.Mutex
	// want is whether the host cursor should be visible: false while it is
	// parked hidden, and false when the view has no cursor at all.
	want bool
	// hidePark says the latest view parks the cursor hidden: it hands the
	// renderer a cursor the host must not show.
	hidePark bool
	// shown is whether the host cursor is visible as of the last write this
	// side saw. A terminal starts with it shown.
	shown bool
	// at and placed are where the last frame left the host cursor, placed
	// false when the frame's view had no cursor. next and nextOK are the
	// latest view's, which the next frame takes.
	at, next       tea.Position
	placed, nextOK bool
	// buf holds the last edited frame.
	buf []byte
	// out writes to the terminal outside a frame.
	out func([]byte)
}

// frame is the part of the frame hook that keeps the cursor's visibility
// right. While the cursor is parked hidden it takes the renderer's
// visibility sequences out of the frame, and puts a hide in front of it when
// the host is showing the cursor. Otherwise the renderer is in charge, and
// after is what brings the host to the wanted visibility when the renderer
// did not: a parked cursor shown again, which the renderer thinks it never
// hid.
func (p *cursorPark) frame(frame []byte) (out, after []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.at, p.placed = p.next, p.nextOK
	if bytes.Contains(frame, seqLeaveAltScreen) {
		// The program is closing or suspending. The terminal goes back to
		// whoever ran tuios, with its cursor showing.
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

// noteVisibility takes the host's visibility from the last DECTCEM in frame.
func (p *cursorPark) noteVisibility(frame []byte) {
	if on, off := bytes.LastIndex(frame, seqShowCursor), bytes.LastIndex(frame, seqHideCursor); on > off {
		p.shown = true
	} else if off > on {
		p.shown = false
	}
}

// strip is frame without its cursor visibility sequences, with a hide in
// front when hide is set.
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

// settle returns the sequence that makes the host's visibility what is
// wanted, nil when it already is. Called with p.mu held.
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

// set records the view's cursor, nil for none, and whether the host shows it.
//
// A change of visibility normally goes out with the frame that follows. When
// the cursor stays where the last frame left it, the renderer may write no
// frame at all (a guest that only shows or hides its cursor changes no cell),
// so the change is written at once. The cursor is already in its place then,
// so it never shows anywhere else.
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
	// Written without the lock. The writer takes its own lock and then runs
	// the frame hook, which takes this one.
	if len(seq) > 0 && out != nil {
		out(seq)
	}
}

// viewCursor is the cursor the view hands the renderer, and whether the host
// shows it. With no cursor shown and something on screen taking typing, it is
// that input's caret, to be parked there hidden.
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

// draftCaret is the caret of an open draft (the Inbox reply, the mail
// editor) on screen, nil when no draft is drawn or its caret is cut off.
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

// connectCursorPark makes w's frame hook keep the host cursor hidden while it
// is parked.
func (m *OS) connectCursorPark(w FrameWriter) *cursorPark {
	m.cursorPark = &cursorPark{shown: true, out: w.WriteOutside}
	return m.cursorPark
}
