package app

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"image/color"
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/tmldraw"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"tuios.dev/loom"
	"tuios.dev/tml"
)

// TML surfaces, the client's half. See session/tml_surface.go for the
// daemon's.
//
// The daemon pushes each surface (the tree, the state and a version) to this
// client, and this client draws it with loom in the content box of its
// window, which is a window of kind tml. Keys in terminal mode and the mouse
// go to the surface instead of the window's PTY. The surface's local actions
// (tabs, sorting, the cursor row, scrolling, set and toggle) run here at once;
// what changes document state goes back to the daemon with tml-input, so every
// client and a reattach see it. View state stays on this client.
//
// Nothing here runs on a timer. A surface is drawn again only when a push, a
// key or a click changed it, or the pane changed size; otherwise the pane
// serves its cached content as every pane does.

// tmlClient holds the surfaces this client draws.
type tmlClient struct {
	// mu guards pending, cmds and inflight, which the daemon's read loop and
	// the tml-input goroutines write.
	mu      sync.Mutex
	pending []*session.TMLSurfacePayload
	cmds    []tea.Cmd
	wake    chan struct{}

	// views is read and written on the UI goroutine only.
	views map[string]*tmlView
	// clockArmed says a minute tick is on its way for the views whose
	// bindings call ago(). UI goroutine only.
	clockArmed bool
	// setGen counts the views made and dropped, and gen every repaint of
	// one, for the keys of the agents view, which lists and draws them too.
	// UI goroutine only.
	setGen, gen uint64
	// shownDirty says a surface on the screen changed since the wake
	// began. UI goroutine only.
	shownDirty bool
}

// TMLClockMsg is the minute tick of the surfaces that call ago().
type TMLClockMsg struct{}

// tmlClockCmd arms the minute tick when a view needs it and none is armed.
// A surface without ago() never arms one.
func (m *OS) tmlClockCmd() tea.Cmd {
	if m.tml.clockArmed {
		return nil
	}
	for _, v := range m.tml.views {
		if v.surface != nil && v.surface.NeedsClock() {
			m.tml.clockArmed = true
			return tea.Tick(time.Minute, func(time.Time) tea.Msg { return TMLClockMsg{} })
		}
	}
	return nil
}

// handleTMLClock moves the ago() bindings on, and arms the next tick while
// a surface still needs it.
func (m *OS) handleTMLClock() tea.Cmd {
	m.tml.clockArmed = false
	for id, v := range m.tml.views {
		if v.surface != nil && v.surface.NeedsClock() {
			v.surface.Tick()
			m.markTMLDirty(id)
		}
	}
	return m.tmlClockCmd()
}

// tmlView is one surface as this client draws it.
type tmlView struct {
	id    string
	title string
	owner string
	// caller is the window id of the pane that opened the surface, "" for
	// the person's own. at is when this client first got it.
	caller     string
	at         time.Time
	version    uint64
	docVersion uint64
	doc        *tml.Node
	surface    *loom.Surface
	palKey     string
	// inflight counts the tml-input calls not yet answered. A push that only
	// echoes state waits until they are, so a value the person just set does
	// not flicker back to the one before it. deferred holds that push.
	inflight int
	deferred *session.TMLSurfacePayload
	// content is the last frame, for the size in w and h.
	content string
	w, h    int
	// refreshing says a tml-get is on its way, after a missed version.
	refreshing bool
	// confirm is a send-text the person has not said yes to yet, and
	// confirmed the targets they said yes to, per surface.
	confirm   *tmlPaneAction
	confirmed map[string]bool
	// sentView is this client's view state as last sent to the daemon,
	// which draws it for capture-pane, and viewAt when. viewDue says a
	// change waits for the flush tmlViewGap schedules.
	sentView []byte
	viewAt   time.Time
	viewDue  bool
}

// tmlParam is a string parameter of a pane() action.
func tmlParam(a tmlPaneAction, name string) string {
	t, _ := a.Params[name].(string)
	return t
}

// tmlActionSession is the session a pane() action acts in: the one it
// names, else this client's, which is the surface's.
func (m *OS) tmlActionSession(a tmlPaneAction) string {
	return cmp.Or(tmlParam(a, "session"), m.SessionName)
}

