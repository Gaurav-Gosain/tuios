package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"tuios.dev/vt/dnd"
)

type dndHostState struct {
	asked, supported, registered bool
	asm                          dnd.Assembler
	out                          []byte
	writing                      bool
}

type dndHostWrittenMsg struct{}

func (m *OS) dndHostOn() bool { return m.dndHost.registered }

func (m *OS) dndHostWanted() bool {
	return m.dndOn() && m.UserConfig.Panes.DragAndDropDesktop && m.Client == ClientLocal && m.AttachedHost == "" && !viewerIsRemote()
}

func (m *OS) dndHostSync() {
	wanted := m.dndHostWanted()
	switch {
	case wanted && !m.dndHost.asked:
		m.dndHost.asked = true
		dndLog("host probe sent")
		m.dndHostSend(dnd.Command{Type: 'q'})
	case !wanted && m.dndHost.asked:
		if m.dndHost.registered {
			if d := m.dndDrag; d != nil && d.viaHost() {
				m.dndCancel("the bridge was switched off")
			}
			m.dndHostSend(dnd.Command{Type: 'A'})
			m.dndHostSend(dnd.Command{Type: 'o', X: 2})
		}
		m.dndHost.asked, m.dndHost.supported, m.dndHost.registered = false, false, false
	}
}

func (m *OS) dndHostSend(cmd dnd.Command) {
	cmd.ID = 0
	dndLog("host send %s", dndFmt(cmd))
	m.dndHost.out = append(m.dndHost.out, cmd.Encode()...)
}

func (m *OS) dndHostSendData(hdr dnd.Command, data []byte) {
	hdr.ID = 0
	dndLog("host send t=%c x=%d y=%d data=%d bytes", hdr.Type, hdr.X, hdr.Y, len(data))
	m.dndHost.out = append(m.dndHost.out, dnd.EncodeData(hdr, data)...)
}

func (m *OS) dndHostFlush() tea.Cmd {
	if m.dndHost.writing || len(m.dndHost.out) == 0 {
		return nil
	}
	out := string(m.dndHost.out)
	m.dndHost.out = nil
	m.dndHost.writing = true
	return tea.Sequence(tea.Raw(out), func() tea.Msg { return dndHostWrittenMsg{} })
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
		if cmd.Type != 'q' && !m.dndHost.registered {
			dndLog("host sent %s with the bridge down; ignored", dndFmt(cmd))
			return true
		}
		if cmd, ok := m.dndHost.asm.Add(cmd); ok {
			dndLog("host recv %s", dndFmt(cmd))
			m.onHostDnD(cmd)
		}
	}
	return true
}

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

func (m *OS) dndHostPointer(d *dndDrag, cmd dnd.Command) {
	d.sx, d.sy = cmd.X, cmd.Y
	d.x, d.y, _ = m.PointerToLayout(cmd.X, cmd.Y)
	m.LastMouseX, m.LastMouseY = cmd.X, cmd.Y
}

func (m *OS) onHostMove(cmd dnd.Command) {
	d := m.dndDrag
	if cmd.X == -1 && cmd.Y == -1 {
		if d == nil || !d.viaHost() || d.phase != dndStarted {
			return
		}
		m.dndLeaveTarget()
		d.target, d.verdict = "", dropVerdict{}
		m.ResetPointerShape()
		if d.fromHost {
			m.dndDrag = nil
		}
		return
	}
	if d == nil {
		m.dndSweep()
		d = &dndDrag{phase: dndStarted, fromHost: true, presented: map[int][]byte{}, hostPending: map[int]bool{}}
		m.dndDrag = d
	} else if !d.viaHost() || d.phase != dndStarted {
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

func (m *OS) dndHostAnswer() {
	d := m.dndDrag
	v := d.verdict
	if v.Window == "" || v.Refusal != "" {
		m.dndHostSend(dnd.Command{Type: 'm'})
		return
	}
	wants := d.wants
	if p := m.dndPanes[v.Window]; p == nil || !p.accepts {
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
		m.ResetPointerShape()
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

func (m *OS) onHostData(cmd dnd.Command) {
	d := m.dndDrag
	if d == nil || !d.fromHost || d.phase != dndDropped || !d.hostPending[cmd.X-1] {
		return
	}
	d.dndDropMoved()
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

func (m *OS) dndHostImage() {
	text := m.dndLabelWhat() + " from " + m.dndLabelFrom()
	m.dndHostSendData(dnd.Command{Type: 'p', X: -1, Op: 1024, PX: 1, PY: 1}, []byte(text))
}

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
		m.ResetPointerShape()
	}
}

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
	m.ResetPointerShape()
}

func (m *OS) dndHostMouseSeen() {
	if d := m.dndDrag; d != nil && d.fromHost && d.phase == dndStarted {
		m.dndCancel("the desktop drag ended out of sight")
	}
}
