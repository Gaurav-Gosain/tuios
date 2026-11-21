package app

import (
	"path"
	"strconv"
	"strings"
	"time"

	"tuios.dev/vt/dnd"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/vt"
)

type dndPhase int

const (
	dndOffered dndPhase = iota + 1
	dndBuilding
	dndStarted
	dndDropped
)

type dndPress struct {
	window       string
	x, y         int
	termX, termY int
}

type dndDrag struct {
	phase       dndPhase
	source      string
	released    bool
	flags       int
	mimes       []string
	presented   map[int][]byte
	names       string
	namesOf     []byte
	icon        string
	x, y        int
	sx, sy      int
	verdict     dropVerdict
	target      string
	op          int
	wants       []string
	told        bool
	dropTarget  string
	pending     map[int]bool
	fallback    bool
	fallbackIdx int
	fromHost    bool
	relay       bool
	hostPending map[int]bool
	hostBuf     []byte
	gen         int
	active      time.Time
}

const dndDropTimeout = 30 * time.Second

type dndDropTimeoutMsg struct{ gen int }

func (m *OS) dndDropArm(d *dndDrag) {
	m.dndDropGen++
	d.gen = m.dndDropGen
	d.active = time.Now()
	m.dndDropArmed = true
}

func (d *dndDrag) dndDropMoved() { d.active = time.Now() }

func (m *OS) dndDropDeadline() tea.Cmd {
	if !m.dndDropArmed {
		return nil
	}
	m.dndDropArmed = false
	d := m.dndDrag
	if d == nil || d.phase != dndDropped {
		return nil
	}
	return dndDropTick(d.gen, dndDropTimeout)
}

func dndDropTick(gen int, after time.Duration) tea.Cmd {
	return tea.Tick(after, func(time.Time) tea.Msg { return dndDropTimeoutMsg{gen: gen} })
}

func (m *OS) onDnDDropTimeout(msg dndDropTimeoutMsg) tea.Cmd {
	d := m.dndDrag
	if d == nil || d.phase != dndDropped || d.gen != msg.gen {
		return nil
	}
	if left := dndDropTimeout - time.Since(d.active); left > 0 {
		return dndDropTick(msg.gen, left)
	}
	m.dndCancel("the drop target did not finish the drop")
	return nil
}

func (d *dndDrag) viaHost() bool { return d.fromHost || d.relay }

const dndMaxMimes = 64

// DnDNotePress records a left press tuios forwarded to a pane. It is the arm
// of the gesture; nothing is sent until the pointer moves.
func (m *OS) DnDNotePress(w *terminal.Window, x, y, termX, termY int) {
	m.dndPress = nil
	m.dndSweep()
	if !m.dndSourceOK(w) {
		return
	}
	if p := m.dndPanes[w.ID]; p == nil || !p.offers {
		return
	}
	if d := m.dndDrag; d != nil {
		if d.phase >= dndStarted {
			return
		}
		m.dndDrag = nil
	}
	m.dndPress = &dndPress{window: w.ID, x: x, y: y, termX: termX, termY: termY}
	dndLog("press in %s at cell (%d,%d)", shortID(w.ID), termX, termY)
}

func (m *OS) dndSourceOK(w *terminal.Window) bool {
	if !m.dndPaneLocal(w) || w.CwdHost != "" {
		return false
	}
	return !remoteShellPrograms[paneProgram(w)]
}

// DnDActive reports whether motion events must reach the handler for a drag:
// one is in flight, or a press is waiting to become one.
func (m *OS) DnDActive() bool {
	return m.dndPress != nil || m.dndDrag != nil
}

