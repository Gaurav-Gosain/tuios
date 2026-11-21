package app

import (
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"tuios.dev/loom"
	"tuios.dev/tml"
)

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

type ckArtList struct {
	items   []ckArtifact
	key     string
	loading bool
	err     string
}

type ckArtBox struct {
	x, y, w, h int
	id         string
}

type ckArtState struct {
	lists map[string]*ckArtList
	sel   string
	focus bool
	seen  map[string]bool
	snaps map[string]*ckArtSnap
	box   ckArtBox
	gen   uint64
}

type ckArtSnap struct {
	key string
	s   *loom.Surface
	pal string
}

type ckArtifactsMsg struct {
	window, key string
	items       []ckArtifact
	err         error
}

const ckArtTab = "Artifacts"

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
			msg.err = jsonx.Unmarshal(raw, &res)
			msg.items = res.Artifacts
		}
		return msg
	}
}

func (m *OS) applyCockpitArtifacts(msg ckArtifactsMsg) {
	l := m.Inbox.ck.art.lists[msg.window]
	if l == nil || l.key != msg.key {
		return
	}
	l.loading = false
	l.err = ""
	if msg.err != nil {
		if session.VerbErrCode(msg.err) == session.ErrVerbUnknownVerb {
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
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].At > fresh[j].At })
	out := append(fresh, listed...)
	for i := range out {
		if out[i].Kind == session.ArtifactTML && m.tml.views[out[i].ID] != nil {
			out[i].Open = true
		}
	}
	return out
}

func (m *OS) ckArtWindow(id string) *terminal.Window {
	for _, w := range m.Windows {
		if w != nil && w.Kind == session.WindowKindTML && session.TMLSurfaceID(w.URI) == id {
			return w
		}
	}
	return nil
}

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

// CockpitArtifactPaste puts pasted text into the focused text field of the
// surface the view draws. The person is in the surface, so a paste no field
// takes is dropped.
func (m *OS) CockpitArtifactPaste(text string) tea.Cmd {
	v, _ := m.ckArtLive()
	if v == nil || v.confirm != nil {
		return nil
	}
	defer m.cockpitTouch()
	res, err := v.surface.Paste(text)
	if err != nil {
		clientLog("TML surface %s: paste: %v", v.id, err)
	}
	cmd := m.tmlResult(nil, v, res)
	m.markTMLDirty(v.id)
	return cmd
}

func (m *OS) cockpitArtifactMouse(x, y int, button loom.MouseButton) (tea.Cmd, bool) {
	art := &m.Inbox.ck.art
	b := art.box
	if b.id == "" || x < b.x || x >= b.x+b.w || y < b.y || y >= b.y+b.h || m.cockpitTab() != ckArtTab {
		return nil, false
	}
	lm := loom.Mouse{X: x - b.x, Y: y - b.y, Button: button}
	v, _ := m.ckArtLive()
	if v == nil || v.id != b.id {
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

func (m *OS) cockpitArtifactArrived(v *tmlView) {
	if !m.ShowInbox || v == nil || v.caller == "" || v.surface == nil {
		return
	}
	m.cockpitTouchRows()
	e, ok := m.cockpitAgent()
	if !ok || e.Host != "" || e.WindowID != v.caller {
		return
	}
	m.ckArtShow(v.id, m.Inbox.reply.editor == nil && !m.CockpitArtifactFocused())
}

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
		m.EnterTerminalMode()
	}
	m.markTMLDirty(v.id)
}

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
		for i, line := range strings.Split(m.renderSurface(s, w, bh).Render(), "\n") {
			if i >= bh {
				break
			}
			c.draw(x, y+i, w, line)
		}
	}
}

func cmpTitle(a ckArtifact) string {
	if a.Title != "" {
		return a.Title
	}
	return a.Kind
}

func boolAttr(on bool) uint8 {
	if on {
		return ckBold
	}
	return 0
}

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
	m.tmlMotion(v)
	out := m.renderSurface(v.surface, w, sh).Render()
	m.tmlArmFrame(v)
	v.content = ""
	if confirm != "" {
		out += "\n" + confirm
	}
	return out
}

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

func (m *OS) ckArtTabLabel(e sidebarAgentEntry) string {
	n := len(m.ckArtifacts(e))
	if n == 0 {
		return ""
	}
	return ckArtTab + " " + ckItoa(int64(n))
}

const ckArtChip = "new artifact"
