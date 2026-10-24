package app

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/loom"
	"tuios.dev/tml"
)

// The Artifacts tab of the agents view: what the selected agent has shown
// the person, the TML surfaces its pane opened and the screenshots it took.
// The daemon keeps the list per pane (artifact-list); this client adds the
// surfaces it holds that the last list did not have yet.
//
// A surface has one state and two places. Outside the agents view it is the
// window the agent asked for, a popup or a pane. While the view is open the
// window is under it, and the surface is drawn in the view's focus area
// instead, from the same tmlView: the same loom surface, the same state, the
// same text typed into a form. Nothing is copied, so nothing is lost when
// the person goes from one place to the other, and the surface never shows
// in two places at once.
//
// While the view is open:
//   - a surface from the selected agent shows in its focus area, selected;
//   - a surface from another agent puts a "new artifact" chip on that
//     agent's row, and a popup moves the row to Needs you, so a form that
//     waits for the person is never out of sight;
//   - opening the view while an agent's popup floats selects that agent and
//     the surface; closing it while the person was in a surface that still
//     waits focuses its window, so it floats again with what was typed.
//
// Cost: nothing runs on a timer. The list is read once per pane and again
// when a surface opens or closes, or the agent's state moves.

// ckArtifact is one artifact as the view lists it.
type ckArtifact struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Title   string          `json:"title"`
	At      int64           `json:"at"`
	Surface string          `json:"surface"`
	Target  string          `json:"target"`
	Open    bool            `json:"open"`
	Reason  string          `json:"reason"`
	Value   json.RawMessage `json:"value"`
	Doc     json.RawMessage `json:"doc"`
	State   json.RawMessage `json:"state"`
	Path    string          `json:"path"`
	Format  string          `json:"format"`
}

// ckArtList is one pane's list as last read.
type ckArtList struct {
	items   []ckArtifact
	key     string
	loading bool
	err     string
}

// ckArtBox is where the selected artifact was last drawn, on the screen.
type ckArtBox struct {
	x, y, w, h int
	id         string
}

// ckArtState is the Artifacts tab's own state.
type ckArtState struct {
	lists map[string]*ckArtList
	// sel is the selected artifact's id, and focus says the keys go to it.
	sel   string
	focus bool
	// seen holds the surfaces the person has had in front of them in the
	// view; an open surface not in it is new.
	seen map[string]bool
	// snaps are the surfaces drawn from a record: a closed surface, or one
	// in a session this client does not hold.
	snaps map[string]*ckArtSnap
	box   ckArtBox
	gen   uint64
}

// ckArtSnap is a record drawn read-only.
type ckArtSnap struct {
	key string
	s   *loom.Surface
	pal string
}

// ckArtifactsMsg is an artifact-list reply.
type ckArtifactsMsg struct {
	window, key string
	items       []ckArtifact
	err         error
}

// ckArtTab is the focus tab's name.
const ckArtTab = "Artifacts"

// ckArtKey is what a pane's list was read for: the client's surfaces and the
// agent's state, either of which moving can change it.
func (m *OS) ckArtKey(e sidebarAgentEntry) string {
	return ckItoa(int64(m.tml.setGen)) + ":" + ckItoa(e.StateAt) + e.State
}

func ckItoa(n int64) string { return strconv.FormatInt(n, 10) }