// DnDMotion takes a pointer motion in layout cells. It reports whether the
// drag consumed it, in which case no pane gets it.
func (m *OS) DnDMotion(x, y int, button tea.MouseButton) bool {
	d := m.dndDrag
	if d != nil && d.viaHost() {
		return false
	}
	if d != nil {
		d.x, d.y = x, y
		d.sx, d.sy = m.ScreenPoint(x, y)
	}
	if button == tea.MouseNone {
		if m.dndPress != nil {
			m.dndPress = nil
		}
		if d != nil && d.phase >= dndStarted && d.phase < dndDropped {
			m.dndCancel("the pointer was released out of sight")
			return true
		}
		if d != nil && d.phase < dndStarted {
			d.released = true
		}
		return false
	}
	if d != nil && d.phase == dndStarted {
		m.dndHover(x, y)
		return true
	}
	if d != nil && d.phase == dndDropped {
		return false
	}
	press := m.dndPress
	if press == nil || d != nil || button != tea.MouseLeft {
		return false
	}
	if x == press.x && y == press.y {
		return false
	}
	if m.dndHostOn() {
		return false
	}
	w := m.windowByID(press.window)
	if w == nil {
		m.dndPress = nil
		return false
	}
	px := m.dndPixel(w, press.termX, press.termY)
	sx, sy := m.ScreenPoint(x, y)
	m.dndDrag = &dndDrag{phase: dndOffered, source: press.window, x: x, y: y, sx: sx, sy: sy, presented: map[int][]byte{}}
	m.dndSend(press.window, dnd.Command{Type: 'o', X: press.termX, Y: press.termY, PX: px.X, PY: px.Y})
	return false
}

func (m *OS) dndPixel(w *terminal.Window, termX, termY int) vt.MousePixel {
	if px := m.PointerPixelIn(w.CellPixelWidth, w.CellPixelHeight, termX, termY); px.OK {
		return px
	}
	return vt.MousePixel{X: termX * max(w.CellPixelWidth, 0), Y: termY * max(w.CellPixelHeight, 0)}
}

// DnDRelease takes the button coming up. It reports whether the drag
// consumed the release, in which case no pane gets it.
func (m *OS) DnDRelease(x, y int) bool {
	m.dndPress = nil
	d := m.dndDrag
	if d == nil || d.viaHost() {
		return false
	}
	d.x, d.y = x, y
	d.sx, d.sy = m.ScreenPoint(x, y)
	switch d.phase {
	case dndOffered, dndBuilding:
		d.released = true
		return false
	case dndStarted:
		m.dndDrop(x, y)
		return true
	}
	return false
}

func (m *OS) dndTargetAt(d *dndDrag) (*terminal.Window, int, int) {
	w := m.dropTargetAt(d.sx, d.sy)
	if w == nil {
		return nil, 0, 0
	}
	tx, ty, ok := w.ScreenToTerminal(d.x, d.y)
	if !ok {
		return nil, 0, 0
	}
	return w, tx, ty
}

const dndRefusedByProgram = "The program in this pane does not take the drop here."

const dndNoLocalFile = "The drop names no file on this machine."

const dndNoBracketedPaste = "The program in this pane has no bracketed paste on. Text is not dropped there."

func (m *OS) dndJudge(w *terminal.Window) dropVerdict {
	d := m.dndDrag
	if p := m.dndPanes[w.ID]; p != nil && p.accepts {
		v := dropVerdict{Window: w.ID, Pane: pipPaneName(w)}
		if v.Program, v.Refusal = m.paneTakesDrop(w); v.Refusal == "" {
			v.Refusal = dndRefusedByProgram
		}
		return v
	}
	if idx := dndMimeIndex(d.mimes, "text/uri-list"); idx >= 0 {
		data, ok := d.presented[idx]
		if !ok {
			return m.judgeDropPaths(w, nil)
		}
		paths, ok := dndURIPaths(string(data))
		if !ok {
			return dropVerdict{Window: w.ID, Pane: pipPaneName(w), Refusal: dndNoLocalFile}
		}
		return m.judgeDropPaths(w, paths)
	}
	if dndMimeIndex(d.mimes, "text/plain") >= 0 {
		v := m.judgeDropPaths(w, nil)
		if v.Refusal == "" && !(w.Terminal != nil && w.Terminal.BracketedPasteEnabled()) {
			v.Refusal = dndNoBracketedPaste
		}
		return v
	}
	return dropVerdict{Window: w.ID, Pane: pipPaneName(w), Refusal: "The program in this pane takes neither files nor text from a drag."}
}

