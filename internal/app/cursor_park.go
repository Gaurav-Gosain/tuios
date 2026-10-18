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
// cursor there and keeps its own idea of the cursor position true, and hides
// it again after the frame. The hiding is done by the frame hook (the same
// one that writes sixel images after a frame), so the DECTCEM reset lands in
// the same write, inside the frame's synchronized update.
//
// Only a transport that installs the hook parks the cursor: the local client,
// the attach client, the SSH server and tuios-web. The others (tape playback,
// the browser build, the GUI bridge) keep the nil cursor.

// Cursor visibility (DECTCEM) and the alternate screen exit, as the renderer
// writes them.
var (
	seqShowCursor     = []byte("\x1b[?25h")
	seqHideCursor     = []byte("\x1b[?25l")
	seqLeaveAltScreen = []byte("\x1b[?1049l")
)

// cursorPark keeps the host cursor hidden while tuios parks it. It sits
// between the model, which says what it wants, and the renderer's writer,
// which reports what each frame did.
type cursorPark struct {
	mu sync.Mutex
	// want is whether the host cursor should be visible after the next
	// write: false while it is parked hidden, and false when the view has
	// no cursor at all.
	want bool
	// shown is whether the host cursor is visible as of the last write this
	// side saw.
	shown bool
	// parked says the last view handed the renderer a cursor, shown or
	// parked. Between two such views the renderer writes no visibility of
	// its own, so a change of want has to be written here.
	parked bool
	// out writes to the terminal outside a frame.
	out func([]byte)
}

// afterFrame is the part of the frame hook that puts the cursor's visibility
// right after frame. The renderer shows the cursor whenever it thinks the
// view has one, which a parked view does.
func (p *cursorPark) afterFrame(frame []byte) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if on, off := bytes.LastIndex(frame, seqShowCursor), bytes.LastIndex(frame, seqHideCursor); on > off {
		p.shown = true
	} else if off > on {
		p.shown = false
	}
	if bytes.Contains(frame, seqLeaveAltScreen) {
		// The program is closing or suspending. The terminal goes back to
		// whoever ran tuios, with its cursor showing.
		p.parked = false
		if !p.shown {
			p.shown = true
			return seqShowCursor
		}
		return nil
	}
	return p.settle()
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

// set records the view's cursor: whether it has one, and whether that one is
// to be seen. When the last view had one too, the renderer writes nothing for
// the visibility, and may write no frame at all, so the change goes out now.
func (p *cursorPark) set(has, visible bool) {
	p.mu.Lock()
	was := p.parked
	p.parked = has
	p.want = has && visible
	var seq []byte
	if was && has {
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

// parkHostCursor is the cursor the view hands the renderer. shown is the
// cursor the host shows, nil when it shows none. When there is none and
// something on screen is taking typing, the cursor is parked, hidden, at that
// input's caret.
func (m *OS) parkHostCursor(shown *tea.Cursor) *tea.Cursor {
	p := m.cursorPark
	if p == nil {
		return shown
	}
	if shown != nil {
		p.set(true, true)
		return shown
	}
	at := m.imeCaret()
	p.set(at != nil, false)
	return at
}

// imeCaret is where typing goes on screen when the host shows no cursor: the
// caret of an open draft (the Inbox reply, the mail editor), else the focused
// pane's hidden cursor. Nil when nothing takes typing.
func (m *OS) imeCaret() *tea.Cursor {
	if d, ok := m.openDraft(); ok && d.wires.at.ok {
		if c, ok := d.comp.Caret(); ok {
			if h, ok := m.overlayHitByKind(d.kind); ok && m.overlayOnTop(d.kind) {
				return tea.NewCursor(h.OriginX+d.wires.at.x+c.Col, h.OriginY+d.wires.at.y+c.Row)
			}
		}
	}
	cur, _ := m.paneCursor()
	return cur
}

// connectCursorPark makes w's frame hook keep the host cursor hidden while it
// is parked.
func (m *OS) connectCursorPark(w FrameWriter) *cursorPark {
	m.cursorPark = &cursorPark{out: w.WriteOutside}
	return m.cursorPark
}