// tmlResolve fixes what a pane() action acts on before the person is asked:
// a send-text names one window of this session, by id, name or id prefix,
// and is sent with that id, so the yes and the typing are about one pane. A
// number, which the daemon would read as a place in the window list, and a
// window of another session are refused. It returns the action to run, or
// why it cannot.
func (m *OS) tmlResolve(a tmlPaneAction) (tmlPaneAction, string) {
	params := map[string]any{}
	maps.Copy(params, a.Params)
	// The session is sent by name, never empty, so the daemon does not
	// pick one: the yes is for the session the question named.
	params["session"] = m.tmlActionSession(a)
	a = tmlPaneAction{Verb: a.Verb, Params: params}
	if a.Verb != "send-text" {
		return a, ""
	}
	if m.tmlActionSession(a) != m.SessionName {
		return a, "pane in another session"
	}
	win := tmlParam(a, "window")
	if win == "" {
		return a, "no pane named"
	}
	if _, err := strconv.Atoi(win); err == nil {
		return a, "name the pane, not a number"
	}
	var found *terminal.Window
	for _, w := range m.Windows {
		if w == nil || w.Kind == session.WindowKindTML {
			continue
		}
		if w.ID == win || w.CustomName == win || (len(win) >= 8 && strings.HasPrefix(w.ID, win)) {
			if found != nil && found != w {
				return a, "two panes match " + tmlQuote(win)
			}
			found = w
		}
	}
	if found == nil {
		return a, "no pane " + tmlQuote(win)
	}
	params["window"] = found.ID
	return a, ""
}

// tmlConfirmKey names what the person said yes to. A send-text yes covers
// one pane, by its id, in one session. A new-window yes covers one exact
// command in one folder in one session, and nothing else.
func (m *OS) tmlConfirmKey(a tmlPaneAction) string {
	if a.Verb == "new-window" {
		raw, _ := json.Marshal(map[string]any{"command": a.Params["command"], "cwd": a.Params["cwd"], "name": a.Params["name"], "session": m.tmlActionSession(a)})
		return a.Verb + "\x00" + string(raw)
	}
	return a.Verb + "\x00" + m.tmlActionSession(a) + "\x00" + tmlParam(a, "window")
}

// tmlAsks reports whether a pane() action waits for the person's yes: one
// that types into a pane or starts a program.
func tmlAsks(a tmlPaneAction) bool {
	return a.Verb == "send-text" || a.Verb == "new-window"
}

// tmlCommandText is a new-window action's whole command as the question
// shows it: each word, quoted when it holds a space or a quote, or "" for
// the shell.
func tmlCommandText(a tmlPaneAction) string {
	var words []string
	switch c := a.Params["command"].(type) {
	case []any:
		for _, w := range c {
			s, _ := w.(string)
			words = append(words, tmlQuote(s))
		}
	case string:
		words = []string{strconv.Quote(c)}
	}
	return strings.Join(words, " ")
}

// tmlQuote shows a word as the question shows it: as is when it is plain,
// else in double quotes with every control or invisible character escaped
// (a tab as \t, a soft hyphen as \u00ad), so the person reads each character
// that will be used.
func tmlQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " '\"\\$`;&|<>()*?#~=[]{}") && strconv.Quote(s) == "\""+s+"\"" {
		return s
	}
	return strconv.Quote(s)
}

// tmlWrap cuts text into lines of at most w cells, by cells, so nothing of
// it is left out.
func tmlWrap(text string, w int) []string {
	w = max(w, 8)
	var lines []string
	for text != "" {
		line := ansi.Truncate(text, w, "")
		if line == "" {
			break
		}
		lines = append(lines, line)
		text = text[len(line):]
	}
	return lines
}

// tmlQuestion is the question under a surface, as plain lines, the keys
// last. A new-window question shows the whole command and the folder.
func (m *OS) tmlQuestion(a tmlPaneAction, w int) []string {
	sess := tmlQuote(m.tmlActionSession(a))
	if a.Verb == "new-window" {
		lines := tmlWrap("Let this surface run this in a new window in "+sess+"?", w)
		if cmdText := tmlCommandText(a); cmdText != "" {
			lines = append(lines, tmlWrap("  "+cmdText, w)...)
		} else {
			lines = append(lines, "  a shell")
		}
		if cwd, ok := a.Params["cwd"].(string); ok {
			lines = append(lines, tmlWrap("  in the folder "+tmlQuote(cwd), w)...)
		}
		if name, ok := a.Params["name"].(string); ok {
			lines = append(lines, tmlWrap("  with the window name "+tmlQuote(name), w)...)
		}
		return append(lines, "y yes  n no")
	}
	where := sess + "/" + tmlWindowLabel(m, tmlParam(a, "window"))
	text := "Let this surface type into pane " + where + "?"
	if ansi.StringWidth(text)+13 <= w {
		return []string{text + " y yes  n no"}
	}
	return append(tmlWrap("Type into "+where+"?", w), "y yes  n no")
}

