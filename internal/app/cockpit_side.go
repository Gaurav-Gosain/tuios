package app

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const ckSideWidth = 46

const ckLiveRows = 14

const ckCheckpointsShown = 5

type ckCheckpoint struct {
	N     int    `json:"n"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	State string `json:"state"`
	At    int64  `json:"at"`
}

type ckCheckpoints struct {
	key     string
	list    []ckCheckpoint
	err     string
	loading bool
	off     bool
}

type ckCheckpointsMsg struct {
	agent, key string
	list       []ckCheckpoint
	off        bool
	err        error
}

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

const ckLivePrimeAfter = 150 * time.Millisecond

type ckLivePrimeMsg struct {
	id  string
	gen uint64
}

func (m *OS) cockpitLiveVisible() bool {
	return m.ShowInbox && (m.GetRenderWidth() >= ckWide || m.cockpitTab() == "Pane")
}

func (m *OS) cockpitLiveFollow() tea.Cmd {
	ck := &m.Inbox.ck
	id := ""
	if m.cockpitLiveVisible() {
		if e, ok := m.cockpitAgent(); ok {
			if w := m.cockpitLiveWindow(e); w != nil {
				id = w.ID
			}
		}
	}
	if id == ck.liveID {
		return nil
	}
	m.cockpitLiveRelease()
	ck.liveID, ck.live, ck.liveDirty = id, "", true
	ck.liveGen++
	w := m.windowByID(id)
	if w == nil || !m.IsDaemonSession || !w.DaemonMode || w.PTYID == "" || m.SubscribedPTYs[w.PTYID] {
		return nil
	}
	ck.livePrime++
	ck.primeID = id
	p := m.frameRate.program
	if p == nil {
		msg := ckLivePrimeMsg{id: id, gen: ck.livePrime}
		return tea.Tick(ckLivePrimeAfter, func(time.Time) tea.Msg { return msg })
	}
	if ck.primeTimer == nil {
		ck.primeTimer = time.AfterFunc(ckLivePrimeAfter, func() { p.Send(ckLivePrimeMsg{}) })
	} else {
		ck.primeTimer.Reset(ckLivePrimeAfter)
	}
	return nil
}

func (m *OS) applyLivePrime(msg ckLivePrimeMsg) {
	ck := &m.Inbox.ck
	id := msg.id
	if msg.gen == 0 {
		id = ck.primeID
	} else if msg.gen != ck.livePrime {
		return
	}
	if id == "" || id != ck.liveID || !m.ShowInbox {
		return
	}
	w := m.windowByID(id)
	if w == nil || w.PTYID == "" || m.SubscribedPTYs[w.PTYID] {
		return
	}
	m.primePaneFromDaemon(w)
	ck.liveOwned = m.SubscribedPTYs[w.PTYID]
	ck.liveDirty = true
	ck.liveGen++
}

func (m *OS) cockpitLiveRelease() {
	ck := &m.Inbox.ck
	old := m.windowByID(ck.liveID)
	owned := ck.liveOwned
	ck.liveID, ck.liveOwned = "", false
	if owned && old != nil && m.IsDaemonSession && old.DaemonMode && old.Workspace != m.CurrentWorkspace {
		m.unsubscribeFromPTY(old)
	}
}

func (m *OS) cockpitLiveForget() {
	ck := &m.Inbox.ck
	ck.liveID, ck.liveOwned, ck.live, ck.primeID = "", false, "", ""
	ck.liveDirty = true
	ck.livePrime++
	ck.liveGen++
}

func (m *OS) cockpitKeepsStream(w *terminal.Window) bool {
	return w != nil && m.ShowInbox && m.Inbox.ck.liveID != "" && w.ID == m.Inbox.ck.liveID
}

func (m *OS) cockpitNoteOutput(w *terminal.Window) bool {
	ck := &m.Inbox.ck
	if !m.ShowInbox || w == nil || w.ID != ck.liveID || ck.liveID == "" {
		return false
	}
	ck.liveDirty = true
	if !ck.liveShown {
		return false
	}
	ck.liveTick++
	if ck.liveTick%3 != 0 {
		w.HasNewOutput.Store(true)
		return false
	}
	ck.liveGen++
	return true
}

func (m *OS) cockpitLiveBody(e sidebarAgentEntry, cols, rows int) (string, bool) {
	w := m.cockpitLiveWindow(e)
	ck := &m.Inbox.ck
	if w == nil || w.ID != ck.liveID {
		return "", false
	}
	if ck.liveDirty || ck.live == "" || ck.liveCols != cols || ck.liveRows != rows {
		if !w.TryRLockIO() {
			w.HasNewOutput.Store(true)
			return ck.live, true
		}
		ck.live = pipCells(w.Terminal, cols, rows)
		w.RUnlockIO()
		ck.liveCols, ck.liveRows, ck.liveDirty = cols, rows, false
	}
	return ck.live, true
}

func (m *OS) drawLiveBox(c *ckCanvas, l ckLook, e sidebarAgentEntry, x, y, w, h int) {
	if w < 6 || h < 3 {
		return
	}
	pal := l.pal
	m.Inbox.ck.liveShown = true
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
		key := m.inboxKeyOr(config.ActionInboxViewPane, "ctrl+]")
		var msg string
		switch {
		case e.Host != "":
			msg = "This pane is on " + printableTitle(e.Host) + ". " + key + " opens it there."
		case e.WindowID == "":
			msg = "This agent has no pane tuios can show."
		case e.Foreign:
			msg = "This pane is in another session. " + key + " opens it."
		case m.windowByID(e.WindowID) == nil:
			msg = "The pane is closed."
		default:
			msg = "Reading the pane..."
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

type ckTurnFile struct {
	path     string
	add, del int
}

func (m *OS) cockpitLastTurn(e sidebarAgentEntry) []ckTurnFile {
	t := m.Inbox.threads.byKey[agentKey(e)]
	if t == nil {
		return nil
	}
	var out []ckTurnFile
	at := map[string]int{}
	root := m.ckAgentRoot(e)
	for _, en := range slices.Backward(t.entries) {
		if en.Kind == "text" && en.Role == "user" {
			break
		}
		if en.Kind != "tool_call" || en.Diff == nil {
			continue
		}
		if !ckInRoot(root, en.Diff.File) {
			continue
		}
		p := ckRelPath(root, printableTitle(en.Diff.File))
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

func drawSideSection(c *ckCanvas, l ckLook, x, y, w int, title, right string) {
	end := c.text(x, y, x+w, ckSeg{t: title, fg: l.pal.FgDim, attrs: ckBold})
	r := x + w
	if right != "" {
		r = c.textR(x+w, y, ckSeg{t: right, fg: l.pal.FgMute}) - 2
	}
	c.hline(end+2, y, r-end-2, hzGlyph(), l.edgePanel)
}

func (m *OS) drawLastTurn(c *ckCanvas, l ckLook, e sidebarAgentEntry, x, y, w, rows int) int {
	pal := l.pal
	files := m.cockpitLastTurn(e)
	n := strconv.Itoa(len(files)) + " file"
	if len(files) != 1 {
		n += "s"
	}
	if !m.threadReadsTranscript(e) {
		n = ""
	}
	drawSideSection(c, l, x, y, w, "Last turn", n)
	y++
	if len(files) == 0 {
		words := "No edits in the last turn."
		if !m.threadReadsTranscript(e) {
			words = "tuios cannot read the edits of this agent yet."
		}
		for _, s := range wrapPlain(words, w) {
			c.text(x, y, x+w, ckSeg{t: s, fg: pal.FgMute})
			y++
		}
		return y
	}
	for i, f := range files {
		if i == rows {
			c.text(x, y, x+w, ckSeg{t: "+" + strconv.Itoa(len(files)-i) + " more", fg: pal.FgMute})
			return y + 1
		}
		drawFileStat(c, l, x, y, w, f.path, f.add, f.del)
		y++
	}
	return y
}

func drawFileStat(c *ckCanvas, l ckLook, x, y, w int, path string, add, del int) {
	total := add + del
	cells := min(5, max(1, total/12))
	ac := (cells*add + total/2) / max(1, total)
	bar := []ckSeg{{t: strings.Repeat("━", ac), fg: l.add}, {t: strings.Repeat("━", cells-ac), fg: l.del}, {t: strings.Repeat("━", 5-cells), fg: l.edgePanel}}
	if l.ascii {
		bar = []ckSeg{{t: strings.Repeat("+", ac), fg: l.add}, {t: strings.Repeat("-", cells-ac), fg: l.del}, {t: strings.Repeat(".", 5-cells), fg: l.edgePanel}}
	}
	r := c.textR(x+w, y, bar...)
	counts := ckDiffCounts(l, add, del)
	r = c.textR(r-1, y, counts[:len(counts)-1]...)
	name := path
	if room := r - 2 - x; ansi.StringWidth(name) > room {
		name = overlay.Ellipsis() + ansi.TruncateLeft(name, ansi.StringWidth(name)-room+1, "")
	}
	c.text(x, y, r-1, ckSeg{t: name, fg: l.pal.FgDim})
}

func (m *OS) drawCheckpoints(c *ckCanvas, l ckLook, e sidebarAgentEntry, x, y, w, shown int) int {
	pal := l.pal
	m.cockpitNoteCheckpoints(e)
	cp := m.Inbox.ck.cps[agentKey(e)]
	count := ""
	if cp != nil && len(cp.list) > 0 {
		count = strconv.Itoa(len(cp.list))
	}
	drawSideSection(c, l, x, y, w, "Checkpoints", count)
	y++
	switch {
	case e.Host != "":
		for _, s := range wrapPlain("The checkpoints of an agent on "+printableTitle(e.Host)+" are kept there. Attach to it to see them.", w) {
			c.text(x, y, x+w, ckSeg{t: s, fg: pal.FgMute})
			y++
		}
		return y
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
		words := "No checkpoints yet. One is taken at the end of a turn that changes a file."
		if cp.off {
			words = "Checkpoints are off. [agents.checkpoints] enabled turns them on."
		}
		for _, s := range wrapPlain(words, w) {
			c.text(x, y, x+w, ckSeg{t: s, fg: pal.FgMute})
			y++
		}
		return y
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

func (m *OS) drawSide(c *ckCanvas, l ckLook, e sidebarAgentEntry, x, y0, w, y1 int) {
	c.fill(x, y0, w, y1-y0, l.panel)
	if l.pal.Depth == overlay.Depth16 {
		for y := y0; y < y1; y++ {
			c.text(x-1, y, 0, ckSeg{t: vtGlyph(), fg: l.edge})
		}
	}
	ix, iw := x+2, w-4
	y := y0 + 1
	drawSideSection(c, l, ix, y, iw, "Live", m.inboxKeyOr(config.ActionInboxViewPane, "ctrl+]")+" open pane")
	y++
	liveH := min(ckLiveRows+2, max(y1-y-14, 6))
	m.drawLiveBox(c, l, e, ix, y, iw, liveH)
	y += liveH + 1
	y = m.drawLastTurn(c, l, e, ix, y, iw, 4) + 1
	if y < y1-3 {
		m.drawCheckpoints(c, l, e, ix, y, iw, min(ckCheckpointsShown, (y1-y-1)/2))
	}
}

// CockpitSideFetch follows the live pane, and reads the selected agent's
// checkpoints when they are on screen and none are read for its current
// state. The input path calls it after every input while the agents view is
// up, and Update after a message that left a frame wanting them.
func (m *OS) CockpitSideFetch() tea.Cmd {
	if !m.ShowInbox {
		return nil
	}
	ck := &m.Inbox.ck
	ck.cpWant = false
	follow := tea.Batch(m.cockpitLiveFollow(), m.cockpitCardFetch())
	if m.GetRenderWidth() < ckWide && m.cockpitTab() != "Checkpoints" {
		return follow
	}
	e, ok := m.cockpitAgent()
	if !ok || e.Host != "" || (m.DaemonClient == nil && m.Inbox.call == nil) {
		return follow
	}
	akey := agentKey(e)
	key := strconv.FormatInt(e.StateAt, 10) + e.State
	if cp := ck.cps[akey]; cp != nil && (cp.key == key || cp.loading) {
		return follow
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
		cp.list, cp.off = prev.list, prev.off
	}
	ck.cps[akey] = cp
	call, sess, win := m.agentCaller(), e.SessionID, e.WindowID
	return tea.Batch(follow, func() tea.Msg {
		raw, err := call("list-checkpoints", map[string]any{"session": sess, "window": win}, threadReadTimeout)
		msg := ckCheckpointsMsg{agent: akey, key: key, err: err}
		if err == nil {
			var res struct {
				Enabled     *bool          `json:"enabled"`
				Checkpoints []ckCheckpoint `json:"checkpoints"`
			}
			msg.err = jsonx.Unmarshal(raw, &res)
			msg.list = res.Checkpoints
			msg.off = res.Enabled != nil && !*res.Enabled
		}
		return msg
	})
}

func (m *OS) cockpitNoteCheckpoints(e sidebarAgentEntry) {
	ck := &m.Inbox.ck
	key := strconv.FormatInt(e.StateAt, 10) + e.State
	if cp := ck.cps[agentKey(e)]; cp == nil || (cp.key != key && !cp.loading) {
		ck.cpWant = true
	}
}

// CockpitSideWanted is CockpitSideFetch when a frame found the checkpoints
// on screen stale, nil otherwise. Update asks it after every message.
func (m *OS) CockpitSideWanted() tea.Cmd {
	if pv := &m.Inbox.ck.kb.pv; pv.picWant {
		pv.picWant = false
		if cmd := m.kbPreviewPictureCmd(); cmd != nil {
			return tea.Batch(cmd, m.cockpitSideWantedRest())
		}
	}
	if m.Inbox.ck.kb.pv.fileWant {
		if cmd := m.kbPreviewFileFetch(); cmd != nil {
			return tea.Batch(cmd, m.cockpitSideWantedRest())
		}
	}
	return m.cockpitSideWantedRest()
}

func (m *OS) cockpitSideWantedRest() tea.Cmd {
	if !m.Inbox.ck.cpWant && !m.Inbox.ck.cardWant {
		return nil
	}
	return m.CockpitSideFetch()
}

func (m *OS) applyCheckpoints(msg ckCheckpointsMsg) {
	cp := m.Inbox.ck.cps[msg.agent]
	if cp == nil || cp.key != msg.key {
		return
	}
	cp.loading = false
	cp.list, cp.err, cp.off = msg.list, "", msg.off
	if msg.err != nil {
		cp.list = nil
		verr, ok := errors.AsType[*session.VerbCallError](msg.err)
		switch {
		case ok && verr.Code == session.ErrVerbNotRepo:
			cp.err = "Checkpoints need the pane in a git work tree."
		case ok && verr.Code == session.ErrVerbUnknownVerb:
			cp.err = "This daemon takes no checkpoints. " + m.restartHint()
		default:
			cp.err = "tuios cannot read the checkpoints now: " + printableTitle(msg.err.Error())
		}
	}
	m.cockpitTouch()
}