func (m *OS) dndHover(x, y int) {
	d := m.dndDrag
	d.x, d.y = x, y
	w, tx, ty := m.dndTargetAt(d)
	id := ""
	if w != nil {
		id = w.ID
	}
	dndLog("hover layout=(%d,%d) screen=(%d,%d) -> pane=%s cell=(%d,%d)", x, y, d.sx, d.sy, shortID(id), tx, ty)
	if id != d.target {
		m.dndLeaveTarget()
		d.target = id
		d.verdict = dropVerdict{}
		if w != nil {
			d.verdict = m.dndJudge(w)
		}
	}
	m.dropPointer(d.verdict)
	if w == nil {
		return
	}
	if d.verdict.Refusal != "" && d.verdict.Refusal != dndRefusedByProgram {
		return
	}
	if p := m.dndPanes[id]; p != nil && p.accepts {
		px := m.dndPixel(w, tx, ty)
		m.dndSend(id, dnd.Command{Type: 'm', X: tx, Y: ty, PX: px.X, PY: px.Y, Op: d.flags,
			Payload: []byte(strings.Join(d.mimes, " "))})
	}
}

func (m *OS) dndLeaveTarget() {
	d := m.dndDrag
	if d.target == "" {
		return
	}
	if p := m.dndPanes[d.target]; p != nil && p.accepts {
		m.dndSend(d.target, dnd.Command{Type: 'm', X: -1, Y: -1})
	}
	if d.told && !d.viaHost() {
		m.dndSend(d.source, dnd.Command{Type: 'e', X: 2, Op: 0})
	}
	d.target, d.op, d.wants, d.told = "", 0, nil, false
}

func (m *OS) dndDrop(x, y int) {
	d := m.dndDrag
	m.dndHover(x, y)
	w, tx, ty := m.dndTargetAt(d)
	if w == nil || w.ID == d.source {
		m.dndCancel("")
		return
	}
	if d.verdict.Refusal != "" {
		m.ShowNotification(d.verdict.Refusal, "warning", m.Settings.NotificationDuration*2)
		m.dndCancel("")
		return
	}
	p := m.dndPanes[w.ID]
	if p == nil || !p.accepts {
		m.dndDropFallback(w)
		return
	}
	m.dndDeliverDrop(w, tx, ty)
}

func (m *OS) dndDeliverDrop(w *terminal.Window, tx, ty int) {
	d := m.dndDrag
	d.phase = dndDropped
	d.dropTarget = w.ID
	d.pending = map[int]bool{}
	m.dndDropArm(d)
	if !d.viaHost() {
		m.dndSend(d.source, dnd.Command{Type: 'e', X: 3})
	}
	px := m.dndPixel(w, tx, ty)
	m.dndSend(w.ID, dnd.Command{Type: 'M', X: tx, Y: ty, PX: px.X, PY: px.Y, Op: d.op,
		Payload: []byte(strings.Join(d.mimes, " "))})
}

func (m *OS) dndDropFallback(w *terminal.Window) {
	d := m.dndDrag
	idx := dndMimeIndex(d.mimes, "text/uri-list")
	if idx < 0 {
		idx = dndMimeIndex(d.mimes, "text/plain")
	}
	if idx < 0 {
		m.dndCancel("")
		return
	}
	d.phase = dndDropped
	d.dropTarget = w.ID
	d.fallback, d.fallbackIdx = true, idx
	d.pending = map[int]bool{}
	m.dndDropArm(d)
	if d.fromHost {
		d.hostPending[idx] = true
		d.hostBuf = nil
		m.dndHostSend(dnd.Command{Type: 'r', X: idx + 1})
		return
	}
	if !d.relay {
		m.dndSend(d.source, dnd.Command{Type: 'e', X: 3})
	}
	if data, ok := d.presented[idx]; ok {
		m.dndPasteFallback(data)
		return
	}
	d.pending[idx] = true
	m.dndSend(d.source, dnd.Command{Type: 'e', X: 5, Y: idx})
}

