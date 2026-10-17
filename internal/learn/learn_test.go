package learn

import (
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/webshell"
	"github.com/charmbracelet/x/xpty"
)

func TestMain(m *testing.M) {
	// The browser build's setup: every pane is the fake shell on an
	// in-memory pty, and keys go through the real input handler.
	ptyspawn.NewGuestPty = func(w, h int) (xpty.Pty, error) { return webshell.NewPty(w, h), nil }
	app.SetInputHandler(input.HandleInput)
	// The tour loads the real config, so it must not read the developer's.
	os.Exit(testutil.RunIsolated(m))
}

// tour is a Model driven the way the program drives it, with the events it
// emits and the messages it sends back recorded.
type tour struct {
	t  *testing.T
	m  *Model
	mu sync.Mutex
	ev []Event
	// seen is how many events earlier waits have consumed.
	seen int
	msgs chan tea.Msg
}

func newTour(t *testing.T) *tour {
	t.Helper()
	cfg := Config()
	seed := config.AppearanceFrom(cfg, config.Overrides{})
	seed.Motion = config.MotionNone
	o := app.NewOS(app.OSOptions{
		KeybindRegistry: config.NewKeybindRegistry(cfg),
		UserConfig:      cfg,
		Settings:        &seed,
		Width:           120,
		Height:          40,
		Caps:            &app.HostCapabilities{TrueColor: true, TerminalName: "tuios-wasm"},
	})
	tr := &tour{t: t, msgs: make(chan tea.Msg, 64)}
	tr.m = New(o, func(e Event) {
		tr.mu.Lock()
		tr.ev = append(tr.ev, e)
		tr.mu.Unlock()
	}, func(msg tea.Msg) { tr.msgs <- msg })
	t.Cleanup(func() {
		webshell.SetEventSink(nil)
		for _, w := range tr.m.OS.Windows {
			w.Close()
		}
	})
	tr.m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return tr
}

// update runs one message through the model, and runs the command it returns
// in the background the way the program would, feeding the result back
// through the program's filter on a later pump.
func (tr *tour) update(msg tea.Msg) tea.Cmd {
	_, cmd := tr.m.Update(msg)
	tr.run(cmd)
	return cmd
}

func (tr *tour) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				tr.run(c)
			}
			return
		}
		if msg == nil {
			return
		}
		select {
		case tr.msgs <- msg:
		default:
		}
	}()
}

// pump delivers what the background commands and the fake shell sent.
func (tr *tour) pump() {
	for {
		select {
		case msg := <-tr.msgs:
			if msg = Filter(tr.m, msg); msg != nil {
				tr.update(msg)
			}
		default:
			return
		}
	}
}

func (tr *tour) press(keys ...tea.KeyPressMsg) {
	for _, k := range keys {
		tr.update(k)
	}
}