// tmlWrapWords wraps plain text at spaces to lines of at most w cells.
func tmlWrapWords(text string, w int) []string {
	var lines []string
	line := ""
	for word := range strings.FieldsSeq(text) {
		if line != "" && ansi.StringWidth(line)+1+ansi.StringWidth(word) > w {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	return append(lines, line)
}

// tmlQuestionFits reports whether a question of n lines fits a window h
// rows tall, with a row of the surface left above it.
func tmlQuestionFits(n, h int) bool {
	return n <= min(12, h-1)
}

// tmlTooSmall stands in for a question that does not fit the window now.
const tmlTooSmall = "Make this pane bigger to see the whole question."

// tmlWindowLabel names a window id for the question: its name and its short
// id.
func tmlWindowLabel(m *OS, id string) string {
	for _, w := range m.Windows {
		if w != nil && w.ID == id {
			short := id
			if len(short) > 8 {
				short = short[:8]
			}
			if w.CustomName != "" {
				return w.CustomName + " (" + short + ")"
			}
			return short
		}
	}
	return id
}

// TMLWakeMsg says a push, an answer or a side effect for a surface is waiting.
type TMLWakeMsg struct{}

// initTML makes the wake channel. It runs before the listener is armed.
func (m *OS) initTML() {
	if m.tml.wake == nil {
		m.tml.wake = make(chan struct{}, 1)
	}
}

// listenForTML arms the listener for the next wake.
func (m *OS) listenForTML() tea.Cmd {
	m.initTML()
	return listenOnce(m.tml.wake, func(struct{}) tea.Msg { return TMLWakeMsg{} })
}

// signalTML wakes the UI goroutine. It never blocks.
func (m *OS) signalTML() {
	select {
	case m.tml.wake <- struct{}{}:
	default:
	}
}

// queueTMLSurface takes a push from the daemon's read loop.
func (m *OS) queueTMLSurface(p *session.TMLSurfacePayload) {
	m.tml.mu.Lock()
	// Kept in order: a push that carries a patch applies on the version
	// before it. The daemon folds pushes for a slow client, so few wait.
	m.tml.pending = append(m.tml.pending, p)
	m.tml.mu.Unlock()
	m.signalTML()
}

// queueTMLCmd hands a side effect to the UI goroutine, from a path that has
// no tea.Cmd to return, such as a click.
func (m *OS) queueTMLCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	m.tml.mu.Lock()
	m.tml.cmds = append(m.tml.cmds, cmd)
	m.tml.mu.Unlock()
	m.signalTML()
}

// handleTMLWake applies what is waiting and arms the listener again.
func (m *OS) handleTMLWake() tea.Cmd {
	m.tml.mu.Lock()
	pending := m.tml.pending
	m.tml.pending = nil
	cmds := m.tml.cmds
	m.tml.cmds = nil
	m.tml.mu.Unlock()
	changed := false
	setGen := m.tml.setGen
	m.tml.shownDirty = false
	for _, p := range pending {
		if m.applyTMLSurface(p) {
			changed = true
		}
	}
	// A view whose answers are all in applies the push it held back.
	for _, v := range m.tml.views {
		m.tml.mu.Lock()
		ready := v.inflight == 0 && v.deferred != nil
		var d *session.TMLSurfacePayload
		if ready {
			d, v.deferred = v.deferred, nil
		}
		m.tml.mu.Unlock()
		if d != nil && m.applyTMLSurface(d) {
			changed = true
		}
	}
	// Nothing on the screen changed: a hidden surface took a patch, or
	// nothing changed at all. The frame stays. A surface that opened or
	// closed changes the agents view's list and the dock.
	if !changed || !m.tml.shownDirty && setGen == m.tml.setGen {
		m.renderSkipped = len(cmds) == 0
	}
	cmds = append(cmds, m.listenForTML(), m.tmlClockCmd())
	return tea.Batch(cmds...)
}

// applyTMLSurface brings one view up to a push. It reports whether a window
// has to be drawn again.
func (m *OS) applyTMLSurface(p *session.TMLSurfacePayload) bool {
	if m.tml.views == nil {
		m.tml.views = map[string]*tmlView{}
	}
	v := m.tml.views[p.Surface]
	if p.Closed {
		if v == nil {
			return false
		}
		delete(m.tml.views, p.Surface)
		m.tml.setGen++
		m.markTMLDirty(p.Surface)
		return true
	}
	if v != nil && p.Version <= v.version {
		if len(p.State) > 0 {
			v.refreshing = false
		}
		return false
	}
	if v != nil && p.DocVersion == v.docVersion {
		m.tml.mu.Lock()
		busy := v.inflight > 0
		if busy {
			v.deferred = p
		}
		m.tml.mu.Unlock()
		if busy {
			return false
		}
	}
	// A push that carries a patch: one version behind on the same tree,
	// the patch repaints only what reads the paths it changed. Otherwise
	// this client missed a version, and reads the surface again.
	if len(p.Ops) > 0 && len(p.State) == 0 {
		if v != nil && v.surface != nil && p.DocVersion == v.docVersion && p.Version == v.version+1 {
			lim := tml.PersonLimits()
			if ops, err := tml.DecodePatch(p.Ops, &lim); err == nil && v.surface.Patch(ops) == nil {
				v.version = p.Version
				m.markTMLDirty(p.Surface)
				return true
			}
		}
		// One read at a time: the pushes that follow it are older than
		// what it brings.
		if v == nil || !v.refreshing {
			if v != nil {
				v.refreshing = true
			}
			m.queueTMLCmd(m.tmlRefresh(p.Surface))
		}
		return false
	}
	if v == nil || p.DocVersion != v.docVersion || v.doc == nil {
		if len(p.Doc) == 0 {
			// A tree this client never got; the next push that carries
			// one draws it. Every push after an attach or a replace does.
			return false
		}
		// The daemon checked the document against the limits of its owner,
		// which are at most the person's, before it ever pushed it.
		lim := tml.PersonLimits()
		doc, err := tml.Parse(p.Doc, &tml.ParseOptions{Limits: &lim})
		if err != nil {
			clientLog("TML surface %s: the pushed document does not parse: %v", p.Surface, err)
			return false
		}
		if v == nil {
			v = &tmlView{id: p.Surface, at: time.Now()}
			m.tml.views[p.Surface] = v
			m.tml.setGen++
			defer m.cockpitArtifactArrived(v)
		}
		v.doc = doc
	}
	doc := v.doc.Clone()
	if len(p.State) > 0 {
		doc.SetAttr("state", tml.Value{Kind: tml.KindObject, Raw: json.RawMessage(p.State)})
	}
	if v.surface == nil {
		s, err := loom.New(doc, &loom.Options{Palette: m.tmlPalette(), Ground: loom.GroundNone, Limits: new(tml.PersonLimits())})
		if err != nil {
			clientLog("TML surface %s: %v", p.Surface, err)
			return false
		}
		v.surface, v.palKey = s, m.tmlPaletteKey()
	} else if err := v.surface.Replace(doc, false); err != nil {
		clientLog("TML surface %s: %v", p.Surface, err)
		return false
	}
	v.title, v.owner = p.Title, p.Owner
	// The opening pane never changes. A refresh read with tml-get carries
	// no caller, so it keeps the one the first push gave.
	if p.Caller != "" {
		v.caller = p.Caller
	}
	v.refreshing = false
	v.version, v.docVersion = p.Version, p.DocVersion
	v.content = ""
	m.markTMLDirty(p.Surface)
	return true
}

// DrawsTML reports whether this client draws w as a TML surface: a window of
// kind tml, on a client of this machine's daemon. A client attached through
// a host shows the window's placeholder line, since what the person does on
// a surface goes back through this machine's socket.
func (m *OS) DrawsTML(w *terminal.Window) bool {
	return w != nil && w.Kind == session.WindowKindTML && m.DaemonClient != nil && m.DaemonClient.DrawsTML()
}

// pruneTMLViews forgets the views of surfaces no window of state shows: the
// surfaces that closed while this client was away, or that belong to the
// session it left. An attach pushes every open surface again.
func (m *OS) pruneTMLViews(state *session.SessionState) {
	if len(m.tml.views) == 0 || state == nil {
		return
	}
	shown := map[string]bool{}
	for _, w := range state.Windows {
		if w.Kind == session.WindowKindTML {
			shown[session.TMLSurfaceID(w.URI)] = true
		}
	}
	for id := range m.tml.views {
		if !shown[id] {
			delete(m.tml.views, id)
			m.tml.setGen++
		}
	}
}

// tmlRefresh reads a surface whole with tml-get and applies it as a push, for
// a client that missed a version. Its answer comes back through the wake.
func (m *OS) tmlRefresh(id string) tea.Cmd {
	call := m.inboxCaller()
	return func() tea.Msg {
		raw, err := call("tml-get", map[string]any{"surface": id}, tmlInputTimeout)
		if err != nil {
			// Once more, then give up: the next full push draws the
			// surface, and a later gap reads again.
			raw, err = call("tml-get", map[string]any{"surface": id}, tmlInputTimeout)
		}
		if err != nil {
			clientLog("TML surface %s: tml-get: %v", id, err)
			m.queueTMLCmd(func() tea.Msg { return tmlRefreshFailedMsg{id: id} })
			return nil
		}
		var res struct {
			WindowID   string          `json:"window_id"`
			Title      string          `json:"title"`
			Owner      string          `json:"owner"`
			Version    uint64          `json:"version"`
			DocVersion uint64          `json:"doc_version"`
			Doc        json.RawMessage `json:"doc"`
			State      json.RawMessage `json:"state"`
		}
		if json.Unmarshal(raw, &res) != nil {
			return nil
		}
		m.queueTMLSurface(&session.TMLSurfacePayload{Surface: id, WindowID: res.WindowID, Title: res.Title, Owner: res.Owner,
			Version: res.Version, DocVersion: res.DocVersion, Doc: res.Doc, State: res.State})
		return nil
	}
}

// tmlRefreshFailedMsg says a refresh read failed twice, so the next gap may
// read again.
type tmlRefreshFailedMsg struct{ id string }

// tmlViewOf is the view a window draws, or nil.
func (m *OS) tmlViewOf(w *terminal.Window) *tmlView {
	if w == nil || w.Kind != session.WindowKindTML {
		return nil
	}
	return m.tml.views[session.TMLSurfaceID(w.URI)]
}

// markTMLDirty marks the windows that draw a surface for a repaint. A
// window that is not on the screen, minimized or on another workspace, keeps
// the frame on the screen: it draws the surface again when it shows. So a
// patch to a hidden surface costs the patch and no frame.
func (m *OS) markTMLDirty(id string) {
	m.tml.gen++
	if m.ShowInbox {
		// The agents view may draw the surface.
		m.tml.shownDirty = true
	}
	for _, w := range m.Windows {
		if w != nil && w.Kind == session.WindowKindTML && session.TMLSurfaceID(w.URI) == id {
			w.MarkContentDirty()
			if !w.Minimized && w.Workspace == m.CurrentWorkspace {
				m.cachedViewContent = ""
				m.tml.shownDirty = true
			}
		}
	}
}

// renderTML draws a TML window's content box. It is renderTerminal for a
// window of kind tml, and serves the last frame when nothing changed.
func (m *OS) renderTML(window *terminal.Window, dim int) string {
	cw, ch := max(window.ContentWidth(), 1), max(window.ContentHeight(), 1)
	v := m.tmlViewOf(window)
	if v == nil || v.surface == nil {
		// The window arrived before its surface, or the surface ended. The
		// push that follows draws it.
		out := tmlWaitingFrame(cw, ch)
		cacheRender(window, out, 0, 0, dim)
		return out
	}
	if key := m.tmlPaletteKey(); key != v.palKey {
		v.surface.SetPalette(m.tmlPalette())
		v.palKey = key
	}
	// The document reads where it is as view.placement: esc closes a popup
	// and not a pane, so a hint can say so only where it is true.
	placement := "pane"
	if m.isTMLPopup(window) {
		placement = "popup"
	}
	v.surface.SetPlacement(placement)
	// The surface sits inside a margin; see tmlInset.
	dx, dy, iw, ih := m.tmlInset(window)
	fullW, fullH := cw, ch
	cw, ch = max(iw, 1), max(ih, 1)
	sh := ch
	confirmText := ""
	if v.confirm != nil {
		// The question is drawn whole or not at all: a window made smaller
		// after the press shows a line that asks for room, and y waits.
		if tmlQuestionFits(len(m.tmlQuestion(*v.confirm, cw)), ch) {
			confirmText = m.tmlConfirmLine(v, cw)
		} else {
			ask := lipgloss.NewStyle().Foreground(theme.UI().Warning).Bold(true)
			var lines []string
			for _, l := range tmlWrapWords(tmlTooSmall, cw) {
				lines = append(lines, ask.Render(l))
			}
			confirmText = strings.Join(lines[:min(len(lines), max(ch-1, 1))], "\n")
		}
		sh = max(ch-strings.Count(confirmText, "\n")-1, 1)
	}
	buf := v.surface.Render(cw, sh)
	if damage := v.surface.Damage(); len(damage) > 0 || v.content == "" || v.w != cw || v.h != sh {
		v.content, v.w, v.h = buf.Render(), cw, sh
	}
	out := v.content
	if confirmText != "" && sh < ch {
		out += "\n" + confirmText
	}
	out = tmldraw.Pad(out, dx, dy, fullW, fullH)
	cacheRender(window, out, 0, 0, dim)
	return out
}

// tmlConfirmLine asks the person whether the surface may type into a pane
// or start a program. tuios draws it under the surface, so a document cannot
// draw it or hide it.
func (m *OS) tmlConfirmLine(v *tmlView, w int) string {
	pal := theme.UI()
	ask := lipgloss.NewStyle().Foreground(pal.Warning).Bold(true)
	plain := lipgloss.NewStyle().Foreground(pal.Fg)
	key := lipgloss.NewStyle().Foreground(pal.AccentBright).Bold(true)
	dim := lipgloss.NewStyle().Foreground(pal.FgDim)
	keys := key.Render("y") + dim.Render(" yes  ") + key.Render("n") + dim.Render(" no")
	lines := m.tmlQuestion(*v.confirm, w)
	var out []string
	for i, l := range lines {
		switch {
		case i == len(lines)-1 && l == "y yes  n no":
			out = append(out, keys)
		case i == len(lines)-1 && strings.HasSuffix(l, " y yes  n no"):
			out = append(out, ask.Render(strings.TrimSuffix(l, "y yes  n no"))+keys)
		case strings.HasPrefix(l, "Let this surface") || strings.HasPrefix(l, "Type into") || i == 0 && !strings.HasPrefix(l, "  "):
			out = append(out, ask.Render(l))
		default:
			out = append(out, plain.Render(l))
		}
	}
	return strings.Join(out, "\n")
}

// tmlWaitingFrame is the content of a TML window with no surface yet.
func tmlWaitingFrame(w, h int) string {
	line := "Waiting for the surface."
	if len(line) > w {
		line = line[:w]
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Foreground(theme.UI().FgDim).Render(line))
	for range h - 1 {
		b.WriteByte('\n')
	}
	return b.String()
}

// tmlPaletteKey names the palette the client draws with now: the depth, the
// glyph set and the theme's colours. It changes when any of them does.
func (m *OS) tmlPaletteKey() string {
	pal := theme.UI()
	var b strings.Builder
	b.WriteString(strconv.Itoa(int(overlay.CurrentDepth())))
	b.WriteString(strconv.FormatBool(m.Settings.UseASCIIOnly))
	for _, c := range []color.Color{pal.Fg, pal.Accent, pal.Surface, pal.Panel, pal.Canvas, pal.Success, pal.Warn} {
		b.WriteByte('|')
		b.WriteString(colorKey(c))
	}
	return b.String()
}

func colorKey(c color.Color) string {
	if c == nil {
		return "-"
	}
	if _, none := c.(lipgloss.NoColor); none {
		return "0"
	}
	r, g, b, a := c.RGBA()
	return strconv.FormatUint(uint64(r)<<48|uint64(g)<<32|uint64(b)<<16|uint64(a), 36)
}

// tmlPalette is the loom palette of this client's depth and theme. See
// tmldraw.Palette.
func (m *OS) tmlPalette() *loom.Palette {
	return tmldraw.Palette(theme.UI(), m.Settings.UseASCIIOnly)
}

// TMLKey gives a key to the surface of a TML window. A popup closes on an
// esc the surface does not use, with no value.
func (m *OS) TMLKey(w *terminal.Window, key string) tea.Cmd {
	v := m.tmlViewOf(w)
	if v == nil || v.surface == nil {
		return nil
	}
	if v.confirm != nil {
		a := *v.confirm
		if (key == "y" || key == "n") && !tmlQuestionFits(len(m.tmlQuestion(a, w.ContentWidth())), w.ContentHeight()) {
			// The person cannot see the whole question, so no answer counts.
			return nil
		}
		switch key {
		case "y":
			v.confirm = nil
			if v.confirmed == nil {
				v.confirmed = map[string]bool{}
			}
			v.confirmed[m.tmlConfirmKey(a)] = true
			w.MarkContentDirty()
			m.cachedViewContent = ""
			return m.tmlInput(v, tmlInputReq{actions: []tmlPaneAction{a}})
		case "n", "esc":
			v.confirm = nil
			w.MarkContentDirty()
			m.cachedViewContent = ""
		}
		return nil
	}
	res, err := v.surface.Key(key)
	if err != nil {
		clientLog("TML surface %s: key %s: %v", v.id, key, err)
	}
	if key == "esc" && w.IsPopup && len(res.Changed) == 0 && !res.View && len(res.Effects) == 0 {
		return m.tmlInput(v, tmlInputReq{closing: true})
	}
	return m.tmlResult(w, v, res)
}

// TMLPaste puts pasted text into the focused text field of a TML window's
// surface. It reports whether a field took it. The state change goes to
// the daemon through the wake, as a click's does.
func (m *OS) TMLPaste(w *terminal.Window, text string) bool {
	v := m.tmlViewOf(w)
	if v == nil || v.surface == nil || v.confirm != nil {
		return false
	}
	res, err := v.surface.Paste(text)
	if err != nil {
		clientLog("TML surface %s: paste: %v", v.id, err)
	}
	if len(res.Changed) == 0 && !res.View {
		return false
	}
	m.queueTMLCmd(m.tmlResult(w, v, res))
	return true
}

// TMLMouse gives a mouse event, in the window's content cells, to the
// surface of a TML window. Its side effects run through the wake, since a
// mouse path returns no command.
func (m *OS) TMLMouse(w *terminal.Window, ev uv.MouseEvent) {
	v := m.tmlViewOf(w)
	if v == nil || v.surface == nil {
		return
	}
	mouse := ev.Mouse()
	dx, dy, _, _ := m.tmlInset(w)
	lm := loom.Mouse{X: mouse.X - dx, Y: mouse.Y - dy}
	switch mouse.Button {
	case uv.MouseLeft:
		lm.Button = loom.MouseLeft
	case uv.MouseMiddle:
		lm.Button = loom.MouseMiddle
	case uv.MouseRight:
		lm.Button = loom.MouseRight
	case uv.MouseWheelUp:
		lm.Button = loom.WheelUp
	case uv.MouseWheelDown:
		lm.Button = loom.WheelDown
	}
	switch ev.(type) {
	case uv.MouseReleaseEvent:
		lm.Action = loom.Release
	case uv.MouseMotionEvent:
		lm.Action = loom.Motion
	}
	if v.confirm != nil {
		// The question under the surface waits for y or n.
		return
	}
	res, err := v.surface.Mouse(lm)
	if err != nil {
		clientLog("TML surface %s: mouse: %v", v.id, err)
	}
	m.queueTMLCmd(m.tmlResult(w, v, res))
}

// tmlResult carries out what an input did: a repaint, the state for the
// daemon, and the effects.
func (m *OS) tmlResult(w *terminal.Window, v *tmlView, res loom.Result) tea.Cmd {
	if len(res.Changed) > 0 || res.View || len(res.Effects) > 0 {
		m.tmlRepaint(w, v)
	}
	roomW, roomH := m.tmlRoom(w, v)
	var cmds []tea.Cmd
	ops := tmlStateOps(v.surface.State(), res.Changed)
	closing, hasValue := false, false
	var value any
	var events []tmlEmit
	var actions []tmlPaneAction
	for _, e := range res.Effects {
		switch e.Action {
		case "pane":
			a := tmlPaneAction{Verb: e.Name}
			a.Params, _ = e.Value.(map[string]any)
			a, why := m.tmlResolve(a)
			if why != "" {
				m.ShowNotification("Not run: "+why+".", "warning", m.Settings.NotificationDuration)
				continue
			}
			if tmlAsks(a) && !tmlQuestionFits(len(m.tmlQuestion(a, roomW)), roomH) {
				// The whole question must fit, or the person would say
				// yes to a part of it.
				m.ShowNotification("The surface's command is too long to show here, so tuios does not run it.", "warning", m.Settings.NotificationDuration)
				continue
			}
			if tmlAsks(a) && !v.confirmed[m.tmlConfirmKey(a)] {
				// The first time a surface types into a pane, or starts a
				// program, the person says yes to it. The action waits for
				// the answer.
				v.confirm = &a
				m.tmlRepaint(w, v)
				continue
			}
			actions = append(actions, a)
		case "emit":
			events = append(events, tmlEmit{Name: e.Name, Value: e.Value})
		case "close":
			if !closing {
				closing, value, hasValue = true, e.Value, true
			}
		case "copy":
			cmds = append(cmds, m.CopyToClipboard(tml.Stringify(e.Value)))
		default:
			// open and link reach files and pages, which a later version
			// asks the person about. They do nothing yet.
			clientLog("TML surface %s: %s is not carried out yet", v.id, e.Action)
		}
	}
	if len(actions) > tmlActionsPerPress {
		m.ShowNotification(fmt.Sprintf("One press can run at most %d pane actions. Nothing ran.", tmlActionsPerPress), "warning", m.Settings.NotificationDuration)
		actions = nil
	}
	// The view travels when it changed, so capture-pane draws what the
	// person looks at. See tmlViewChange.
	view, flush := m.tmlViewChange(v, res.View)
	if flush != nil {
		cmds = append(cmds, flush)
	}
	if len(ops) > 0 || closing || len(events) > 0 || len(actions) > 0 || view != nil {
		cmds = append(cmds, m.tmlInput(v, tmlInputReq{ops: ops, events: events, actions: actions, closing: closing, value: value, hasValue: hasValue, view: view}))
	}
	return tea.Batch(cmds...)
}

// tmlViewGap is the least time between two reports of a view that only
// changed. A wheel scroll changes the view on every step. Its last place
// goes in one report after the gap.
const tmlViewGap = 250 * time.Millisecond

// TMLViewFlushMsg sends a view change that waited for tmlViewGap.
type TMLViewFlushMsg struct{ id string }

// tmlViewChange returns v's view as JSON when an input changed it and it
// differs from the last one sent, for the tml-input of that input. A change
// within tmlViewGap of the last report waits: the command it returns sends
// it after the gap. Typed text that binds no state path stays here.
func (m *OS) tmlViewChange(v *tmlView, changed bool) (json.RawMessage, tea.Cmd) {
	if (!changed && !v.viewDue) || v.surface == nil {
		return nil, nil
	}
	view := v.surface.View()
	view.Text, view.Caret = nil, nil
	raw, err := json.Marshal(view)
	if err != nil || bytes.Equal(raw, v.sentView) {
		v.viewDue = false
		return nil, nil
	}
	if wait := tmlViewGap - time.Since(v.viewAt); wait > 0 {
		if v.viewDue {
			return nil, nil
		}
		v.viewDue = true
		id := v.id
		return nil, tea.Tick(wait, func(time.Time) tea.Msg { return TMLViewFlushMsg{id: id} })
	}
	v.sentView, v.viewAt, v.viewDue = raw, time.Now(), false
	return raw, nil
}

// handleTMLViewFlush sends the view change of a surface that waited.
func (m *OS) handleTMLViewFlush(id string) tea.Cmd {
	v := m.tml.views[id]
	if v == nil || !v.viewDue {
		return nil
	}
	v.viewAt = time.Time{}
	raw, _ := m.tmlViewChange(v, true)
	if raw == nil {
		return nil
	}
	return m.tmlInput(v, tmlInputReq{view: raw})
}

// tmlRepaint marks a surface for a repaint: its window when it has one here,
// and the agents view, which may draw it instead.
func (m *OS) tmlRepaint(w *terminal.Window, v *tmlView) {
	if w != nil {
		w.MarkContentDirty()
	}
	m.cachedViewContent = ""
	m.tml.gen++
	if v != nil {
		// The window and the agents view draw the surface at their own
		// sizes, so neither keeps the other's last frame.
		v.content = ""
	}
}

// tmlRoom is the room a surface is drawn in: its window's content box, or
// the box the agents view last drew it in.
func (m *OS) tmlRoom(w *terminal.Window, v *tmlView) (int, int) {
	if w != nil {
		return w.ContentWidth(), w.ContentHeight()
	}
	return m.Inbox.ck.art.box.w, m.Inbox.ck.art.box.h
}

// tmlStateOps turns the state paths a local action changed into a JSON Patch
// that writes the same values on the daemon. A path whose ancestor is in the
// list already rides with it.
func tmlStateOps(state map[string]any, changed []string) []tml.Op {
	var ops []tml.Op
	var done []string
	for _, p := range changed {
		covered := false
		for _, d := range done {
			if p == d || strings.HasPrefix(p, d+"/") {
				covered = true
				break
			}
		}
		if covered || p == "" {
			continue
		}
		done = append(done, p)
		parent, val, ok := tmlLookup(state, p)
		if !ok {
			ops = append(ops, tml.Op{Op: "remove", Path: p})
			continue
		}
		raw, err := json.Marshal(val)
		if err != nil {
			continue
		}
		op := "add"
		if _, inArray := parent.([]any); inArray {
			op = "replace"
		}
		ops = append(ops, tml.Op{Op: op, Path: p, Value: raw})
	}
	return ops
}

// tmlLookup finds a JSON Pointer in state. It returns the container that
// holds the value, the value, and whether it is there.
func tmlLookup(state map[string]any, pointer string) (any, any, bool) {
	toks := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	var parent any
	cur := any(state)
	for _, t := range toks {
		t = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
		parent = cur
		switch c := cur.(type) {
		case map[string]any:
			next, ok := c[t]
			if !ok {
				return parent, nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(t)
			if err != nil || i < 0 || i >= len(c) {
				return parent, nil, false
			}
			cur = c[i]
		default:
			return parent, nil, false
		}
	}
	return parent, cur, true
}

// TMLInputFailedMsg says the daemon refused what the person did on a
// surface.
type TMLInputFailedMsg struct{ Text string }

// tmlInputTimeout bounds one tml-input call.
const tmlInputTimeout = 5 * time.Second

// tmlEmit is one emit action, for the owner.
type tmlEmit struct {
	Name  string `json:"name"`
	Value any    `json:"value,omitempty"`
}

// tmlActionsPerPress is the most pane actions one key or click runs, as the
// daemon holds a tml-input to.
const tmlActionsPerPress = 4

// tmlInputReq is what one tml-input carries.
type tmlInputReq struct {
	ops      []tml.Op
	events   []tmlEmit
	actions  []tmlPaneAction
	closing  bool
	value    any
	hasValue bool
	view     json.RawMessage
}

// tmlPaneAction is one pane() action, run by the daemon with the grants of
// the surface's owner.
type tmlPaneAction struct {
	Verb   string         `json:"verb"`
	Params map[string]any `json:"params"`
}

// TMLActionFailedMsg says the daemon refused a pane() action of a surface.
type TMLActionFailedMsg struct{ Text string }

// tmlInput sends what the person did to the daemon, off the UI goroutine:
// the state, the emits for the owner, the pane() actions, and a close.
func (m *OS) tmlInput(v *tmlView, r tmlInputReq) tea.Cmd {
	call, nonce := m.inboxCaller(), m.inboxNonce()
	params := map[string]any{"surface": v.id, "human_nonce": nonce}
	if len(r.ops) > 0 {
		params["ops"] = r.ops
	}
	if len(r.events) > 0 {
		params["events"] = r.events
	}
	if len(r.actions) > 0 {
		params["actions"] = r.actions
	}
	if r.view != nil {
		params["view"] = r.view
	}
	if r.closing {
		params["close"] = true
		if r.hasValue {
			raw, err := json.Marshal(r.value)
			if err != nil {
				raw = []byte("null")
			}
			params["value"] = json.RawMessage(raw)
		}
	}
	m.tml.mu.Lock()
	v.inflight++
	m.tml.mu.Unlock()
	return func() tea.Msg {
		raw, err := call("tml-input", params, tmlInputTimeout)
		m.tml.mu.Lock()
		v.inflight--
		m.tml.mu.Unlock()
		m.signalTML()
		if err != nil {
			clientLog("TML surface %s: tml-input: %v", v.id, err)
			// The person sees what did not go through, such as an emit
			// value over the limit.
			return TMLInputFailedMsg{Text: "The surface could not send what you did: " + err.Error()}
		}
		var res struct {
			Actions []struct {
				Verb  string `json:"verb"`
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			} `json:"actions"`
		}
		if json.Unmarshal(raw, &res) == nil {
			for _, a := range res.Actions {
				if !a.OK {
					return TMLActionFailedMsg{Text: "The surface could not run " + a.Verb + ": " + a.Error}
				}
			}
		}
		return nil
	}
}