func (m *OS) dndPasteFallback(data []byte) {
	d := m.dndDrag
	w := m.windowByID(d.dropTarget)
	if w == nil {
		m.dndFinish(0)
		return
	}
	var v dropVerdict
	if d.mimes[d.fallbackIdx] == "text/uri-list" {
		if paths, ok := dndURIPaths(string(data)); ok {
			v = m.judgeDropPaths(w, paths)
		} else {
			v = dropVerdict{Window: w.ID, Pane: pipPaneName(w), Refusal: dndNoLocalFile}
		}
	} else if v = m.judgeDropPaths(w, nil); v.Refusal == "" {
		if w.Terminal == nil || !w.Terminal.BracketedPasteEnabled() {
			v.Refusal = dndNoBracketedPaste
		} else {
			v.Text = vt.SanitizePaste(string(data))
		}
	}
	if v.Refusal == "" && v.Text == "" {
		v.Refusal = "The drop holds nothing to paste."
	}
	m.deliverDrop(v, dropSource{}, false)
	op := 0
	if v.Refusal == "" {
		op = 1
	}
	m.dndFinish(op)
}

func dndMimeIndex(mimes []string, mime string) int {
	for i, m := range mimes {
		if m == mime {
			return i
		}
	}
	return -1
}

func (m *OS) dndFinish(op int) {
	d := m.dndDrag
	if d == nil {
		return
	}
	if d.viaHost() {
		m.dndHostSend(dnd.Command{Type: 'r', Op: op})
		if d.relay {
			d.phase, d.dropTarget, d.fallback, d.target, d.verdict = dndStarted, "", false, "", dropVerdict{}
			return
		}
		m.dndDrag = nil
		m.dndPress = nil
		m.ResetPointerShape()
		return
	}
	y := 1
	if op != 0 {
		y = 0
	}
	if d.source != "" {
		m.dndSend(d.source, dnd.Command{Type: 'e', X: 4, Y: y})
	}
	m.dndDrag = nil
	m.dndPress = nil
	m.ResetPointerShape()
}

func (m *OS) dndCancel(why string) {
	d := m.dndDrag
	if d == nil {
		return
	}
	if why != "" {
		m.LogInfo("Drag and drop ended: %s", why)
	}
	switch {
	case d.phase == dndDropped && !d.fallback && d.dropTarget != "":
		m.dndError(d.dropTarget, 'R', 0, 0, "ECANCELED", "the drag ended")
	case d.phase == dndStarted:
		m.dndLeaveTarget()
	}
	switch {
	case d.fromHost && d.phase == dndDropped:
		m.dndHostSend(dnd.Command{Type: 'r'})
	case d.relay:
		m.dndHostSend(dnd.Command{Type: 'E', Y: -1})
	}
	if d.source != "" && d.phase >= dndStarted {
		m.dndSend(d.source, dnd.Command{Type: 'e', X: 4, Y: 1})
	}
	if d.phase >= dndStarted {
		m.ResetPointerShape()
	}
	m.dndDrag = nil
	m.dndPress = nil
}

func (m *OS) onDnDOffer(id string, p *dndPane, cmd dnd.Command) {
	switch cmd.X {
	case 1:
		p.offers = true
		p.owner = dndForeground(m.windowByID(id))
		return
	case 2:
		p.offers = false
		if d := m.dndDrag; d != nil && d.source == id {
			m.dndCancel("the source stopped offering drags")
		}
		return
	}
	d := m.dndDrag
	if d == nil || d.source != id || d.phase > dndBuilding {
		m.dndRefuse(id, 'E', 0, 0, "EPERM", "no drag gesture in this pane")
		return
	}
	if d.released {
		m.dndError(id, 'E', 0, 0, "EPERM", "the button was released")
		m.dndDrag = nil
		return
	}
	mimes := dnd.MimeList(cmd.Payload)
	if len(mimes) == 0 || len(mimes) > dndMaxMimes || cmd.Op < 1 || cmd.Op > 3 {
		m.dndError(id, 'E', 0, 0, "EINVAL", "an offer needs operations and MIME types")
		m.dndDrag = nil
		return
	}
	d.phase = dndBuilding
	d.flags = cmd.Op
	d.mimes = append(d.mimes[:0], mimes...)
	if d.relay {
		m.dndHostSend(dnd.Command{Type: 'o', Op: cmd.Op, Payload: []byte(strings.Join(mimes, " "))})
	}
}

