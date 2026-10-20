package app

import (
	"cmp"
	"encoding/json"
	"image/color"
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
	id         string
	title      string
	owner      string
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

// tmlConfirmKey names what the person said yes to: the verb, the session and
// the window, by id where this client knows it, so a second name for the
// same pane asks again only when it names another pane.
func (m *OS) tmlConfirmKey(a tmlPaneAction) string {
	win := tmlParam(a, "window")
	if m.tmlActionSession(a) == m.SessionName {
		for _, w := range m.Windows {
			if w != nil && (w.ID == win || w.CustomName == win) {
				win = w.ID
				break
			}
		}
	}
	key := a.Verb + "\x00" + m.tmlActionSession(a) + "\x00" + win
	if a.Verb == "new-window" {
		// Each command is asked about on its own.
		key += "\x00" + tmlCommandText(a)
	}
	return key
}

// tmlAsks reports whether a pane() action waits for the person's yes: one
// that types into a pane or starts a program.
func tmlAsks(a tmlPaneAction) bool {
	return a.Verb == "send-text" || a.Verb == "new-window"
}

// tmlCommandText is a new-window action's command as the question shows it,
// at most 40 cells, or "" for the shell.
func tmlCommandText(a tmlPaneAction) string {
	var words []string
	switch c := a.Params["command"].(type) {
	case []any:
		for _, w := range c {
			if s, ok := w.(string); ok {
				words = append(words, s)
			}
		}
	case string:
		words = []string{c}
	}
	text := strings.Join(strings.Fields(tml.Sanitize(strings.Join(words, " "))), " ")
	return ansi.Truncate(text, 40, "…")
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
	if !changed {
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
			v = &tmlView{id: p.Surface}
			m.tml.views[p.Surface] = v
		}
		v.doc = doc
	}
	doc := v.doc.Clone()
	if len(p.State) > 0 {
		doc.SetAttr("state", tml.Value{Kind: tml.KindObject, Raw: json.RawMessage(p.State)})
	}
	if v.surface == nil {
		s, err := loom.New(doc, &loom.Options{Palette: m.tmlPalette(), Limits: ptr(tml.PersonLimits())})
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
	v.refreshing = false
	v.version, v.docVersion = p.Version, p.DocVersion
	v.content = ""
	m.markTMLDirty(p.Surface)
	return true
}

func ptr[T any](v T) *T { return &v }

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

// markTMLDirty marks the windows that draw a surface for a repaint.
func (m *OS) markTMLDirty(id string) {
	for _, w := range m.Windows {
		if w != nil && w.Kind == session.WindowKindTML && session.TMLSurfaceID(w.URI) == id {
			w.MarkContentDirty()
			m.cachedViewContent = ""
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
	sh := ch
	if v.confirm != nil && ch > 1 {
		sh = ch - strings.Count(m.tmlConfirmLine(v, cw), "\n") - 1
		sh = max(sh, 1)
	}
	buf := v.surface.Render(cw, sh)
	if damage := v.surface.Damage(); len(damage) > 0 || v.content == "" || v.w != cw || v.h != sh {
		v.content, v.w, v.h = buf.Render(), cw, sh
	}
	out := v.content
	if sh < ch {
		out += "\n" + m.tmlConfirmLine(v, cw)
	}
	cacheRender(window, out, 0, 0, dim)
	return out
}

// tmlConfirmLine asks the person whether the surface may type into a pane.
// tuios draws it under the surface, so a document cannot draw it or hide it.
func (m *OS) tmlConfirmLine(v *tmlView, w int) string {
	pal := theme.UI()
	a := *v.confirm
	where := m.tmlActionSession(a) + "/" + tmlParam(a, "window")
	text := "Let this surface type into pane " + where + "? "
	short := "Type into " + where + "? "
	if a.Verb == "new-window" {
		what := cmp.Or(tmlCommandText(a), "a shell")
		text = "Let this surface run " + what + " in a new window in " + m.tmlActionSession(a) + "? "
		short = "Run " + what + " in " + m.tmlActionSession(a) + "? "
	}
	keys := lipgloss.NewStyle().Foreground(pal.AccentBright).Bold(true).Render("y") +
		lipgloss.NewStyle().Foreground(pal.FgDim).Render(" yes  ") +
		lipgloss.NewStyle().Foreground(pal.AccentBright).Bold(true).Render("n") +
		lipgloss.NewStyle().Foreground(pal.FgDim).Render(" no")
	if ansi.StringWidth(text)+12 > w {
		text = short
	}
	ask := lipgloss.NewStyle().Foreground(pal.Warning).Bold(true)
	if ansi.StringWidth(text)+12 > w {
		// A narrow pane puts the keys on a line of their own, so they are
		// never the part that is cut.
		return ansi.Truncate(ask.Render(strings.TrimSpace(text)), w, "…") + "\n" + keys
	}
	return ask.Render(text) + keys
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

// tmlPalette maps the chrome palette of this client's depth onto loom's
// roles, so a surface looks like the rest of tuios and keeps its contrast
// floors. At 16 colours the grounds are the terminal's own, so they are left
// out and loom draws the cursor row and a primary button in reverse video.
func (m *OS) tmlPalette() *loom.Palette {
	pal := theme.UI()
	depth := loom.TrueColor
	switch pal.Depth {
	case overlay.Depth256:
		depth = loom.Color256
	case overlay.Depth16:
		depth = loom.Color16
	}
	if m.Settings.UseASCIIOnly {
		depth = loom.ASCII
	}
	roles := map[string]color.Color{
		"fg": pal.Fg, "dim": pal.FgDim, "mute": pal.FgMute,
		"accent": pal.Accent, "accent-bright": pal.AccentBright,
		"success": pal.Success, "warning": pal.Warning, "error": pal.Warn, "info": pal.Info,
		"attention": pal.Attention, "busy": pal.Busy, "edge": pal.Edge,
		"canvas": pal.Canvas, "panel": pal.Panel, "surface": pal.Surface, "card": pal.Card,
		"accent-tint": pal.AccentTint, "success-tint": pal.SuccessTint,
		"warning-tint": pal.WarningTint, "error-tint": pal.WarnTint,
		loom.RoleCursor: pal.RowSel, loom.RolePill: pal.PillFg,
	}
	for k, c := range roles {
		if c == nil {
			delete(roles, k)
			continue
		}
		if _, none := c.(lipgloss.NoColor); none {
			delete(roles, k)
		}
	}
	return loom.NewPalette(depth, roles)
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

// TMLMouse gives a mouse event, in the window's content cells, to the
// surface of a TML window. Its side effects run through the wake, since a
// mouse path returns no command.
func (m *OS) TMLMouse(w *terminal.Window, ev uv.MouseEvent) {
	v := m.tmlViewOf(w)
	if v == nil || v.surface == nil {
		return
	}
	mouse := ev.Mouse()
	lm := loom.Mouse{X: mouse.X, Y: mouse.Y}
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
		w.MarkContentDirty()
		m.cachedViewContent = ""
	}
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
			if tmlAsks(a) && !v.confirmed[m.tmlConfirmKey(a)] {
				// The first time a surface types into a pane, or starts a
				// program, the person says yes to it. The action waits for
				// the answer.
				v.confirm = &a
				w.MarkContentDirty()
				m.cachedViewContent = ""
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
	if len(ops) > 0 || closing || len(events) > 0 || len(actions) > 0 {
		cmds = append(cmds, m.tmlInput(v, tmlInputReq{ops: ops, events: events, actions: actions, closing: closing, value: value, hasValue: hasValue}))
	}
	return tea.Batch(cmds...)
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

// tmlInputReq is what one tml-input carries.
type tmlInputReq struct {
	ops      []tml.Op
	events   []tmlEmit
	actions  []tmlPaneAction
	closing  bool
	value    any
	hasValue bool
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
		if err == nil && json.Unmarshal(raw, &res) == nil {
			for _, a := range res.Actions {
				if !a.OK {
					return TMLActionFailedMsg{Text: "The surface could not run " + a.Verb + ": " + a.Error}
				}
			}
		}
		return nil
	}
}
