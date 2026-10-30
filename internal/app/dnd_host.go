package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/dnd"
)

// The bridge to a terminal that speaks the drag and drop protocol itself.
//
// With [panes] drag_and_drop_desktop on, tuios asks its terminal at startup
// (t=q). A terminal that answers, kitty 0.47 or later, gets tuios registered
// as a program that takes drops and offers drags, and from then on tuios is a
// client of its terminal's protocol as well as the terminal of its panes:
//
//   - A drag that comes in from outside, a file manager or a browser, reaches
//     tuios as moves and a drop. tuios routes them to the pane under the
//     pointer exactly as it routes its own drags, by the same rules and with
//     the same ring and label, and passes the pane's data requests to the
//     terminal and the terminal's data back. A pane without the protocol gets
//     the paths as a paste, which tuios asks the terminal for.
//   - A drag that starts in a pane becomes the window system's drag. The
//     terminal recognises the gesture and sends a t=o; tuios passes it to the
//     pane the button was pressed in, and passes the pane's offer, its data
//     sent ahead and its start to the terminal. The drag image the terminal
//     shows is tuios's own text, the file names and the pane they come from,
//     never the program's: a program can offer any path and call it anything,
//     and on the desktop tuios draws no label of its own. While the drag is
//     out, every event the terminal sends goes to the source pane, and the
//     data the terminal asks for comes from it.
//   - The window system's drag that comes back over the tuios window is
//     tuios's own. The terminal reports it like any other drag, but refuses to
//     serve its data (a drop into the window a drag came from is EPERM), so
//     tuios serves it from the source pane itself and only tells the terminal
//     the drop is done.
//
// With the bridge up, tuios does not recognise the gesture itself: the
// terminal does, and a drag that tuios started on its own at the same time
// would be the same drag twice. The terminal arms its gesture on every left
// drag in the window; tuios answers only the one whose press went to a pane
// that offers drags, so moving a pane, selecting text and dragging a row of
// the rail are as they were.
//
// tuios sends the terminal no machine id, so the terminal treats every pane
// as local and gives paths, not file contents. The pane rules (paneTakesDrop,
// dndSourceOK) keep a pane whose shell runs elsewhere out, as they do for a
// drag between panes.

// dndHostState is the bridge's state.
type dndHostState struct {
	asked, supported, registered bool
	asm                          dnd.Assembler
	// out is what waits to be written to the terminal. A pane's message, the
	// terminal's and a mouse event all queue here, and Update writes it after
	// every message (dndHostFlush).
	out []byte
}

// dndHostQuery asks the terminal whether it speaks the protocol.
const dndHostQuery = "\x1b]72;t=q\x1b\\"

// dndHostOn reports whether the bridge is up.
func (m *OS) dndHostOn() bool { return m.dndHost.registered }

// dndHostWanted reports whether this client may bridge drags: both switches
// on, and a terminal on this machine that tuios writes to directly. A browser
// draws through tuios-web, and an ssh client's terminal, or a client attached
// with --host, is on a machine whose paths mean nothing here.
func (m *OS) dndHostWanted() bool {
	return m.dndOn() && m.UserConfig.Panes.DragAndDropDesktop && m.Client == ClientLocal && m.AttachedHost == ""
}

// dndHostProbe asks the terminal at startup, once.
func (m *OS) dndHostProbe() tea.Cmd {
	if m.dndHost.asked || !m.dndHostWanted() {
		return nil
	}
	m.dndHost.asked = true
	dndLog("host probe sent")
	return tea.Raw(dndHostQuery)
}

// dndHostSend queues one message for the terminal.
func (m *OS) dndHostSend(cmd dnd.Command) {
	cmd.ID = 0
	dndLog("host send %s", dndFmt(cmd))
	m.dndHost.out = append(m.dndHost.out, cmd.Encode()...)
}

// dndHostSendData queues a data stream for the terminal.
func (m *OS) dndHostSendData(hdr dnd.Command, data []byte) {
	hdr.ID = 0
	dndLog("host send t=%c x=%d y=%d data=%d bytes", hdr.Type, hdr.X, hdr.Y, len(data))
	m.dndHost.out = append(m.dndHost.out, dnd.EncodeData(hdr, data)...)
}

// dndHostFlush returns the command that writes what is queued for the
// terminal.
func (m *OS) dndHostFlush() tea.Cmd {
	if len(m.dndHost.out) == 0 {
		return nil
	}
	out := string(m.dndHost.out)
	m.dndHost.out = nil
	return tea.Raw(out)
}

// DnDHostClear is what unregisters tuios from its terminal, or "" when it
// never registered. The caller writes it once the program has stopped, so
// the shell the terminal goes back to is not sent drops it never asked for.
func (m *OS) DnDHostClear() string {
	if !m.dndHost.registered {
		return ""
	}
	m.dndHost.registered = false
	return string(dnd.Command{Type: 'A'}.Encode()) + string(dnd.Command{Type: 'o', X: 2}.Encode())
}