// CockpitArtifactsWanted reads the selected agent's list when the view has
// none or an old one. Update asks it after every message: a few compares
// when nothing changed.
func (m *OS) CockpitArtifactsWanted() tea.Cmd {
	if !m.ShowInbox || (m.DaemonClient == nil && m.Inbox.call == nil) {
		return nil
	}
	e, ok := m.cockpitAgent()
	if !ok || e.Host != "" || e.WindowID == "" {
		return nil
	}
	art := &m.Inbox.ck.art
	key := m.ckArtKey(e)
	l := art.lists[e.WindowID]
	if l != nil && (l.key == key || l.loading) {
		return nil
	}
	if art.lists == nil {
		art.lists = map[string]*ckArtList{}
	}
	if len(art.lists) > 32 {
		clear(art.lists)
	}
	next := &ckArtList{key: key, loading: true}
	if l != nil {
		next.items = l.items
	}
	art.lists[e.WindowID] = next
	call, win := m.inboxCaller(), e.WindowID
	return func() tea.Msg {
		raw, err := call("artifact-list", map[string]any{"window": win}, threadReadTimeout)
		msg := ckArtifactsMsg{window: win, key: key, err: err}
		if err == nil {
			var res struct {
				Artifacts []ckArtifact `json:"artifacts"`
			}
			msg.err = json.Unmarshal(raw, &res)
			msg.items = res.Artifacts
		}
		return msg
	}
}

// applyCockpitArtifacts takes an artifact-list reply.
func (m *OS) applyCockpitArtifacts(msg ckArtifactsMsg) {
	l := m.Inbox.ck.art.lists[msg.window]
	if l == nil || l.key != msg.key {
		return
	}
	l.loading = false
	l.err = ""
	if msg.err != nil {
		if verr, ok := errors.AsType[*session.VerbCallError](msg.err); ok && verr.Code == session.ErrVerbUnknownVerb {
			l.err = "This daemon keeps no artifacts. " + m.restartHint()
		} else {
			l.err = "tuios cannot read the artifacts now: " + printableTitle(msg.err.Error())
		}
	} else {
		l.items = msg.items
	}
	m.Inbox.ck.art.gen++
	m.cockpitTouch()
}

// ckArtifacts is what the agent has shown, newest first: the daemon's list,
// with the surfaces this client holds that the list does not have yet.
func (m *OS) ckArtifacts(e sidebarAgentEntry) []ckArtifact {
	if e.Host != "" || e.WindowID == "" {
		return nil
	}
	var listed []ckArtifact
	if l := m.Inbox.ck.art.lists[e.WindowID]; l != nil {
		listed = l.items
	}
	var fresh []ckArtifact
	for _, v := range m.tml.views {
		if v.caller != e.WindowID || v.surface == nil {
			continue
		}
		if slices.ContainsFunc(listed, func(a ckArtifact) bool { return a.ID == v.id }) {
			continue
		}
		fresh = append(fresh, ckArtifact{ID: v.id, Kind: session.ArtifactTML, Title: v.title, At: v.at.UnixNano(), Surface: v.id, Open: true})
	}
	slices.SortFunc(fresh, func(a, b ckArtifact) int { return int(b.At - a.At) })
	out := append(fresh, listed...)
	for i := range out {
		// A surface this client holds is open, whatever the list read.
		if out[i].Kind == session.ArtifactTML && m.tml.views[out[i].ID] != nil {
			out[i].Open = true
		}
	}
	return out
}

// ckArtWindow is the window that draws a surface on this client, or nil.
func (m *OS) ckArtWindow(id string) *terminal.Window {
	for _, w := range m.Windows {
		if w != nil && w.Kind == session.WindowKindTML && session.TMLSurfaceID(w.URI) == id {
			return w
		}
	}
	return nil
}

// ckArtifactNew reports whether the agent has an open surface the person
// has not had in front of them in the view. waiting narrows it to a popup,
// which is a question more often than a dashboard is.
func (m *OS) ckArtifactNew(e sidebarAgentEntry, waiting bool) bool {
	if e.Host != "" || e.WindowID == "" {
		return false
	}
	for _, v := range m.tml.views {
		if v.caller != e.WindowID || v.surface == nil || m.Inbox.ck.art.seen[v.id] {
			continue
		}
		if !waiting {
			return true
		}
		if w := m.ckArtWindow(v.id); w != nil && w.IsPopup {
			return true
		}
	}
	return false
}

