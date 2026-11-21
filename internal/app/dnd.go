package app

import (
	"fmt"
	"sync"
	"time"

	"tuios.dev/vt/dnd"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// DnDMsg is one message a pane's program sent, chunk by chunk.
type DnDMsg struct {
	WindowID string
	Cmd      dnd.Command
}

type dndWakeMsg struct{}

type dndInbox struct {
	mu      sync.Mutex
	msgs    []DnDMsg
	bytes   int
	dropped bool
	wake    chan struct{}
}

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

func (m *OS) ensureDnDInbox() *dndInbox {
	if m.dndIn == nil {
		m.dndIn = &dndInbox{wake: make(chan struct{}, 1)}
	}
	return m.dndIn
}

func listenDnDUntil(stop <-chan struct{}, q *dndInbox) tea.Cmd {
	return listenOnce(stop, q.wake, func(struct{}) tea.Msg { return dndWakeMsg{} })
}

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

func (m *OS) handleDnDWake() {
	msgs, dropped := m.ensureDnDInbox().drain()
	if dropped {
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

type dndPane struct {
	asm             dnd.Assembler
	accepts, offers bool
	id              int
	owner           int
	streams         map[dndStreamKey]*dndStream
}

type dndStreamKey struct {
	typ byte
	idx int
}

type dndStream struct {
	enc []byte
}

const dndMaxStreamBytes = 64 << 20

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

func dndLog(format string, args ...any) {
	if !debuglog.Enabled() {
		return
	}
	f, err := debuglog.Open(debuglog.Path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "[%s] DND: %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

func (m *OS) dndOn() bool {
	return m.UserConfig != nil && m.UserConfig.Panes.DragAndDrop && !m.LearnMode
}

func (m *OS) dndPaneLocal(w *terminal.Window) bool {
	return m.dndOn() && m.paneIsLocal(w)
}

func dndFmt(cmd dnd.Command) string {
	return fmt.Sprintf("t=%c o=%d x=%d y=%d X=%d Y=%d m=%v payload=%q", cmd.Type, cmd.Op, cmd.X, cmd.Y, cmd.PX, cmd.PY, cmd.More, cmd.Payload)
}

func (m *OS) takeDaemonDnD(w *terminal.Window, ws *session.WindowState) {
	if w == nil || ws == nil || !m.dndPaneLocal(w) {
		return
	}
	owner := 0
	if ws.DnDOwner > 0 && session.ProcessGroupAlive(ws.DnDOwner) {
		owner = ws.DnDOwner
	}
	if p, seen := m.dndPanes[w.ID]; seen {
		if owner != 0 {
			p.owner = owner
		}
		return
	}
	if !ws.DnDAccepts && !ws.DnDOffers {
		return
	}
	p := m.dndPaneFor(w.ID)
	p.accepts, p.offers, p.id, p.owner = ws.DnDAccepts, ws.DnDOffers, ws.DnDID, owner
	dndLog("took registration of %s from the daemon: accepts=%v offers=%v", shortID(w.ID), p.accepts, p.offers)
}

func (m *OS) dndShared(id string) bool {
	w := m.windowByID(id)
	return w != nil && w.DaemonMode
}

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

func (m *OS) dndSend(id string, cmd dnd.Command) {
	w, cmdID := m.dndTo(id, cmd.ID)
	if w == nil {
		return
	}
	cmd.ID = cmdID
	dndLog("send %s %s", shortID(id), dndFmt(cmd))
	_ = w.SendInput(cmd.Encode())
}

func (m *OS) dndSendData(id string, hdr dnd.Command, data []byte) {
	w, cmdID := m.dndTo(id, hdr.ID)
	if w == nil {
		return
	}
	hdr.ID = cmdID
	dndLog("send %s t=%c x=%d y=%d data=%d bytes", shortID(id), hdr.Type, hdr.X, hdr.Y, len(data))
	_ = w.SendInput(dnd.EncodeData(hdr, data))
}

func (m *OS) dndError(id string, t byte, x, y int, errno, why string) {
	m.dndSend(id, dnd.Command{Type: t, X: x, Y: y, Payload: dnd.ErrorPayload(errno, why)})
}

func (m *OS) dndRefuse(id string, t byte, x, y int, errno, why string) {
	if m.dndShared(id) {
		return
	}
	m.dndError(id, t, x, y, errno, why)
}

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
		if !m.dndShared(id) {
			m.dndSend(id, dnd.Command{Type: 'q', ID: cmd.ID})
		}
	case 'a':
		if cmd.X != 1 {
			p.accepts = true
			p.owner = dndForeground(m.windowByID(id))
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

func dndForeground(w *terminal.Window) int {
	if w == nil {
		return 0
	}
	if pgrp := w.ForegroundPgrp(); pgrp > 0 {
		return pgrp
	}
	return session.ForegroundGroup(w.ShellPgid)
}

func (m *OS) dndSweep() {
	for id, p := range m.dndPanes {
		if p.owner <= 0 || (!p.accepts && !p.offers) || session.ProcessGroupAlive(p.owner) {
			continue
		}
		dndLog("registration of %s ended: process group %d is gone", shortID(id), p.owner)
		if d := m.dndDrag; d != nil && (d.source == id || d.target == id || d.dropTarget == id) {
			m.dndCancel("the program in a pane of the drag is gone")
		}
		p.accepts, p.offers, p.owner = false, false, 0
		p.asm.Reset()
		clear(p.streams)
	}
}