// handleHostDnD takes an OSC 72 the terminal sent. It reports whether msg
// was one.
func (m *OS) handleHostDnD(msg tea.Msg) bool {
	ev, ok := msg.(uv.UnknownOscEvent)
	if !ok {
		return false
	}
	body, ok := strings.CutPrefix(string(ev), "\x1b]72;")
	if !ok {
		return false
	}
	body = strings.TrimSuffix(strings.TrimSuffix(body, "\x1b\\"), "\x07")
	if cmd, ok := dnd.Parse([]byte(body)); ok {
		if cmd, ok := m.dndHost.asm.Add(cmd); ok {
			dndLog("host recv %s", dndFmt(cmd))
			m.onHostDnD(cmd)
		}
	}
	return true
}

// onHostDnD acts on one message from the terminal.
func (m *OS) onHostDnD(cmd dnd.Command) {
	switch cmd.Type {
	case 'q':
		if !m.dndHost.asked || m.dndHost.supported {
			return
		}
		m.dndHost.supported = true
		m.dndHost.registered = true
		m.dndHostSend(dnd.Command{Type: 'a', Payload: []byte("text/uri-list text/plain")})
		m.dndHostSend(dnd.Command{Type: 'o', X: 1})
		m.LogInfo("The terminal speaks the drag and drop protocol: bridging drags to and from the desktop")
	case 'm':
		m.onHostMove(cmd)
	case 'M':
		m.onHostDrop(cmd)
	case 'r':
		m.onHostData(cmd)
	case 'R':
		m.onHostDataError(cmd)
	case 'o':
		m.onHostGesture()
	case 'e':
		m.onHostDragEvent(cmd)
	case 'E':
		m.onHostDragError(cmd)
	}
}

// dndHostPointer takes the pointer the terminal reported, in screen cells,
// into the drag, and to where the label is drawn: no mouse event carries it
// while the terminal holds the drag.
func (m *OS) dndHostPointer(d *dndDrag, cmd dnd.Command) {
	d.sx, d.sy = cmd.X, cmd.Y
	d.x, d.y, _ = m.PointerToLayout(cmd.X, cmd.Y)
	m.LastMouseX, m.LastMouseY = cmd.X, cmd.Y
}

// onHostMove is a drag moving over the tuios window, or leaving it.
func (m *OS) onHostMove(cmd dnd.Command) {
	d := m.dndDrag
	if cmd.X == -1 && cmd.Y == -1 {
		if d == nil || !d.viaHost() || d.phase != dndStarted {
			return
		}
		m.dndLeaveTarget()
		d.target, d.verdict = "", dropVerdict{}
		if d.fromHost {
			// The specification says to forget a drag that left undropped.
			m.dndDrag = nil
		}
		return
	}
	if d == nil {
		d = &dndDrag{phase: dndStarted, fromHost: true, presented: map[int][]byte{}, hostPending: map[int]bool{}}
		m.dndDrag = d
	} else if !d.viaHost() || d.phase != dndStarted {
		// tuios's own drag between panes, or a drop being served.
		return
	}
	if d.fromHost && len(cmd.Payload) > 0 {
		d.mimes = dnd.MimeList(cmd.Payload)
	}
	if d.fromHost && cmd.Op != 0 {
		d.flags = cmd.Op
	}
	m.dndHostPointer(d, cmd)
	m.dndHover(d.x, d.y)
	m.dndHostAnswer()
}

// dndHostAnswer tells the terminal whether tuios takes the drop where the
// pointer is, from the verdict, and which types it wants. It answers every
// move: kitty takes a move with no answer of its own as a refusal, and the
// program that started the drag then drops nothing, as it would for a program
// run in kitty directly that stayed silent.
func (m *OS) dndHostAnswer() {
	d := m.dndDrag
	v := d.verdict
	if v.Window == "" || v.Refusal != "" {
		m.dndHostSend(dnd.Command{Type: 'm'})
		return
	}
	wants := d.wants
	if p := m.dndPanes[v.Window]; p == nil || !p.accepts {
		// A pane without the protocol: tuios takes paths, else text.
		wants = nil
		for _, mime := range []string{"text/uri-list", "text/plain"} {
			if dndMimeIndex(d.mimes, mime) >= 0 {
				wants = []string{mime}
				break
			}
		}
	}
	m.dndHostSend(dnd.Command{Type: 'm', Op: 1, Payload: []byte(strings.Join(wants, " "))})
}

// onHostDrop is the drop on the tuios window.
func (m *OS) onHostDrop(cmd dnd.Command) {
	d := m.dndDrag
	if d == nil || !d.viaHost() || d.phase != dndStarted {
		m.dndHostSend(dnd.Command{Type: 'r'})
		return
	}
	if d.fromHost && len(cmd.Payload) > 0 {
		d.mimes = dnd.MimeList(cmd.Payload)
	}
	m.dndHostPointer(d, cmd)
	m.dndHover(d.x, d.y)
	w, tx, ty := m.dndTargetAt(d)
	refuse := func(why string) {
		if why != "" {
			m.ShowNotification(why, "warning", m.Settings.NotificationDuration*2)
		}
		m.dndLeaveTarget()
		m.dndHostSend(dnd.Command{Type: 'r'})
		if d.fromHost {
			m.dndDrag = nil
		}
	}
	if w == nil || w.ID == d.source {
		refuse("")
		return
	}
	if d.verdict.Refusal != "" {
		refuse(d.verdict.Refusal)
		return
	}
	if p := m.dndPanes[w.ID]; p == nil || !p.accepts {
		m.dndDropFallback(w)
		return
	}
	m.dndDeliverDrop(w, tx, ty)
}