// ckArtFormRow is the title of the popup that put the row in Needs you,
// "" for any other row: an agent row with no item, in Needs you, whose agent
// does not need input itself and has a TML popup open. The row's mark, its
// second line and the focus's badge say so, so the row never reads as
// working in a group that says it needs you.
func (m *OS) ckArtFormRow(r ckRow, e sidebarAgentEntry) string {
	if r.agent < 0 || r.hasItem || r.group != ckNeeds || e.State == "needs_input" || e.Host != "" || e.WindowID == "" {
		return ""
	}
	for _, v := range m.tml.views {
		if v.caller != e.WindowID || v.surface == nil {
			continue
		}
		if w := m.ckArtWindow(v.id); w != nil && w.IsPopup {
			return cmpTitle(ckArtifact{Title: v.title, Kind: "form"})
		}
	}
	return ""
}

// ckArtSelected is the place of the selected artifact in items: the one
// selected before, else the first surface that is new, else the first open
// one, else the newest.
func (m *OS) ckArtSelected(items []ckArtifact) int {
	art := &m.Inbox.ck.art
	if i := slices.IndexFunc(items, func(a ckArtifact) bool { return a.ID == art.sel }); i >= 0 {
		return i
	}
	if i := slices.IndexFunc(items, func(a ckArtifact) bool { return a.Open && !art.seen[a.ID] }); i >= 0 {
		return i
	}
	if i := slices.IndexFunc(items, func(a ckArtifact) bool { return a.Open }); i >= 0 {
		return i
	}
	if len(items) == 0 {
		return -1
	}
	return 0
}

// ckArtLive is the selected artifact's view when it is a surface this client
// holds, and its window.
func (m *OS) ckArtLive() (*tmlView, *terminal.Window) {
	e, ok := m.cockpitAgent()
	if !ok {
		return nil, nil
	}
	items := m.ckArtifacts(e)
	i := m.ckArtSelected(items)
	if i < 0 {
		return nil, nil
	}
	v := m.tml.views[items[i].ID]
	if v == nil || v.surface == nil || v.caller != e.WindowID {
		return nil, nil
	}
	return v, m.ckArtWindow(v.id)
}

// ckArtShow selects the artifact id on the Artifacts tab of the selected
// agent, and gives it the keys when focus is set and it is live.
func (m *OS) ckArtShow(id string, focus bool) {
	art := &m.Inbox.ck.art
	art.sel = id
	m.Inbox.ck.tab = ckArtTab
	if art.seen == nil {
		art.seen = map[string]bool{}
	}
	art.seen[id] = true
	if len(art.seen) > 256 {
		for k := range art.seen {
			if m.tml.views[k] == nil {
				delete(art.seen, k)
			}
		}
	}
	v := m.tml.views[id]
	art.focus = focus && v != nil && v.surface != nil
	art.gen++
	m.cockpitTouchRows()
}

// CockpitArtifactFocused reports whether the keys go to the surface drawn in
// the view. A surface that closed gives them back.
func (m *OS) CockpitArtifactFocused() bool {
	art := &m.Inbox.ck.art
	if !art.focus {
		return false
	}
	if v, _ := m.ckArtLive(); v == nil || m.cockpitTab() != ckArtTab {
		art.focus = false
		m.cockpitTouch()
		return false
	}
	return true
}

// CockpitArtifactOpen is o: the selected agent's artifact, on its tab, with
// the keys when it is a live surface.
func (m *OS) CockpitArtifactOpen() tea.Cmd {
	e, ok := m.cockpitAgent()
	if !ok {
		return nil
	}
	items := m.ckArtifacts(e)
	i := m.ckArtSelected(items)
	if i < 0 {
		m.ShowNotification("This agent has shown nothing yet.", "info", m.Settings.NotificationDuration)
		return nil
	}
	m.ckArtShow(items[i].ID, true)
	return nil
}

