// Package learn runs tuios as the guided tour at tuios.gaurav.zip/learn. It
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
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/webshell"
)

// Event types. See cmd/tuios-wasm/README.md for each one's data.
const (
	EventReady          = "ready"
	EventKey            = "key"
	EventAction         = "action"
	EventMode           = "mode"
	EventPrefix         = "prefix"
	EventWindowOpen     = "window.open"
	EventWindowClose    = "window.close"
	EventWindowFocus    = "window.focus"
	EventWindowRename   = "window.rename"
	EventWindowMinimize = "window.minimize"
	EventWindowZoom     = "window.zoom"
	EventWorkspace      = "workspace"
	EventTiling         = "tiling"
	EventLayout         = "layout"
	EventTheme          = "theme"
	EventOverlayOpen    = "overlay.open"
	EventOverlayClose   = "overlay.close"
	EventNotification   = "notification"
	EventAgent          = "agent"
	EventTapeStart      = "tape.start"
	EventTapeFinish     = "tape.finish"
	EventShellStart     = webshell.EventCommandStart
	EventShellCommand   = webshell.EventCommand
	EventShellCwd       = webshell.EventCwd
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

	mu       sync.Mutex
	last     Snapshot
	theme    string // the theme the tour started with, for reset
	tapeName string
}

// New wraps o for the tour. emit receives every event; it may be called from
// any goroutine. send delivers a message to the running program, and is how
// the fake shell's agent reports and tape requests reach the model.
//
// New puts o in Learn mode and installs the fake shell's event sink, so there
// is one tour per process.
func New(o *app.OS, emit func(Event), send func(tea.Msg)) *Model {
	m := &Model{OS: o, emit: emit, send: send}
	o.LearnMode = true
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
	webshell.SetEventSink(m.fromGuest)
	m.last = take(o)
	m.theme = m.last.Theme
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

// Init starts the app and tells the page the tour is ready.
func (m *Model) Init() tea.Cmd {
	cmd := m.OS.Init()
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
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case CommandMsg:
		cmd = m.RunCommand(msg.Name, msg.Args...)
	case playTapeMsg:
		m.tapeName = msg.name
		_, cmd = m.OS.Update(app.PlayTapeMsg{Name: msg.name, Script: msg.script})
	default:
		next, c := m.OS.Update(msg)
		if n, ok := next.(*app.OS); ok {
			m.OS = n
		}
		cmd = c
	}
	m.report()
	return m, cmd
}

// report emits one event per change since the last Update.
func (m *Model) report() {
	now := take(m.OS)
	m.mu.Lock()
	prev := m.last
	m.last = now
	m.mu.Unlock()
	events := diff(prev, now)
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