func (m *OS) onDnDPresent(id string, p *dndPane, cmd dnd.Command) {
	d := m.dndDrag
	if d == nil || d.source != id || d.phase != dndBuilding {
		m.dndRefuse(id, 'E', 0, 0, "EINVAL", "no drag is being built")
		return
	}
	if cmd.X >= 0 && cmd.X >= len(d.mimes) {
		m.dndError(id, 'E', 0, 0, "EINVAL", "pre-sent data index out of range")
		m.dndCancel("the source sent data for a type it did not offer")
		return
	}
	if cmd.X < 0 && cmd.Y != 0 {
		return
	}
	data, done, ok := m.dndStreamAdd(p, 'p', cmd)
	if !ok {
		m.dndError(id, 'E', 0, 0, "EFBIG", "too much pre-sent data")
		m.dndCancel("the source sent too much data ahead")
		return
	}
	if d.relay && cmd.X >= 0 {
		fwd := cmd
		fwd.ID = 0
		m.dndHostSend(fwd)
	}
	if !done {
		return
	}
	if cmd.X >= 0 {
		d.presented[cmd.X] = data
		return
	}
	if cmd.X == -1 {
		d.icon = dndLabelText(string(data), 40)
	}
}

func (m *OS) dndStreamAdd(p *dndPane, typ byte, cmd dnd.Command) (data []byte, done, ok bool) {
	key := dndStreamKey{typ: typ, idx: cmd.X}
	if typ == 'e' {
		key.idx = cmd.Y
	}
	s := p.streams[key]
	if s == nil {
		s = &dndStream{}
		p.streams[key] = s
	}
	if len(s.enc)+len(cmd.Payload) > dndMaxStreamBytes {
		delete(p.streams, key)
		return nil, false, false
	}
	s.enc = append(s.enc, cmd.Payload...)
	if cmd.More || len(cmd.Payload) > 0 {
		return nil, false, true
	}
	delete(p.streams, key)
	data, err := dnd.DecodePayload(s.enc)
	if err != nil {
		return nil, true, false
	}
	return data, true, true
}

func (m *OS) onDnDStart(id string, cmd dnd.Command) {
	d := m.dndDrag
	if cmd.X >= 0 {
		return
	}
	if d == nil || d.source != id || d.phase != dndBuilding {
		m.dndRefuse(id, 'E', 0, 0, "EINVAL", "no drag is being built")
		return
	}
	if d.released {
		m.dndError(id, 'E', 0, 0, "EPERM", "the button was released")
		m.dndCancel("")
		return
	}
	if p := m.dndPanes[id]; p != nil {
		for key, s := range p.streams {
			if key.typ != 'p' {
				continue
			}
			delete(p.streams, key)
			data, err := dnd.DecodePayload(s.enc)
			if err != nil {
				continue
			}
			switch {
			case key.idx >= 0 && key.idx < len(d.mimes):
				d.presented[key.idx] = data
			case key.idx == -1:
				d.icon = dndLabelText(string(data), 40)
			}
		}
	}
	if d.relay {
		m.dndHostImage()
		m.dndHostSend(dnd.Command{Type: 'P', X: -1})
		return
	}
	m.dndSend(id, dnd.Command{Type: 'E', Payload: []byte("OK")})
	d.phase = dndStarted
	m.dndHover(d.x, d.y)
}