// ckArtMove moves the selection in the Artifacts tab by delta.
func (m *OS) ckArtMove(delta int) bool {
	e, ok := m.cockpitAgent()
	if !ok || m.cockpitTab() != ckArtTab {
		return false
	}
	items := m.ckArtifacts(e)
	i := m.ckArtSelected(items)
	if i < 0 {
		return true
	}
	i = min(max(i+delta, 0), len(items)-1)
	m.ckArtShow(items[i].ID, false)
	return true
}

// CockpitArtifactKey gives a key to the surface the view draws. esc that the
// surface does not use gives the keys back to the view: it never dismisses
// the surface, so a form keeps what the person typed.
func (m *OS) CockpitArtifactKey(key string) tea.Cmd {
	art := &m.Inbox.ck.art
	v, _ := m.ckArtLive()
	if v == nil {
		art.focus = false
		m.cockpitTouch()
		return nil
	}
	defer m.cockpitTouch()
	if v.confirm != nil {
		a := *v.confirm
		if (key == "y" || key == "n") && !tmlQuestionFits(len(m.tmlQuestion(a, art.box.w)), art.box.h) {
			return nil
		}
		switch key {
		case "y":
			v.confirm = nil
			if v.confirmed == nil {
				v.confirmed = map[string]bool{}
			}
			v.confirmed[m.tmlConfirmKey(a)] = true
			m.markTMLDirty(v.id)
			return m.tmlInput(v, tmlInputReq{actions: []tmlPaneAction{a}})
		case "n", "esc":
			v.confirm = nil
			m.markTMLDirty(v.id)
		}
		return nil
	}
	res, err := v.surface.Key(key)
	if err != nil {
		clientLog("TML surface %s: key %s: %v", v.id, key, err)
	}
	if key == "esc" && len(res.Changed) == 0 && !res.View && len(res.Effects) == 0 {
		art.focus = false
		return nil
	}
	cmd := m.tmlResult(nil, v, res)
	m.markTMLDirty(v.id)
	return cmd
}

// cockpitArtifactMouse gives a press or a wheel move in the drawn surface to
// it. handled is false outside the surface.
func (m *OS) cockpitArtifactMouse(x, y int, button loom.MouseButton) (tea.Cmd, bool) {
	art := &m.Inbox.ck.art
	b := art.box
	if b.id == "" || x < b.x || x >= b.x+b.w || y < b.y || y >= b.y+b.h || m.cockpitTab() != ckArtTab {
		return nil, false
	}
	lm := loom.Mouse{X: x - b.x, Y: y - b.y, Button: button}
	v, _ := m.ckArtLive()
	if v == nil || v.id != b.id {
		// A record: the wheel scrolls it, and nothing goes back.
		if sn := art.snaps[b.id]; sn != nil && sn.s != nil {
			_, _ = sn.s.Mouse(lm)
			m.cockpitTouch()
		}
		return nil, true
	}
	if v.confirm != nil {
		return nil, true
	}
	defer m.cockpitTouch()
	if button == loom.MouseLeft {
		art.focus = true
		res, _ := v.surface.Mouse(lm)
		lm.Action = loom.Release
		rel, _ := v.surface.Mouse(lm)
		res.Changed = append(res.Changed, rel.Changed...)
		res.View = res.View || rel.View
		res.Effects = append(res.Effects, rel.Effects...)
		cmd := m.tmlResult(nil, v, res)
		m.markTMLDirty(v.id)
		return cmd, true
	}
	res, _ := v.surface.Mouse(lm)
	cmd := m.tmlResult(nil, v, res)
	m.markTMLDirty(v.id)
	return cmd, true
}

// cockpitArtifactArrived shows a surface that just reached this client in
// the view, when the view is open on the agent that opened it. Any other
// agent's row gets the chip, as the rows are built again.
func (m *OS) cockpitArtifactArrived(v *tmlView) {
	if !m.ShowInbox || v == nil || v.caller == "" || v.surface == nil {
		return
	}
	m.cockpitTouchRows()
	e, ok := m.cockpitAgent()
	if !ok || e.Host != "" || e.WindowID != v.caller {
		return
	}
	// The reply editor keeps the keys: what the person types there is not
	// meant for the surface.
	m.ckArtShow(v.id, m.Inbox.reply.editor == nil && !m.CockpitArtifactFocused())
}

