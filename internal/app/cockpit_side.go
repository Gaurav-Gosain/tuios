package app

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The side column of the agents view (DESIGN.md 2.4): a live copy of the
// selected agent's pane, the files its last turn changed, and its turn
// checkpoints. At 170 columns and wider it sits beside the focus. Narrower,
// the same pieces are focus tabs: Checkpoints, and Pane for the live copy at
// the focus's size.
//
// The live copy is the picture-in-picture reader (pip.go pipCells): the cells
// the client's emulator already holds, read again only when the pane had
// output, with no second emulator and no stream of its own. A pane of this
// session on another workspace is primed from the daemon and kept streamed
// while it is the live pane, as a pinned PiP pane is. A pane of another
// session or machine has no cells here, and the box says so.
//
// The checkpoints are read with list-checkpoints when the selection moves to
// an agent or its state changes, never on a timer.

// ckSideWidth is the side column's width at ckWide columns and more.
const ckSideWidth = 46

// ckLiveRows is how many rows the live box holds in the side column.
const ckLiveRows = 14

// ckCheckpointsShown is how many checkpoints the side column lists.
const ckCheckpointsShown = 5

// ckCheckpoint is one checkpoint as list-checkpoints sends it.
type ckCheckpoint struct {
	N     int    `json:"n"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	State string `json:"state"`
	At    int64  `json:"at"`
}

// ckCheckpoints are an agent's checkpoints, read for key: the agent and the
// time its state last changed.
type ckCheckpoints struct {
	key     string
	list    []ckCheckpoint
	err     string
	loading bool
}

// ckCheckpointsMsg is a list-checkpoints reply.
type ckCheckpointsMsg struct {
	agent, key string
	list       []ckCheckpoint
	err        error
}

// cockpitLiveWindow is the selected agent's pane when this client holds its
// cells: a pane of the attached session on this machine.
func (m *OS) cockpitLiveWindow(e sidebarAgentEntry) *terminal.Window {
	if e.Host != "" || e.Foreign || e.WindowID == "" {
		return nil
	}
	w := m.windowByID(e.WindowID)
	if w == nil || w.Terminal == nil {
		return nil
	}
	return w
}

// cockpitLiveFollow makes the selected agent's pane the live pane: a pane on
// another workspace is primed and kept streamed while it is, and the one
// before goes back to how its workspace has it. It runs after input, never
// in a render.
func (m *OS) cockpitLiveFollow() {
	ck := &m.Inbox.ck
	id := ""
	if e, ok := m.cockpitAgent(); ok && m.ShowInbox {
		if w := m.cockpitLiveWindow(e); w != nil {
			id = w.ID
		}
	}
	if id == ck.liveID {
		return
	}
	old := m.windowByID(ck.liveID)
	ck.liveID, ck.live, ck.liveDirty = id, "", true
	if old != nil && m.IsDaemonSession && old.DaemonMode && old.Workspace != m.CurrentWorkspace {
		m.unsubscribeFromPTY(old)
	}
	if w := m.windowByID(id); w != nil && m.IsDaemonSession && w.DaemonMode && w.PTYID != "" && !m.SubscribedPTYs[w.PTYID] {
		m.primePaneFromDaemon(w)
	}
	m.cockpitTouch()
}

// cockpitKeepsStream reports whether w's stream stays open because it is the
// live pane of the open agents view.
func (m *OS) cockpitKeepsStream(w *terminal.Window) bool {
	return w != nil && m.ShowInbox && m.Inbox.ck.liveID != "" && w.ID == m.Inbox.ck.liveID
}

// cockpitNoteOutput marks the live pane as having new output, so the next
// frame reads its cells again.
func (m *OS) cockpitNoteOutput(id string) bool {
	if !m.ShowInbox || id != m.Inbox.ck.liveID || id == "" {
		return false
	}
	m.Inbox.ck.liveDirty = true
	m.cockpitTouch()
	return true
}

// cockpitLiveBody is the live pane's last rows that hold text, cols by rows,
// read again only after output or a change of size.
func (m *OS) cockpitLiveBody(e sidebarAgentEntry, cols, rows int) (string, bool) {
	w := m.cockpitLiveWindow(e)
	ck := &m.Inbox.ck
	if w == nil || w.ID != ck.liveID {
		return "", false
	}
	if ck.liveDirty || ck.live == "" || ck.liveCols != cols || ck.liveRows != rows {
		if !w.TryRLockIO() {
			return ck.live, true
		}
		ck.live = pipCells(w.Terminal, cols, rows)
		w.RUnlockIO()
		ck.liveCols, ck.liveRows, ck.liveDirty = cols, rows, false
	}
	return ck.live, true
}

// drawLiveBox draws the live pane in a frame, at x, y, w by h cells.
func (m *OS) drawLiveBox(c *ckCanvas, l ckLook, e sidebarAgentEntry, x, y, w, h int) {
	pal := l.pal
	edge := l.edgePanel
	c.fill(x, y, w, h, l.canvas)
	c.text(x, y, 0, ckSeg{t: "╭" + strings.Repeat("─", w-2) + "╮", fg: edge})
	for j := y + 1; j < y+h-1; j++ {
		c.text(x, j, 0, ckSeg{t: "│", fg: edge})
		c.text(x+w-1, j, 0, ckSeg{t: "│", fg: edge})
	}
	c.text(x, y+h-1, 0, ckSeg{t: "╰" + strings.Repeat("─", w-2) + "╯", fg: edge})
	if l.ascii {
		c.text(x, y, 0, ckSeg{t: "+" + strings.Repeat("-", w-2) + "+", fg: edge})
		c.text(x, y+h-1, 0, ckSeg{t: "+" + strings.Repeat("-", w-2) + "+", fg: edge})
	}
	name, _, place, _ := m.ckRowFacts(ckRow{agent: 0}, e)
	c.text(x+2, y, x+w-2, ckSeg{t: " " + name + sepWord() + place + " ", fg: pal.FgDim})
	body, ok := m.cockpitLiveBody(e, w-4, h-2)
	if !ok {
		msg := "This pane is in another session. ctrl+] opens it."
		if e.Host != "" {
			msg = "This pane is on " + printableTitle(e.Host) + ". ctrl+] opens it there."
		}
		for i, s := range wrapPlain(msg, w-6) {
			c.text(x+3, y+2+i, x+w-2, ckSeg{t: s, fg: pal.FgMute})
		}
		return
	}
	for i, s := range strings.Split(body, "\n") {
		c.draw(x+2, y+1+i, w-4, s)
	}
}

// ckTurnFile is one file the last turn changed.
type ckTurnFile struct {
	path     string
	add, del int
}

// cockpitLastTurn is what the agent's last turn changed, from its thread:
// each file once, with the lines it added and removed.
func (m *OS) cockpitLastTurn(e sidebarAgentEntry) []ckTurnFile {
	t := m.Inbox.threads.byKey[agentKey(e)]
	if t == nil {
		return nil
	}
	var out []ckTurnFile
	at := map[string]int{}
	for i := len(t.entries) - 1; i >= 0; i-- {
		en := t.entries[i]
		if en.Kind == "text" && en.Role == "user" {
			break
		}
		if en.Kind != "tool_call" || en.Diff == nil {
			continue
		}
		p := ckShortPath(printableTitle(en.Diff.File))
		j, ok := at[p]
		if !ok {
			j = len(out)
			at[p] = j
			out = append(out, ckTurnFile{path: p})
		}
		out[j].add += en.Diff.Added
		out[j].del += en.Diff.Removed
	}
	return out
}

// drawSideSection draws a section's heading: its name, a hairline, and words
// on the right.
func drawSideSection(c *ckCanvas, l ckLook, x, y, w int, title, right string) {
	end := c.text(x, y, x+w, ckSeg{t: title, fg: l.pal.FgDim, attrs: ckBold})
	r := x + w
	if right != "" {
		r = c.textR(x+w, y, ckSeg{t: right, fg: l.pal.FgMute}) - 2
	}
	c.hline(end+2, y, r-end-2, hzGlyph(), l.edgePanel)
}

// drawLastTurn draws the files of the last turn from row y, at most rows of
// them, and returns the row under them.
func (m *OS) drawLastTurn(c *ckCanvas, l ckLook, e sidebarAgentEntry, x, y, w, rows int) int {
	pal := l.pal
	files := m.cockpitLastTurn(e)
	n := strconv.Itoa(len(files)) + " file"
	if len(files) != 1 {
		n += "s"
	}
	drawSideSection(c, l, x, y, w, "Last turn", n)
	y++
	if len(files) == 0 {
		c.text(x, y, x+w, ckSeg{t: "No edits in the last turn.", fg: pal.FgMute})
		return y + 1
	}
	for i, f := range files {
		if i == rows {
			c.text(x, y, x+w, ckSeg{t: "+" + strconv.Itoa(len(files)-i) + " more", fg: pal.FgMute})
			return y + 1
		}
		total := f.add + f.del
		cells := min(5, max(1, total/12))
		ac := (cells*f.add + total/2) / max(1, total)
		bar := []ckSeg{{t: strings.Repeat("■", ac), fg: l.add}, {t: strings.Repeat("■", cells-ac), fg: l.del}, {t: strings.Repeat("■", 5-cells), fg: l.edgePanel}}
		if l.ascii {
			bar = []ckSeg{{t: strings.Repeat("+", ac), fg: l.add}, {t: strings.Repeat("-", cells-ac), fg: l.del}, {t: strings.Repeat(".", 5-cells), fg: l.edgePanel}}
		}
		r := c.textR(x+w, y, bar...)
		counts := ckDiffCounts(l, f.add, f.del)
		r = c.textR(r-1, y, counts[:len(counts)-1]...)
		name := f.path
		if room := r - 2 - x; ansi.StringWidth(name) > room {
			name = overlay.Ellipsis() + ansi.TruncateLeft(name, ansi.StringWidth(name)-room+1, "")
		}
		c.text(x, y, r-1, ckSeg{t: name, fg: pal.FgDim})
		y++
	}
	return y
}

// drawCheckpoints draws the agent's checkpoints, newest first, as a timeline
// from row y, and returns the row under them.
func (m *OS) drawCheckpoints(c *ckCanvas, l ckLook, e sidebarAgentEntry, x, y, w, shown int) int {
	pal := l.pal
	cp := m.Inbox.ck.cps[agentKey(e)]
	count := ""
	if cp != nil && len(cp.list) > 0 {
		count = strconv.Itoa(len(cp.list))
	}
	drawSideSection(c, l, x, y, w, "Checkpoints", count)
	y++
	switch {
	case cp == nil || (cp.loading && cp.list == nil):
		c.text(x, y, x+w, ckSeg{t: "Reading...", fg: pal.FgMute})
		return y + 1
	case cp.err != "":
		for _, s := range wrapPlain(cp.err, w) {
			c.text(x, y, x+w, ckSeg{t: s, fg: pal.FgMute})
			y++
		}
		return y
	case len(cp.list) == 0:
		c.text(x, y, x+w, ckSeg{t: "No checkpoints yet. One is taken at each turn's end.", fg: pal.FgMute})
		return y + 1
	}
	n := 0
	for i := len(cp.list) - 1; i >= 0 && n < shown; i-- {
		k := cp.list[i]
		g, gInk, ink := l.g(ckGlyphIdle), pal.FgMute, pal.FgDim
		if n == 0 {
			g, gInk, ink = l.g(ckGlyphNeeds), pal.AccentBright, pal.Fg
		}
		label := printableTitle(k.Label)
		if label == "" {
			label = "turn " + strconv.Itoa(k.N)
		}
		if k.Kind == "safety" {
			label = "before a restore"
		}
		clock := time.Unix(0, k.At).Format("15:04")
		c.text(x, y, x+w, ckSeg{t: g, fg: gInk}, ckSeg{t: "  " + clock, fg: pal.FgMute}, ckSeg{t: "   " + label, fg: ink})
		y++
		n++
		if n < shown && i > 0 {
			c.text(x, y, 0, ckSeg{t: vtGlyph(), fg: l.edgePanel})
			y++
		}
	}
	return y
}

// drawSide draws the side column at x, from row y0 to y1.
func (m *OS) drawSide(c *ckCanvas, l ckLook, e sidebarAgentEntry, x, y0, w, y1 int) {
	c.fill(x, y0, w, y1-y0, l.panel)
	if l.pal.Depth == overlay.Depth16 {
		// No grounds at 16 colours: a hairline parts the columns.
		for y := y0; y < y1; y++ {
			c.text(x-1, y, 0, ckSeg{t: vtGlyph(), fg: l.edge})
		}
	}
	ix, iw := x+2, w-4
	y := y0 + 1
	drawSideSection(c, l, ix, y, iw, "Live", "ctrl+] open pane")
	y++
	liveH := min(ckLiveRows+2, max(y1-y-14, 6))
	m.drawLiveBox(c, l, e, ix, y, iw, liveH)
	y += liveH + 1
	y = m.drawLastTurn(c, l, e, ix, y, iw, 4) + 1
	if y < y1-3 {
		m.drawCheckpoints(c, l, e, ix, y, iw, min(ckCheckpointsShown, (y1-y-1)/2))
	}
}

// CockpitSideFetch reads the selected agent's checkpoints when it has none
// read for its current state, and follows the live pane. The input path
// calls it after every input while the agents view is up.
func (m *OS) CockpitSideFetch() tea.Cmd {
	if !m.ShowInbox {
		return nil
	}
	m.cockpitLiveFollow()
	e, ok := m.cockpitAgent()
	if !ok || e.Host != "" || (m.DaemonClient == nil && m.Inbox.call == nil) {
		return nil
	}
	ck := &m.Inbox.ck
	akey := agentKey(e)
	key := strconv.FormatInt(e.StateAt, 10) + e.State
	if cp := ck.cps[akey]; cp != nil && (cp.key == key || cp.loading) {
		return nil
	}
	if ck.cps == nil {
		ck.cps = map[string]*ckCheckpoints{}
	}
	if len(ck.cps) > 16 {
		clear(ck.cps)
	}
	prev := ck.cps[akey]
	cp := &ckCheckpoints{key: key, loading: true}
	if prev != nil {
		cp.list = prev.list
	}
	ck.cps[akey] = cp
	call, sess, win := m.inboxCaller(), e.SessionID, e.WindowID
	return func() tea.Msg {
		raw, err := call("list-checkpoints", map[string]any{"session": sess, "window": win}, threadReadTimeout)
		msg := ckCheckpointsMsg{agent: akey, key: key, err: err}
		if err == nil {
			var res struct {
				Checkpoints []ckCheckpoint `json:"checkpoints"`
			}
			msg.err = json.Unmarshal(raw, &res)
			msg.list = res.Checkpoints
		}
		return msg
	}
}

// applyCheckpoints takes a list-checkpoints reply.
func (m *OS) applyCheckpoints(msg ckCheckpointsMsg) {
	cp := m.Inbox.ck.cps[msg.agent]
	if cp == nil || cp.key != msg.key {
		return
	}
	cp.loading = false
	cp.list, cp.err = msg.list, ""
	if msg.err != nil {
		cp.list = nil
		cp.err = "Checkpoints need the pane in a git work tree."
	}
	m.cockpitTouch()
}