// onHostData is a chunk of the terminal's answer to a data request of a drop
// from outside: passed to the pane that asked, or kept for a paste.
func (m *OS) onHostData(cmd dnd.Command) {
	d := m.dndDrag
	if d == nil || !d.fromHost || d.phase != dndDropped || !d.hostPending[cmd.X-1] {
		return
	}
	end := !cmd.More && len(cmd.Payload) == 0
	if d.fallback {
		if !end {
			if len(d.hostBuf)+len(cmd.Payload) > dndMaxStreamBytes {
				m.dndCancel("the dropped data is too large")
				return
			}
			d.hostBuf = append(d.hostBuf, cmd.Payload...)
			return
		}
		data, err := dnd.DecodePayload(d.hostBuf)
		if err != nil {
			m.dndFinish(0)
			return
		}
		m.dndPasteFallback(data)
		return
	}
	if end {
		delete(d.hostPending, cmd.X-1)
	}
	m.dndSend(d.dropTarget, dnd.Command{Type: 'r', X: cmd.X, Y: cmd.Y, PX: cmd.PX, PY: cmd.PY, More: cmd.More, Payload: cmd.Payload})
}

// onHostDataError is the terminal refusing a data request of a drop from
// outside.
func (m *OS) onHostDataError(cmd dnd.Command) {
	d := m.dndDrag
	if d == nil || !d.fromHost || d.phase != dndDropped {
		return
	}
	if d.fallback {
		m.dndFinish(0)
		return
	}
	m.dndSend(d.dropTarget, dnd.Command{Type: 'R', X: cmd.X, Y: cmd.Y, Payload: cmd.Payload})
}

// onHostGesture is the terminal's drag gesture in the tuios window. It goes
// to the pane the left button was pressed in, if that pane offers drags and
// may be a source; any other gesture, a pane being moved, text being
// selected, a row of the rail being carried, is left unanswered, which starts
// nothing. The terminal reports where the gesture fired, past the press by
// its threshold; the pane is told where the press was, which is what the
// person took hold of.
func (m *OS) onHostGesture() {
	press := m.dndPress
	if d := m.dndDrag; d != nil && d.relay && d.phase < dndStarted {
		m.dndDrag = nil
	}
	if press == nil || m.dndDrag != nil {
		return
	}
	w := m.windowByID(press.window)
	if w == nil || !m.dndSourceOK(w) {
		return
	}
	if p := m.dndPanes[w.ID]; p == nil || !p.offers {
		return
	}
	m.dndPress = nil
	px := m.dndPixel(w, press.termX, press.termY)
	m.dndDrag = &dndDrag{phase: dndOffered, source: w.ID, relay: true, x: press.x, y: press.y,
		presented: map[int][]byte{}, hostPending: map[int]bool{}}
	m.dndSend(w.ID, dnd.Command{Type: 'o', X: press.termX, Y: press.termY, PX: px.X, PY: px.Y})
}

// dndHostImage gives the terminal tuios's own drag image for a relayed drag:
// the file names and the pane they come from, as the label between panes
// says them. The program's image is never forwarded, since a program can
// offer any path and call it anything.
func (m *OS) dndHostImage() {
	text := m.dndLabelWhat() + " from " + m.dndLabelFrom()
	m.dndHostSendData(dnd.Command{Type: 'p', X: -1, Op: 1024, PX: 1, PY: 1}, []byte(text))
}

// onHostDragEvent is an event of the window system's drag of a pane's offer:
// accepted, operation changed, dropped, finished, or a request for data. It
// goes to the source pane as the terminal sent it.
func (m *OS) onHostDragEvent(cmd dnd.Command) {
	d := m.dndDrag
	if d == nil || !d.relay {
		return
	}
	if cmd.X == 5 {
		d.hostPending[cmd.Y] = true
	}
	m.dndSend(d.source, dnd.Command{Type: 'e', X: cmd.X, Y: cmd.Y, Op: cmd.Op})
	if cmd.X == 4 {
		m.dndDrag = nil
		m.dndPress = nil
	}
}

// onHostDragError is the terminal's answer to the start of a pane's drag, OK
// or an error, or an error that ended it. It goes to the source pane.
func (m *OS) onHostDragError(cmd dnd.Command) {
	d := m.dndDrag
	if d == nil || !d.relay {
		return
	}
	m.dndSend(d.source, dnd.Command{Type: 'E', Y: cmd.Y, Payload: cmd.Payload})
	if string(cmd.Payload) == "OK" && d.phase == dndBuilding {
		d.phase = dndStarted
		return
	}
	m.dndDrag = nil
}