// cockpitAdoptFloating moves a TML popup that floats now into the view as it
// opens: its agent is selected and the surface shown, with the keys. The
// focused popup goes first, then the one on top.
func (m *OS) cockpitAdoptFloating() {
	var pick *tmlView
	consider := func(w *terminal.Window) {
		if pick != nil || w == nil || !w.IsPopup || w.Minimized || w.Workspace != m.CurrentWorkspace || !m.DrawsTML(w) {
			return
		}
		if v := m.tmlViewOf(w); v != nil && v.caller != "" && v.surface != nil {
			pick = v
		}
	}
	consider(m.GetFocusedWindow())
	for _, w := range slices.Backward(m.Windows) {
		consider(w)
	}
	if pick == nil {
		return
	}
	ck := &m.Inbox.ck
	for i, r := range m.cockpitRows() {
		if r.selectable() && r.agent >= 0 && ck.agents[r.agent].Host == "" && ck.agents[r.agent].WindowID == pick.caller {
			m.cockpitSetCursor(i)
			ck.follow = true
			m.cockpitSync()
			m.ckArtShow(pick.id, true)
			return
		}
	}
}

// cockpitArtifactRelease runs as the view closes: the surface the person had
// on screen in the Artifacts tab gets its window's focus and comes to the
// front, so it floats again with what they did in it.
func (m *OS) cockpitArtifactRelease() {
	art := &m.Inbox.ck.art
	inside := art.focus
	shown := inside || m.cockpitTab() == ckArtTab
	art.focus = false
	art.box = ckArtBox{}
	if !shown {
		return
	}
	v, w := m.ckArtLive()
	if v == nil || w == nil {
		return
	}
	if w.Workspace != m.CurrentWorkspace {
		m.SwitchToWorkspace(w.Workspace)
	}
	for i, win := range m.Windows {
		if win == w {
			if w.Minimized {
				m.RestoreWindow(i)
			}
			m.FocusWindow(i)
			break
		}
	}
	if inside {
		// The person was typing into the surface: the keys stay with it.
		m.EnterTerminalMode()
	}
	m.markTMLDirty(v.id)
}

