package app

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/dnd"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Drag and drop between panes over the kitty drag and drop protocol (OSC 72;
// the wire is in vt/dnd.go).
//
// To the program in a pane, tuios is the terminal: the program says it offers
// drags (t=o:x=1) or takes drops (t=a), and tuios sends it the gesture, the
// moves, the drop and the data. Between two panes tuios plays the part the
// window system plays under kitty: it carries the drag from the pane it
// started in to the pane under the pointer, asks the source for data when
// the target asks, and tells each side how the other answered. No host
// terminal takes part, so this works in any terminal, and only between the
// panes of this client.
//
// The gesture is kitty's: a left press in a pane whose program offers drags,
// then the pointer moving to another cell. The press and the moves before
// that reach the program as mouse events, as in kitty. The release does not,
// since kitty's native drag consumes it and the programs expect that. The
// program decides whether the press was on something to drag: tuios offers,
// the program answers with MIME types or stays silent.
//
// A pane whose program did not register for drops still takes one: the paths
// of a dropped text/uri-list arrive as a paste, quoted the way a terminal
// pastes a dropped file, bracketed when the program asked for that. A drop
// from yazi onto a shell or an agent in another pane works with nothing in
// that pane knowing the protocol.
//
// tuios reads no file in this mode. The source is asked for its data and the
// target gets exactly that; the target program opens paths with its own
// rights. A drop into the pane the drag came from is refused with EPERM, as
// kitty refuses it.
//
// The registrations the daemon keeps for the clients are in
// session/dnd_registration.go. Not here yet: the bridge to a host terminal
// that speaks the protocol, which would carry drags between the desktop and
// the panes, and a drop onto a pane whose shell runs on another machine,
// which should go through the file transfer DropPaste uses. Such a pane
// takes no part for now, and neither does a client attached with --host.

// DnDMsg is one message a pane's program sent, chunk by chunk.
type DnDMsg struct {
	WindowID string
	Cmd      dnd.Command
}

// dndWakeMsg says the inbox has messages to drain.
type dndWakeMsg struct{}

// dndInbox is where the emulator callbacks leave messages for the update
// loop. The callback runs on the PTY reader under the window's IO lock, so it
// must not block, and a data stream is thousands of chunks, which a channel
// of a fixed size would lose in the middle. So: a bounded queue under its own
// lock, and a one-slot wake channel.
type dndInbox struct {
	mu      sync.Mutex
	msgs    []DnDMsg
	bytes   int
	dropped bool
	wake    chan struct{}
}

// dndInboxCap bounds the payload bytes waiting in the inbox, and
// dndInboxMsgs the messages: a flood of moves with no payload would grow the
// queue past any byte count.
const (
	dndInboxCap  = 32 << 20
	dndInboxMsgs = 4096
)