func (m *OS) onDnDItemData(id string, p *dndPane, cmd dnd.Command) {
	d := m.dndDrag
	if d != nil && d.relay && d.source == id && d.hostPending[cmd.Y] {
		d.dndDropMoved()
		m.dndHostSend(dnd.Command{Type: 'e', Y: cmd.Y, More: cmd.More, Payload: cmd.Payload})
		if !cmd.More && len(cmd.Payload) == 0 {
			delete(d.hostPending, cmd.Y)
		}
		return
	}
	if d == nil || d.source != id {
		m.dndRefuse(id, 'E', 0, cmd.Y, "EINVAL", "no request is open for this data")
		return
	}
	if d.phase != dndDropped || cmd.Y < 0 || cmd.Y >= len(d.mimes) {
		m.dndError(id, 'E', 0, cmd.Y, "EINVAL", "no request is open for this data")
		m.dndCancel("the source sent data nobody asked for")
		return
	}
	d.dndDropMoved()
	data, done, ok := m.dndStreamAdd(p, 'e', cmd)
	if !ok {
		m.dndError(id, 'E', 0, cmd.Y, "EFBIG", "too much data")
		m.dndCancel("the source sent too much data")
		return
	}
	if !done {
		return
	}
	d.presented[cmd.Y] = data
	if !d.pending[cmd.Y] {
		return
	}
	delete(d.pending, cmd.Y)
	if d.fallback {
		if cmd.Y == d.fallbackIdx {
			m.dndPasteFallback(data)
		}
		return
	}
	m.dndSendData(d.dropTarget, dnd.Command{Type: 'r', X: cmd.Y + 1}, data)
}

func (m *OS) onDnDSourceError(id string, cmd dnd.Command) {
	d := m.dndDrag
	if d == nil || d.source != id {
		return
	}
	if d.relay && (cmd.Y == -1 || d.hostPending[cmd.Y]) {
		m.dndHostSend(dnd.Command{Type: 'E', Y: cmd.Y, Payload: cmd.Payload})
		if cmd.Y == -1 {
			m.dndDrag = nil
		}
		return
	}
	if cmd.Y == -1 || d.phase != dndDropped {
		m.dndCancel("the source cancelled the drag")
		return
	}
	errno, _, _ := strings.Cut(string(cmd.Payload), ":")
	if errno == "" {
		errno = "EUNKNOWN"
	}
	if !d.fallback && d.dropTarget != "" {
		m.dndError(d.dropTarget, 'R', cmd.Y+1, 0, errno, "the source could not provide the data")
	}
	m.dndFinish(0)
}

func (m *OS) onDnDTargetStatus(id string, cmd dnd.Command) {
	d := m.dndDrag
	if d == nil || d.phase != dndStarted || d.target != id {
		return
	}
	if len(cmd.Payload) > 0 {
		d.wants = dnd.MimeList(cmd.Payload)
	}
	op := cmd.Op
	if op < 0 || op > 2 {
		op = 0
	}
	if op == d.op {
		return
	}
	d.op = op
	if d.verdict.Window == id && (d.verdict.Refusal == "" || d.verdict.Refusal == dndRefusedByProgram) {
		d.verdict.Refusal = ""
		if op == 0 {
			d.verdict.Refusal = dndRefusedByProgram
		}
		m.dropPointer(d.verdict)
	}
	if d.viaHost() {
		m.dndHostAnswer()
		return
	}
	if op != 0 && !d.told {
		idx := 0
		if len(d.wants) > 0 {
			if i := dndMimeIndex(d.mimes, d.wants[0]); i >= 0 {
				idx = i
			}
		}
		m.dndSend(d.source, dnd.Command{Type: 'e', X: 1, Y: idx})
		d.told = true
	}
	m.dndSend(d.source, dnd.Command{Type: 'e', X: 2, Op: op})
}