// drawArtifacts draws the Artifacts tab: the list, then the selected
// artifact, from row y to y1.
func (m *OS) drawArtifacts(c *ckCanvas, l ckLook, e sidebarAgentEntry, x, y, w, y1, top int, now time.Time) {
	pal := l.pal
	art := &m.Inbox.ck.art
	art.box = ckArtBox{}
	items := m.ckArtifacts(e)
	if len(items) == 0 {
		words := "Nothing yet. A form, a table or a dashboard the agent shows with tuios tml show appears here."
		if lst := art.lists[e.WindowID]; lst != nil && lst.err != "" {
			words = lst.err
		} else if lst == nil || lst.loading {
			words = "Reading..."
		}
		for _, s := range wrapPlain(words, w) {
			c.text(x, y, x+w, ckSeg{t: s, fg: pal.FgMute})
			y++
		}
		return
	}
	sel := m.ckArtSelected(items)
	// The list takes at most a third of the room, and at least one row.
	shown := min(len(items), max((y1-y)/3, 1), 6)
	first := 0
	if sel >= shown {
		first = sel - shown + 1
	}
	for i := first; i < first+shown && i < len(items); i++ {
		a := items[i]
		cursor := i == sel
		ground := l.canvas
		if cursor {
			ground = l.cursor
		}
		c.fill(x, y, w, 1, ground)
		if cursor {
			c.text(x, y, 0, ckSeg{t: "▌", fg: pal.Accent})
		}
		g, gInk := l.g(ckGlyphReview), pal.FgMute
		status := "closed"
		switch {
		case a.Kind == session.ArtifactScreenshot:
			g, gInk, status = l.g(ckGlyphImage), pal.FgDim, "screenshot"
		case a.Open && !art.seen[a.ID]:
			g, gInk, status = l.g(ckGlyphNeeds), pal.Attention, "new"
		case a.Open:
			g, gInk, status = l.g(ckGlyphNeeds), pal.AccentBright, "open"
		case a.Reason == session.TMLClosedByPerson:
			status = "answered"
		case a.Reason == session.TMLClosedByProgram:
			status = "closed by the agent"
		case a.Reason == session.TMLDismissed:
			status = "dismissed"
		}
		right := x + w - 1
		age := agentMailAge(a.At, now)
		rx := c.textR(right, y, ckSeg{t: status, fg: overlay.Readable(pal.FgDim, ground)}, ckSeg{t: "  " + age, fg: overlay.Readable(pal.FgMute, ground)})
		titleInk := pal.FgDim
		if cursor {
			titleInk = pal.Fg
		}
		c.text(x+2, y, rx-2, ckSeg{t: g, fg: gInk}, ckSeg{t: "  " + ckTrunc(printableTitle(cmpTitle(a)), max(rx-x-7, 4)), fg: overlay.Readable(titleInk, ground), attrs: boolAttr(cursor)})
		c.mark(pal, x+1, y, w-1, 1, overlay.RowState{Cursor: cursor, Focused: cursor})
		m.Inbox.ck.hits = append(m.Inbox.ck.hits, ckHit{x0: x, y0: top + y, x1: x + w, y1: top + y + 1, kind: ckHitAction, action: "art:" + a.ID})
		y++
	}
	if len(items) > shown {
		c.text(x+2, y, x+w, ckSeg{t: "J K  " + ckItoa(int64(len(items))) + " in all", fg: pal.FgMute})
		y++
	}
	if sel < 0 || y1-y < 3 {
		return
	}
	a := items[sel]
	y++
	// The line above the artifact says what it is and what the keys do.
	var head []ckSeg
	v := m.tml.views[a.ID]
	live := v != nil && v.surface != nil && v.caller == e.WindowID
	switch {
	case a.Kind == session.ArtifactScreenshot:
		head = []ckSeg{{t: "Screenshot", fg: pal.FgDim, attrs: ckBold}}
	case live && art.focus:
		head = []ckSeg{{t: "You are in this surface.", fg: pal.AccentBright, attrs: ckBold}, {t: "  esc goes back to the agents", fg: pal.FgMute}}
	case live:
		head = []ckSeg{{t: "Open.", fg: pal.Fg, attrs: ckBold}, {t: "  " + m.inboxKeyOr(config.ActionInboxArtifact, "o") + " or a click to use it", fg: pal.FgMute}}
	case a.Open:
		head = []ckSeg{{t: "Open in another session.", fg: pal.FgDim, attrs: ckBold}, {t: "  Attach to it to use it.", fg: pal.FgMute}}
	case a.Reason == session.TMLClosedByPerson && len(a.Value) > 0:
		head = []ckSeg{{t: "You answered ", fg: pal.FgDim}, {t: ckTrunc(printableTitle(string(a.Value)), max(w-14, 4)), fg: pal.Fg, attrs: ckBold}}
	default:
		head = []ckSeg{{t: "Closed. This is what it showed last.", fg: pal.FgDim}}
	}
	c.text(x, y, x+w, head...)
	y += 2
	bh := y1 - y
	if bh < 2 {
		return
	}
	switch {
	case a.Kind == session.ArtifactScreenshot:
		for _, s := range wrapPlain(ckHomePath(a.Path), w) {
			c.text(x, y, x+w, ckSeg{t: s, fg: pal.Fg})
			y++
		}
		c.text(x, y, x+w, ckSeg{t: strings.ToUpper(a.Format), fg: pal.FgMute})
		return
	case live:
		art.box = ckArtBox{x: x, y: top + y, w: w, h: bh, id: a.ID}
		for i, s := range strings.Split(m.ckArtRenderLive(v, w, bh), "\n") {
			if i >= bh {
				break
			}
			c.draw(x, y+i, w, s)
		}
		if art.focus {
			for i := range bh {
				c.text(x-1, y+i, 0, ckSeg{t: "▎", fg: pal.Accent})
			}
		}
	default:
		s := m.ckArtSnap(a)
		if s == nil {
			c.text(x, y, x+w, ckSeg{t: "tuios cannot draw this document.", fg: pal.FgMute})
			return
		}
		art.box = ckArtBox{x: x, y: top + y, w: w, h: bh, id: a.ID}
		for i, line := range strings.Split(s.Render(w, bh).Render(), "\n") {
			if i >= bh {
				break
			}
			c.draw(x, y+i, w, line)
		}
	}
}