func (q *dndInbox) push(msg DnDMsg) {
	q.mu.Lock()
	if q.bytes+len(msg.Cmd.Payload) > dndInboxCap || len(q.msgs) >= dndInboxMsgs {
		q.dropped = true
	} else {
		q.msgs = append(q.msgs, msg)
		q.bytes += len(msg.Cmd.Payload)
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *dndInbox) drain() (msgs []DnDMsg, dropped bool) {
	q.mu.Lock()
	msgs, dropped = q.msgs, q.dropped
	q.msgs, q.bytes, q.dropped = nil, 0, false
	q.mu.Unlock()
	return msgs, dropped
}

// ensureDnDInbox makes the inbox on first use.
func (m *OS) ensureDnDInbox() *dndInbox {
	if m.dndIn == nil {
		m.dndIn = &dndInbox{wake: make(chan struct{}, 1)}
	}
	return m.dndIn
}

// listenDnDUntil wakes the update loop when a pane left a message. The
// caller re-arms it after each wake.
func listenDnDUntil(stop <-chan struct{}, q *dndInbox) tea.Cmd {
	return listenOnce(stop, q.wake, func(struct{}) tea.Msg { return dndWakeMsg{} })
}

// setupDnD wires a window's OSC 72 to the inbox. Called under the window's
// IO lock from installPassthroughs.
func (m *OS) setupDnD(window *terminal.Window) {
	if window == nil {
		return
	}
	q := m.ensureDnDInbox()
	id := window.ID
	window.DnDFunc = func(cmd dnd.Command) {
		q.push(DnDMsg{WindowID: id, Cmd: cmd})
	}
}

// handleDnDWake drains the inbox and acts on each message.
func (m *OS) handleDnDWake() {
	msgs, dropped := m.ensureDnDInbox().drain()
	if dropped {
		// A stream with a hole in it is not data anyone should get, and a
		// chain with a chunk missing never ends on its own.
		m.dndCancel("the drag and drop inbox overflowed")
		for _, p := range m.dndPanes {
			p.asm.Reset()
			clear(p.streams)
		}
	}
	for _, msg := range msgs {
		m.onDnD(msg)
	}
}

// dndPane is what one pane's program has said about itself, and the chunk
// assembler and data streams of that pane.
type dndPane struct {
	asm dnd.Assembler
	// accepts says the program sent t=a, offers that it sent t=o:x=1.
	accepts, offers bool
	// id is the multiplexer id (i key) the program used, echoed back on
	// everything sent to it.
	id int
	// streams are the data streams in progress from this pane, by type and
	// index: pre-sent data (p), item data for a request (e), and the text
	// drag image (p with a negative index, keyed by it).
	streams map[dndStreamKey]*dndStream
}

type dndStreamKey struct {
	typ byte
	idx int
}

// dndStream is base64 text accumulating until the chunk that ends it.
type dndStream struct {
	enc []byte
}

// dndMaxStreamBytes bounds one stream's encoded bytes, at the 64 MB of
// pre-sent data the specification asks a terminal to take.
const dndMaxStreamBytes = 64 << 20

// dndPaneFor is the record for a window, made on first use.
func (m *OS) dndPaneFor(id string) *dndPane {
	if m.dndPanes == nil {
		m.dndPanes = map[string]*dndPane{}
	}
	p := m.dndPanes[id]
	if p == nil {
		p = &dndPane{streams: map[dndStreamKey]*dndStream{}}
		m.dndPanes[id] = p
	}
	return p
}

// dndWindowClosed forgets a pane that is gone and ends a drag it took part
// in.
func (m *OS) dndWindowClosed(id string) {
	delete(m.dndPanes, id)
	if m.dndPress != nil && m.dndPress.window == id {
		m.dndPress = nil
	}
	if d := m.dndDrag; d != nil && (d.source == id || d.target == id || d.dropTarget == id) {
		if d.source == id {
			d.source = ""
		}
		if d.target == id {
			d.target = ""
		}
		if d.dropTarget == id {
			d.dropTarget = ""
		}
		m.dndCancel("a pane of the drag closed")
	}
}

// dndLog writes a line to the debug log when TUIOS_DEBUG_INTERNAL is set, as
// the kitty passthrough does. Every message in and out goes through it, which
// is how an exchange with a program is read back.
func dndLog(format string, args ...any) {
	dndLogOnce.Do(func() { dndLogOn = os.Getenv("TUIOS_DEBUG_INTERNAL") == "1" })
	if !dndLogOn {
		return
	}
	f, err := debuglog.Open(debuglog.Path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "[%s] DND: %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

var (
	dndLogOnce sync.Once
	dndLogOn   bool
)

// dndOn reports whether drag and drop is on for this client: the setting
// says so.
func (m *OS) dndOn() bool {
	return m.UserConfig != nil && m.UserConfig.Panes.DragAndDrop && !m.LearnMode
}

// dndPaneLocal reports whether a pane takes part in drag and drop: drag and
// drop is on, and the pane's shell runs on this machine. A pane opened on
// another machine, and every pane of a client attached with --host, runs
// its shell elsewhere: a local path dropped there means nothing there, and a
// path a program there offers means nothing here. Phase 4 lifts that with a
// file transfer. paneIsLocal is the one place that judges this.
func (m *OS) dndPaneLocal(w *terminal.Window) bool {
	return m.dndOn() && m.paneIsLocal(w)
}

// dndFmt is a message as the log shows it.
func dndFmt(cmd dnd.Command) string {
	return fmt.Sprintf("t=%c o=%d x=%d y=%d X=%d Y=%d m=%v payload=%q", cmd.Type, cmd.Op, cmd.X, cmd.Y, cmd.PX, cmd.PY, cmd.More, cmd.Payload)
}

// takeDaemonDnD takes the drag and drop registration the daemon kept for a
// pane, for a pane this client has heard nothing from itself: it attached
// after the program registered, which a program does once, when it starts.
// Once the client has a record of its own, the stream it reads is fresher
// than any state push. See session/dnd_registration.go.
func (m *OS) takeDaemonDnD(w *terminal.Window, ws *session.WindowState) {
	if w == nil || ws == nil || (!ws.DnDAccepts && !ws.DnDOffers) || !m.dndPaneLocal(w) {
		return
	}
	if _, seen := m.dndPanes[w.ID]; seen {
		return
	}
	p := m.dndPaneFor(w.ID)
	p.accepts, p.offers, p.id = ws.DnDAccepts, ws.DnDOffers, ws.DnDID
	dndLog("took registration of %s from the daemon: accepts=%v offers=%v", shortID(w.ID), p.accepts, p.offers)
}

// dndShared reports whether a pane's program may be heard by more than this
// client: its PTY is the daemon's, and another client can be attached. Such a
// client answers only what belongs to a drag of its own, and leaves the
// query to the daemon. A message for another client's drag gets no error
// from this one.
func (m *OS) dndShared(id string) bool {
	w := m.windowByID(id)
	return w != nil && w.DaemonMode
}

// dndTo is the pane a message goes to and the multiplexer id it carries:
// the message's own, or the one the pane's program registered with.
func (m *OS) dndTo(id string, cmdID int) (*terminal.Window, int) {
	w := m.windowByID(id)
	if w == nil {
		return nil, 0
	}
	if p := m.dndPanes[id]; p != nil && cmdID == 0 {
		cmdID = p.id
	}
	return w, cmdID
}

// dndSend writes one message to a pane's program.
func (m *OS) dndSend(id string, cmd dnd.Command) {
	w, cmdID := m.dndTo(id, cmd.ID)
	if w == nil {
		return
	}
	cmd.ID = cmdID
	dndLog("send %s %s", shortID(id), dndFmt(cmd))
	_ = w.SendInput(cmd.Encode())
}

// dndSendData writes a data response to a pane's program.
func (m *OS) dndSendData(id string, hdr dnd.Command, data []byte) {
	w, cmdID := m.dndTo(id, hdr.ID)
	if w == nil {
		return
	}
	hdr.ID = cmdID
	dndLog("send %s t=%c x=%d y=%d data=%d bytes", shortID(id), hdr.Type, hdr.X, hdr.Y, len(data))
	_ = w.SendInput(dnd.EncodeData(hdr, data))
}

// dndError writes an error message of type t (R to a drop target, E to a
// drag source) to a pane's program.
func (m *OS) dndError(id string, t byte, x, y int, errno, why string) {
	m.dndSend(id, dnd.Command{Type: t, X: x, Y: y, Payload: dnd.ErrorPayload(errno, why)})
}

// dndRefuse is dndError for a message that belongs to no drag of this
// client. A daemon's pane may be heard by another client whose drag it is,
// and that client answers; this one stays silent rather than send an error
// into an exchange it is not part of.
func (m *OS) dndRefuse(id string, t byte, x, y int, errno, why string) {
	if m.dndShared(id) {
		return
	}
	m.dndError(id, t, x, y, errno, why)
}

// onDnD acts on one message from a pane's program. A pane that takes no part
// (drag and drop off, or a pane on another machine) is not answered: the
// query goes unanswered, as in a terminal without the protocol, and the
// daemon that runs such a pane's program answers for it there.
func (m *OS) onDnD(msg DnDMsg) {
	if !m.dndPaneLocal(m.windowByID(msg.WindowID)) {
		return
	}
	p := m.dndPaneFor(msg.WindowID)
	cmd, ok := p.asm.Add(msg.Cmd)
	if !ok {
		return
	}
	if cmd.ID != 0 {
		p.id = cmd.ID
	}
	id := msg.WindowID
	dndLog("recv %s %s", shortID(id), dndFmt(cmd))
	switch cmd.Type {
	case 'q':
		// Yes, this terminal speaks the protocol. The payload names optional
		// parts; there are none yet. A daemon's pane hears it from the daemon.
		if !m.dndShared(id) {
			m.dndSend(id, dnd.Command{Type: 'q', ID: cmd.ID})
		}
	case 'a':
		// x=1 carries a machine id. Every pane of this client is local.
		if cmd.X != 1 {
			p.accepts = true
		}
	case 'A':
		p.accepts = false
		if d := m.dndDrag; d != nil && d.dropTarget == id {
			m.dndCancel("the drop target stopped taking drops")
		}
	case 'o':
		m.onDnDOffer(id, p, cmd)
	case 'p':
		m.onDnDPresent(id, p, cmd)
	case 'P':
		m.onDnDStart(id, cmd)
	case 'e':
		m.onDnDItemData(id, p, cmd)
	case 'E':
		m.onDnDSourceError(id, cmd)
	case 'm':
		m.onDnDTargetStatus(id, cmd)
	case 'r':
		m.onDnDRequest(id, cmd)
	}
}
