// Package learn runs tuios as the guided tour at tuios.dev/learn. It
// wraps the real app model, reports what the person did as a stream of
// events a lesson can check steps against, and takes commands from the page
// that set the scene for a step.
//
// It is plain Go with no build tags, so the whole contract is tested natively
// against a real app.OS with the fake shell in its panes. cmd/tuios-wasm only
// carries it across to JavaScript. The contract is documented in
// cmd/tuios-wasm/README.md.
package learn

import (
	"reflect"
	"strconv"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/webshell"
)

// Event types. See cmd/tuios-wasm/README.md for each one's data.
const (
	EventReady           = "ready"
	EventKey             = "key"
	EventAction          = "action"
	EventMode            = "mode"
	EventPrefix          = "prefix"
	EventWindowOpen      = "window.open"
	EventWindowClose     = "window.close"
	EventWindowFocus     = "window.focus"
	EventWindowRename    = "window.rename"
	EventWindowMinimize  = "window.minimize"
	EventWindowZoom      = "window.zoom"
	EventWindowMove      = "window.move"
	EventWindowFloat     = "window.float"
	EventWindowWorkspace = "window.workspace"
	EventSetting         = "setting"
	EventWorkspace       = "workspace"
	EventTiling          = "tiling"
	EventLayout          = "layout"
	EventTheme           = "theme"
	EventOverlayOpen     = "overlay.open"
	EventOverlayClose    = "overlay.close"
	EventNotification    = "notification"
	EventAgent           = "agent"
	EventTapeStart       = "tape.start"
	EventTapeFinish      = "tape.finish"
	EventKeys            = "keys"
	EventPiP             = "pip"
	EventAgentsSelect    = "agents.select"
	EventAgentsTab       = "agents.tab"
	EventThemeEdit       = "themeEdit"
	EventShellStart      = webshell.EventCommandStart
	EventShellCommand    = webshell.EventCommand
	EventShellCwd        = webshell.EventCwd
	EventFiles           = "files"
	EventReviewMove      = "review.move"
	EventReviewNote      = "review.note"
	EventReviewSend      = "review.send"
	EventWorktree        = webshell.EventWorktree
	EventCopyYank        = "copy.yank"
)

// Event is one thing that happened, as the page receives it.
type Event struct {
	Type     string
	WindowID string
	Data     map[string]any
	// State is the snapshot after the event, set on every event that comes
	// from a state change. Nil on key, action, notification and shell events.
	State map[string]any
}

// ToMap is the event as a plain object for the page.
func (e Event) ToMap() map[string]any {
	data := e.Data
	if data == nil {
		data = map[string]any{}
	}
	out := map[string]any{"type": e.Type, "data": data}
	if e.WindowID != "" {
		out["windowId"] = e.WindowID
	}
	if e.State != nil {
		out["state"] = e.State
	}
	return out
}

// Model is the app model with the tour's reporting around it. It is what the
// Bubble Tea program runs in the browser build.
type Model struct {
	*app.OS
	emit func(Event)
	send func(tea.Msg)

	mu   sync.Mutex
	last Snapshot
	// rest is the last snapshot taken with no animation running, which
	// window.move events are measured from.
	rest     Snapshot
	theme    string // the theme the tour started with, for reset
	tapeName string
	// fleet answers the agents view in place of a daemon. See fleet.go.
	fleet *fleet
	// verbs are the daemon verbs the tour fakes. See callVerb.
	verbs map[string]verbFunc
	// looksDue says the Update that just ran could have changed a look, so
	// report reads lookPaths again. A look changes only by a key, the mouse,
	// a command from the page or a tape. Reading them walks the config by
	// reflection, about 10 µs a path natively, which is too much for every
	// Update: the fake shell's output and each animation frame are Updates.
	looksDue bool
	// startLooks is the looks the tour started with. config.toml shows a look
	// once it differs.
	startLooks looks
	// surfaces holds the TML surfaces tuios tml show opens. See
	// surfaces.go.
	surfaces *surfaceHost
}