// typeText presses one key per character, the way a person types into a pane.
func (tr *tour) typeText(s string) {
	for _, r := range s {
		if r == '\r' {
			tr.press(tea.KeyPressMsg{Code: tea.KeyEnter})
			continue
		}
		tr.press(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "ctrl+b":
		return tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(s)
	return tea.KeyPressMsg{Code: r[0], Text: s}
}

// waitFor returns the first event after the last one waited for that matches,
// pumping the messages the fake shell sends into the model meanwhile, the way
// the program would.
func (tr *tour) waitFor(typ string, match func(Event) bool) Event {
	tr.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tr.pump()
		// Output from the panes reaches the model as it would in the program.
		tr.update(app.PTYDataMsg{})
		tr.mu.Lock()
		for i := tr.seen; i < len(tr.ev); i++ {
			e := tr.ev[i]
			if e.Type == typ && (match == nil || match(e)) {
				tr.seen = i + 1
				tr.mu.Unlock()
				return e
			}
		}
		tr.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	var types []string
	for _, e := range tr.ev[tr.seen:] {
		types = append(types, e.Type)
	}
	tr.t.Fatalf("no %s event; saw %v", typ, types)
	return Event{}
}

func data(e Event, k string) any { return e.Data[k] }

// TestEventContract drives the real OS through the steps a lesson checks and
// asserts the event each one produces.
func TestEventContract(t *testing.T) {
	tr := newTour(t)
	tr.update(nil)

	// A window.
	tr.press(key("n"))
	tr.waitFor(EventAction, func(e Event) bool { return data(e, "name") == "new_window" })
	open := tr.waitFor(EventWindowOpen, nil)
	first := open.WindowID
	if first == "" || open.State["totalWindows"] != 1 {
		t.Fatalf("window.open = %+v", open)
	}

	// Typing mode, and a command in the fake shell with its exit code.
	tr.press(key("i"))
	tr.waitFor(EventMode, func(e Event) bool { return data(e, "to") == "terminal" })
	tr.typeText("ls\r")
	done := tr.waitFor(EventShellCommand, func(e Event) bool { return data(e, "command") == "ls" })
	if done.Data["exitCode"] != 0 || done.WindowID != first {
		t.Errorf("shell.command = %+v", done)
	}
	tr.typeText("nope\r")
	done = tr.waitFor(EventShellCommand, func(e Event) bool { return data(e, "command") == "nope" })
	if done.Data["exitCode"] != 127 {
		t.Errorf("unknown command exit = %v", done.Data["exitCode"])
	}

	// The prefix, then a second window through it.
	tr.press(key("ctrl+b"))
	tr.waitFor(EventPrefix, func(e Event) bool { return data(e, "to") == "prefix" })
	tr.press(key("c"))
	tr.waitFor(EventAction, func(e Event) bool { return data(e, "name") == "prefix_new_window" })
	second := tr.waitFor(EventWindowOpen, nil).WindowID
	tr.waitFor(EventWindowFocus, func(e Event) bool { return data(e, "to") == second })

	// Tiling off and on.
	tr.press(key("ctrl+b"), key("space"))
	tr.waitFor(EventTiling, func(e Event) bool { return data(e, "to") == false })
	tr.press(key("ctrl+b"), key("space"))
	tr.waitFor(EventTiling, func(e Event) bool { return data(e, "to") == true })

	// The layout mode.
	tr.m.RunCommand("layout", "master-stack")
	tr.update(nil)
	tr.waitFor(EventLayout, func(e Event) bool { return data(e, "to") == "master-stack" })

	// Help opens and closes.
	tr.press(key("ctrl+b"), key("?"))
	tr.waitFor(EventOverlayOpen, func(e Event) bool { return data(e, "name") == OverlayHelp })
	tr.press(key("esc"))
	tr.waitFor(EventOverlayClose, func(e Event) bool { return data(e, "name") == OverlayHelp })

	// Back to window mode for the plain keys.
	tr.press(key("esc"))
	if tr.m.OS.Mode != app.WindowManagementMode {
		tr.press(key("ctrl+b"), key("esc"))
	}
	tr.waitFor(EventMode, func(e Event) bool { return data(e, "to") == "window" })

	// Zoom and minimize, through the dispatcher as a key would.
	tr.m.RunCommand("action", "toggle_zoom")
	tr.update(nil)
	tr.waitFor(EventAction, func(e Event) bool { return data(e, "name") == "toggle_zoom" })
	tr.waitFor(EventWindowZoom, func(e Event) bool { return data(e, "zoomed") == true })
	tr.m.RunCommand("action", "toggle_zoom")
	tr.update(nil)
	tr.waitFor(EventWindowZoom, func(e Event) bool { return data(e, "zoomed") == false })
	tr.m.RunCommand("action", "minimize_window")
	tr.update(nil)
	tr.waitFor(EventWindowMinimize, func(e Event) bool { return data(e, "minimized") == true })

	// Rename.
	_ = tr.m.OS.RenameWindowByID(first, "logs")
	tr.update(nil)
	tr.waitFor(EventWindowRename, func(e Event) bool { return data(e, "to") == "logs" })

	// A workspace, through its prefix.
	tr.press(key("ctrl+b"), key("w"))
	tr.waitFor(EventPrefix, func(e Event) bool { return data(e, "to") == "workspace" })
	tr.press(key("2"))
	tr.waitFor(EventWorkspace, func(e Event) bool { return data(e, "to") == 2 })

	// The theme.
	tr.m.RunCommand("theme", "dracula")
	tr.update(nil)
	tr.waitFor(EventTheme, func(e Event) bool { return data(e, "to") == "dracula" })

	// A notification.
	tr.m.RunCommand("notify", "hello from the page")
	tr.update(nil)
	tr.waitFor(EventNotification, func(e Event) bool { return data(e, "message") == "hello from the page" })

	// Closing a window.
	tr.m.RunCommand("workspace", "1")
	tr.m.RunCommand("closeWindow", second)
	tr.update(nil)
	tr.waitFor(EventWindowClose, func(e Event) bool { return e.WindowID == second })

	// The command palette, and the key that opened it.
	tr.m.RunCommand("action", "command_palette")
	tr.update(nil)
	tr.waitFor(EventOverlayOpen, func(e Event) bool { return data(e, "name") == OverlayCommandPalette })
}

func TestCommandsListMatchesRunCommand(t *testing.T) {
	src, err := os.ReadFile("commands.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range Commands {
		if !strings.Contains(string(src), `case "`+name+`"`) {
			t.Errorf("Commands lists %q but RunCommand has no case for it", name)
		}
	}
	readme, err := os.ReadFile("../../cmd/tuios-wasm/README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range Commands {
		if !strings.Contains(string(readme), "`"+name+"`") {
			t.Errorf("README does not document the %q command", name)
		}
	}
	for _, typ := range []string{
		EventReady, EventKey, EventAction, EventMode, EventPrefix, EventWindowOpen, EventWindowClose,
		EventWindowFocus, EventWindowRename, EventWindowMinimize, EventWindowZoom, EventWorkspace,
		EventTiling, EventLayout, EventTheme, EventOverlayOpen, EventOverlayClose, EventNotification,
		EventAgent, EventTapeStart, EventTapeFinish, EventShellStart, EventShellCommand, EventShellCwd,
		EventWindowMove, EventWindowFloat, EventSetting,
	} {
		if !strings.Contains(string(readme), "`"+typ+"`") {
			t.Errorf("README does not document the %q event", typ)
		}
	}
}

// TestSetRefusesHostOptions is the boundary between the page and the host:
// the page's set command must not reach an option that runs a program, names
// a file, or writes the host clipboard, and an unknown path must change
// nothing.
func TestSetRefusesHostOptions(t *testing.T) {
	tr := newTour(t)
	o := tr.m.OS
	snapshot := func() map[string]string {
		vals := map[string]string{}
		for _, opt := range config.Options() {
			vals[opt.Path], _ = config.GetOptionValue(o.UserConfig, opt.Path)
		}
		return vals
	}

	before := snapshot()
	beforeCopy, beforeOpener := o.Settings.CopyCommand, o.Settings.LinkOpener
	for _, path := range []string{
		"appearance.selection.copy_command", "selection.copy_command",
		"appearance.link_opener", "link_opener",
		"notifications.agent.command", "tape.autorun", "launcher.gui_command",
		"screenshot.directory", "appearance.sidebar.editor", "appearance.sidebar.file_delete",
		"appearance.preferred_shell", "preferred_shell", "daemon.window_size",
		"no.such.option", "",
	} {
		tr.m.RunCommand("set", path, "touch /tmp/tuios-pwned")
	}
	tr.m.RunCommand("set", "appearance.selection.osc52_write", "on")
	tr.m.RunCommand("set", "tape.autorun", "auto")
	if o.Settings.CopyCommand != beforeCopy || o.Settings.LinkOpener != beforeOpener {
		t.Errorf("a refused path changed the settings: copy_command %q, link_opener %q",
			o.Settings.CopyCommand, o.Settings.LinkOpener)
	}
	for path, val := range snapshot() {
		if before[path] != val {
			t.Errorf("a refused path changed %s from %q to %q", path, before[path], val)
		}
	}

	// The options the docs page uses still go through, by both spellings.
	tr.m.RunCommand("set", "appearance.border_style", "double")
	tr.m.RunCommand("set", "glyphs", "ascii")
	after := snapshot()
	if after["appearance.border_style"] != "double" || after["appearance.glyphs"] != "ascii" {
		t.Errorf("set border_style/glyphs = %q/%q, want double/ascii",
			after["appearance.border_style"], after["appearance.glyphs"])
	}

	// A later set must not put back an older theme: set re-applies the
	// tab's config, which has to hold the theme the page chose last.
	o.UserConfig.Appearance.Theme = "gruvbox_dark"
	tr.m.RunCommand("theme", "dracula")
	tr.m.RunCommand("set", "appearance.gap", "1")
	if got := theme.CurrentThemeID(); got != "dracula" {
		t.Errorf("theme after theme dracula and set gap = %q, want dracula", got)
	}

	// themeJSON takes a look's theme through the tuios look apply check. A
	// theme that fails it changes nothing.
	for _, bad := range [][2]string{
		{"../evil", `{"base":"nord"}`},
		{"x", `{"base":"no-such-theme"}`},
		{"x", `{"display_name":"a\u001bb","base":"nord"}`},
		{"x", `not json`},
		{"x", `{"base":"nord","chrome":{"accent":7}}`},
	} {
		tr.m.RunCommand("themeJSON", bad[0], bad[1])
		if got := theme.CurrentThemeID(); got != "dracula" {
			t.Errorf("themeJSON %q %s set the theme %q", bad[0], bad[1], got)
		}
	}
	tr.m.RunCommand("themeJSON", "amber", `{"base":"nord","bg":"#120b02","chrome":{"border":{"unfocused":"#5c3b00"}}}`)
	if got := theme.CurrentThemeID(); got != "look-amber" {
		t.Errorf("theme after themeJSON amber = %q, want look-amber", got)
	}

	// An option added later that names a program or a path has to be refused
	// on purpose, or refused by config.ReachesHost.
	for _, opt := range config.Options() {
		if _, ok := pageSettable(opt.Path); !ok {
			continue
		}
		for _, word := range []string{"command", "opener", "editor", "shell", "directory", "_file", "osc52", "url", "root"} {
			if strings.Contains(opt.Path, word) {
				t.Errorf("the page may set %s; refuse it in config.ReachesHost or say here why it is safe", opt.Path)
			}
		}
	}
}

// TestSetLookItems holds the page's set command to what a look file may hold:
// a dock list or a rail layout with an item a look leaves out, such as a
// custom dock cell, changes nothing. A list of built-ins and the startup
// layout keys go through, so the gallery's Try it shows them.
func TestSetLookItems(t *testing.T) {
	tr := newTour(t)
	o := tr.m.OS
	right := o.UserConfig.Dock.DockList("right")
	sections := o.UserConfig.Appearance.Sidebar.Sections

	tr.m.RunCommand("set", "dock.right", `["custom/x","clock"]`)
	tr.m.RunCommand("set", "dock.right", `["made-up"]`)
	tr.m.RunCommand("set", "dock.custom.x.command", "touch /tmp/tuios-pwned")
	tr.m.RunCommand("set", "appearance.sidebar.sections", "files,custom")
	tr.m.RunCommand("set", "startup.daemon", "false")
	if got := o.UserConfig.Dock.DockList("right"); !slices.Equal(got, right) {
		t.Errorf("a refused set changed dock.right to %q", got)
	}
	if len(o.UserConfig.Dock.Custom) != 0 {
		t.Errorf("the page defined a custom dock cell: %v", o.UserConfig.Dock.Custom)
	}
	if got := o.UserConfig.Appearance.Sidebar.Sections; got != sections {
		t.Errorf("a refused set changed the rail layout to %q", got)
	}

	tr.m.RunCommand("set", "dock.left", `["workspaces"]`)
	tr.m.RunCommand("set", "dock.tabs.style", "flat")
	tr.m.RunCommand("set", "dock.tabs.number_glyphs", `["一","二"]`)
	tr.m.RunCommand("set", "appearance.sidebar.sections", "sessions,git")
	if got := o.UserConfig.Dock.DockList("left"); !slices.Equal(got, []string{"workspaces"}) {
		t.Errorf("dock.left = %q, want [workspaces]", got)
	}
	if tabs := o.UserConfig.Dock.Tabs; !tabs.Flat() || tabs.Icon(1) != "一" {
		t.Errorf("dock.tabs = %+v", tabs)
	}
	if got := o.UserConfig.Appearance.Sidebar.Sections; got != "sessions,git" {
		t.Errorf("sections = %q, want sessions,git", got)
	}

	tr.m.RunCommand("set", "startup.tiled", "false")
	tr.m.RunCommand("set", "startup.tiled", "true")
	tr.m.RunCommand("set", "startup.layout", "master-stack")
	if !o.AutoTiling || o.LayoutModeName() != "master-stack" {
		t.Errorf("tiling %v, layout %q, want tiled master-stack", o.AutoTiling, o.LayoutModeName())
	}
	// A name tuios look apply refuses does nothing: no layout cycle and no
	// notification.
	before := len(o.Notifications)
	tr.m.RunCommand("set", "startup.layout", "spiral")
	if o.LayoutModeName() != "master-stack" || len(o.Notifications) != before {
		t.Errorf("layout %q, %d notifications, want master-stack and %d", o.LayoutModeName(), len(o.Notifications), before)
	}
}