func (m *OS) onDnDRequest(id string, cmd dnd.Command) {
	d := m.dndDrag
	if d == nil || d.phase != dndDropped || d.dropTarget != id || d.fallback {
		m.dndRefuse(id, 'R', cmd.X, cmd.Y, "EPERM", "drop data can only be requested after a drop")
		return
	}
	d.dndDropMoved()
	if cmd.X == 0 {
		m.dndFinish(cmd.Op)
		return
	}
	idx := cmd.X - 1
	switch {
	case idx < 0 || idx >= len(d.mimes):
		m.dndError(id, 'R', cmd.X, cmd.Y, "ENOENT", "no such MIME type in the drop")
		m.dndFinish(0)
	case cmd.Y != 0 || cmd.PY != 0:
		m.dndError(id, 'R', cmd.X, cmd.Y, "EINVAL", "file contents are not served between panes")
	case d.fromHost:
		d.hostPending[idx] = true
		m.dndHostSend(dnd.Command{Type: 'r', X: cmd.X})
	default:
		if data, ok := d.presented[idx]; ok {
			m.dndSendData(id, dnd.Command{Type: 'r', X: cmd.X}, data)
			return
		}
		if !d.pending[idx] {
			d.pending[idx] = true
			m.dndSend(d.source, dnd.Command{Type: 'e', X: 5, Y: idx})
		}
	}
}

func dndURILines(list string) []string {
	var lines []string
	for line := range strings.SplitSeq(list, "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines
}

func dndURIPaths(list string) ([]string, bool) {
	lines := dndURILines(list)
	if len(lines) == 0 {
		return nil, false
	}
	return parseDroppedPaths(strings.Join(lines, "\n"))
}

func dndLabelText(s string, n int) string {
	return overlay.Truncate(strings.Join(strings.Fields(printableTitle(s)), " "), n)
}

func (m *OS) renderDnDLabel() *lipgloss.Layer {
	d := m.dndDrag
	if d == nil || d.phase != dndStarted {
		return nil
	}
	if d.viaHost() && d.verdict.Window == "" {
		return nil
	}
	text := m.dndLabelWhat() + " from " + m.dndLabelFrom()
	v := d.verdict
	switch {
	case v.Window == "":
	case v.Refusal != "":
		text += "  " + v.Refusal
	default:
		text += m.dropArrow() + dropTargetText(v)
	}
	if d.icon != "" {
		text += "  " + d.icon
	}
	return m.renderDropLabel(text, v.Refusal != "", "dnd-label")
}

func (m *OS) dndLabelWhat() string {
	d := m.dndDrag
	what := ""
	if idx := dndMimeIndex(d.mimes, "text/uri-list"); idx >= 0 {
		if data, ok := d.presented[idx]; ok {
			if !sameBytes(data, d.namesOf) {
				d.names = dndNamesText(data)
				d.namesOf = data
			}
			what = d.names
		} else {
			what = "files"
		}
	} else if len(d.mimes) > 0 {
		what = d.mimes[0]
	}
	return dndLabelText(what, 48)
}

func sameBytes(a, b []byte) bool {
	return len(a) > 0 && len(a) == len(b) && &a[0] == &b[0]
}

func dndNamesText(data []byte) string {
	names := dndURILines(string(data))
	if paths, ok := dndURIPaths(string(data)); ok {
		names = make([]string, len(paths))
		for i, p := range paths {
			names[i] = path.Base(p)
		}
	}
	switch {
	case len(names) == 0:
		return "no files"
	case len(names) <= 3:
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:3], ", ") + " +" + strconv.Itoa(len(names)-3)
}

func (m *OS) dndLabelFrom() string {
	d := m.dndDrag
	if d.fromHost {
		return "the desktop"
	}
	if w := m.windowByID(d.source); w != nil {
		return m.dndPaneName(w)
	}
	return "a pane"
}

func (m *OS) dndPaneName(w *terminal.Window) string {
	name := printableTitle(contextMenuWindowName(m, m.windowIndexByID(w.ID), "pane "+shortID(w.ID)))
	if w.Host != "" {
		name = w.Host + ":" + name
	}
	return overlay.Truncate(name, 32)
}