// New wraps o for the tour. emit receives every event; it may be called from
// any goroutine. send delivers a message to the running program, and is how
// the fake shell's agent reports and tape requests reach the model.
//
// New puts o in Learn mode and installs the fake shell's event sink, so there
// is one tour per process.
func New(o *app.OS, emit func(Event), send func(tea.Msg)) *Model {
	m := &Model{OS: o, emit: emit, send: send, verbs: map[string]verbFunc{}}
	o.LearnMode = true
	o.SetLearnBackend(backend{})
	m.registerReviewVerbs()
	o.OnAction = func(action string) {
		mode := "window"
		if m.OS.Mode == app.TerminalMode {
			mode = "terminal"
		}
		m.emit(Event{Type: EventAction, Data: map[string]any{"name": action, "mode": mode}})
	}
	o.OnNotification = func(message, level string) {
		m.emit(Event{Type: EventNotification, Data: map[string]any{"message": message, "level": level}})
	}
	m.fleet = newFleet(send)
	m.fleet.spawn = func(msg spawnAgentsMsg) { send(msg) }
	m.fleet.close = func(windows []string) { send(closeWindowsMsg{windows: windows}) }
	// One caller answers every verb: the review's from m.verbs, the agents
	// view's from the fleet.
	o.SetInboxVerbCaller(m.callVerb, func() string { return learnNonce })
	o.SetLearnSpawnInfo(spawnInfo)
	webshell.SetEventSink(m.fromGuest)
	m.surfaces = &surfaceHost{m: m}
	o.SetLearnTMLHost(m.surfaces.call)
	webshell.SetSurfaceHost(m.surfaces.request)
	m.last = take(o)
	m.last.Looks = readLooks(o.UserConfig)
	m.rest = m.last
	m.theme = m.last.Theme
	m.startLooks = m.last.Looks
	m.writeConfig(m.last)
	return m
}

// fromGuest routes what the fake shell reports. Agent reports and tape
// requests are for tuios, and go to the program as messages, which is the
// same in-process path set-agent-state takes for a local pane. Everything
// else is for the page.
func (m *Model) fromGuest(e webshell.Event) {
	str := func(k string) string {
		s, _ := e.Data[k].(string)
		return s
	}
	switch e.Type {
	case webshell.EventAgentReport:
		m.send(app.AgentReportMsg{
			WindowID: e.WindowID,
			State:    str("state"),
			Message:  str("message"),
			Kind:     str("kind"),
			Harness:  str("harness"),
		})
	case webshell.EventTapePlay:
		m.send(playTapeMsg{name: str("name"), script: str("script")})
	case webshell.EventAgentChanged:
		m.fleet.changed(e.WindowID)
	case webshell.EventFilesChanged:
		m.send(app.LearnFilesChangedMsg{})
	default:
		m.emit(Event{Type: e.Type, WindowID: e.WindowID, Data: e.Data})
	}
}

// State is the latest snapshot, safe to call from any goroutine.
func (m *Model) State() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

// playTapeMsg asks for a tape from inside the tour, so the tape.start event
// can carry its name.
type playTapeMsg struct{ name, script string }

// CommandMsg is a command from the page, run on the program's goroutine so it
// never races Update. See RunCommand.
type CommandMsg struct {
	Name string
	Args []string
}

// commandDoneMsg follows a command through the app's Update. The command
// changed the model outside Update, and Update is where a change gets its
// frame: it drops the cached view, arms any animation, and asks the renderer
// to write the frame now. The app ignores the message itself.
type commandDoneMsg struct{}

// Init starts the app and tells the page the tour is ready.
func (m *Model) Init() tea.Cmd {
	cmd := tea.Batch(m.OS.Init(), m.OS.LearnInboxFeed(m.fleet.feed))
	m.emit(Event{Type: EventReady, State: m.State().ToMap()})
	return cmd
}