// cmpTitle is an artifact's title, or its kind when it has none.
func cmpTitle(a ckArtifact) string {
	if a.Title != "" {
		return a.Title
	}
	return a.Kind
}

// boolAttr is bold when on.
func boolAttr(on bool) uint8 {
	if on {
		return ckBold
	}
	return 0
}

// ckArtRenderLive draws a live surface at w by h for the view, with the
// question under it when the surface waits for a yes. It is renderTML's
// body without the window: the window draws the same surface at its own
// size when it shows again.
func (m *OS) ckArtRenderLive(v *tmlView, w, h int) string {
	if key := m.tmlPaletteKey(); key != v.palKey {
		v.surface.SetPalette(m.tmlPalette())
		v.palKey = key
	}
	sh := h
	confirm := ""
	if v.confirm != nil {
		if tmlQuestionFits(len(m.tmlQuestion(*v.confirm, w)), h) {
			confirm = m.tmlConfirmLine(v, w)
		} else {
			confirm = tmlTooSmall
		}
		sh = max(h-strings.Count(confirm, "\n")-1, 1)
	}
	out := v.surface.Render(w, sh).Render()
	// The window's last frame was at its own size: it draws again.
	v.content = ""
	if confirm != "" {
		out += "\n" + confirm
	}
	return out
}

// ckArtSnap is the read-only surface of a record, built again only when the
// record or the palette changed.
func (m *OS) ckArtSnap(a ckArtifact) *loom.Surface {
	if len(a.Doc) == 0 {
		return nil
	}
	art := &m.Inbox.ck.art
	key := a.ID + "\x00" + string(a.State) + "\x00" + a.Reason
	pal := m.tmlPaletteKey()
	if sn := art.snaps[a.ID]; sn != nil && sn.key == key {
		if sn.pal != pal {
			sn.s.SetPalette(m.tmlPalette())
			sn.pal = pal
		}
		return sn.s
	}
	lim := tml.PersonLimits()
	doc, err := tml.Parse(a.Doc, &tml.ParseOptions{Limits: &lim})
	if err != nil {
		return nil
	}
	if len(a.State) > 0 {
		doc.SetAttr("state", tml.Value{Kind: tml.KindObject, Raw: a.State})
	}
	s, err := loom.New(doc, &loom.Options{Palette: m.tmlPalette(), Ground: loom.GroundNone, Limits: &lim})
	if err != nil {
		return nil
	}
	if art.snaps == nil || len(art.snaps) > 32 {
		art.snaps = map[string]*ckArtSnap{}
	}
	art.snaps[a.ID] = &ckArtSnap{key: key, s: s, pal: pal}
	return s
}

// ckArtTabLabel is the tab's label for the agent, "" when it has shown
// nothing.
func (m *OS) ckArtTabLabel(e sidebarAgentEntry) string {
	n := len(m.ckArtifacts(e))
	if n == 0 {
		return ""
	}
	return ckArtTab + " " + ckItoa(int64(n))
}

// ckArtChip is the words of the chip on an agent row with a new surface.
const ckArtChip = "new artifact"