// Update runs the app's Update, reporting the key before it is handled and
// every state change after.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		mode := "window"
		if m.OS.Mode == app.TerminalMode {
			mode = "terminal"
		}
		m.emit(Event{Type: EventKey, Data: map[string]any{"key": k.String(), "mode": mode}})
	}
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg, tea.PasteMsg, CommandMsg:
		m.looksDue = true
	}
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case CommandMsg:
		cmd = m.RunCommand(msg.Name, msg.Args...)
		_, after := m.OS.Update(commandDoneMsg{})
		cmd = tea.Batch(cmd, after)
	case playTapeMsg:
		m.tapeName = msg.name
		_, cmd = m.OS.Update(app.PlayTapeMsg{Name: msg.name, Script: msg.script})
	case spawnAgentsMsg:
		m.spawnAgents(msg)
		_, cmd = m.OS.Update(commandDoneMsg{})
	case closeWindowsMsg:
		for _, id := range msg.windows {
			m.OS.LearnSetFanAttempt(id, app.LearnFanAttempt{})
			_ = m.OS.CloseWindow(id)
		}
		_, cmd = m.OS.Update(commandDoneMsg{})
	case openSurfaceMsg, closeSurfaceWindowMsg:
		cmd, _ = m.surfaces.update(msg)
		// The window opened or closed outside the app's Update, which is
		// where a change gets its frame.
		_, after := m.OS.Update(commandDoneMsg{})
		cmd = tea.Batch(cmd, after)
	default:
		next, c := m.OS.Update(msg)
		if n, ok := next.(*app.OS); ok {
			m.OS = n
		}
		cmd = c
	}
	m.report()
	m.surfaces.sweep()
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseReleaseMsg:
		cmd = m.watchClipboard(cmd)
	}
	return m, cmd
}

// watchClipboard reports a clipboard write in cmd as a copy.yank event. A
// yank in copy mode or the scrollback browser writes the clipboard through
// tea.SetClipboard, whose message type Bubble Tea keeps to itself, so the
// write is known by that type's name once the command has run.
func (m *Model) watchClipboard(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		switch v := msg.(type) {
		case tea.BatchMsg:
			out := make(tea.BatchMsg, len(v))
			for i, c := range v {
				out[i] = m.watchClipboard(c)
			}
			return out
		case nil:
			return nil
		}
		if t := reflect.TypeOf(msg); t.Name() == "setClipboardMsg" && t.PkgPath() == clipboardPkg && t.Kind() == reflect.String {
			m.emit(Event{Type: EventCopyYank, Data: map[string]any{"chars": len([]rune(reflect.ValueOf(msg).String()))}})
		}
		return msg
	}
}

// clipboardPkg is Bubble Tea's import path, where setClipboardMsg lives.
var clipboardPkg = reflect.TypeFor[tea.BatchMsg]().PkgPath()

// report emits one event per change since the last Update.
func (m *Model) report() {
	now := take(m.OS)
	m.mu.Lock()
	prev := m.last
	if m.looksDue || now.Tape || prev.Tape {
		now.Looks = readLooks(m.OS.UserConfig)
		m.looksDue = false
	} else {
		now.Looks = prev.Looks
	}
	m.last = now
	m.mu.Unlock()
	if prev.Theme != now.Theme || prev.Border != now.Border || prev.Glyphs != now.Glyphs || prev.Looks != now.Looks {
		m.writeConfig(now)
	}
	events := diff(prev, now)
	// A move is reported once it has settled, not once per animation frame.
	if !now.Moving {
		events = append(events, moves(m.rest, now)...)
		m.rest = now
	}
	if len(events) == 0 {
		return
	}
	state := now.ToMap()
	for _, e := range events {
		if e.Type == EventTapeStart && m.tapeName != "" {
			e.Data["name"] = m.tapeName
		}
		e.State = state
		m.emit(e)
	}
}

// writeConfig keeps the fake shell's config.toml in step with the looks on
// screen. The real app writes nothing in Learn mode, so without this the file
// would show the defaults after the reader changed them. A look in lookPaths
// is written once it differs from the one the tour started with.
func (m *Model) writeConfig(s Snapshot) {
	var lines []webshell.ConfigLine
	for i, path := range lookPaths {
		if s.Looks[i] == m.startLooks[i] {
			continue
		}
		lines = append(lines, webshell.ConfigLine{Path: path, Value: tomlValue(path, s.Looks[i])})
	}
	webshell.WriteConfig(s.Theme, s.Border, s.Glyphs, lines...)
}

// tomlValue is v as config.toml spells the option at path: a bool or an int
// bare, anything else quoted.
func tomlValue(path, v string) string {
	if opt, ok := config.LookupOption(path); ok && (opt.Type == config.OptionBool || opt.Type == config.OptionInt) && v != "" {
		return v
	}
	return strconv.Quote(v)
}

// Filter is the program's event filter: Learn mode's quit guard, then the
// app's own mouse motion filter, unwrapped from the tour's model.
func Filter(model tea.Model, msg tea.Msg) tea.Msg {
	m, ok := model.(*Model)
	if !ok {
		return msg
	}
	msg = app.FilterLearnMode(m.OS, msg)
	return app.FilterMouseMotion(m.OS, msg)
}
