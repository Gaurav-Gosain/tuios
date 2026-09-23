package learn

import (
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/xpty"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/Gaurav-Gosain/tuios/internal/webshell"
)

func TestMain(m *testing.M) {
	// The browser build's setup: every pane is the fake shell on an
	// in-memory pty, and keys go through the real input handler.
	ptyspawn.NewGuestPty = func(w, h int) (xpty.Pty, error) { return webshell.NewPty(w, h), nil }
	app.SetInputHandler(input.HandleInput)
	os.Exit(m.Run())
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
	cfg := config.DefaultConfig()
	noNotify := false
	cfg.Notifications.Agent.Notify = &noNotify
	seed := config.AppearanceFrom(cfg, config.Overrides{})
	seed.AnimationsEnabled = false
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

// saw waits until some event at all, waited for or not, matches.
func (tr *tour) saw(typ string, match func(Event) bool) {
	tr.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tr.pump()
		tr.mu.Lock()
		found := slices.ContainsFunc(tr.ev, func(e Event) bool { return e.Type == typ && (match == nil || match(e)) })
		tr.mu.Unlock()
		if found {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	tr.t.Fatalf("never saw a matching %s event", typ)
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

// TestLearnModeNeverQuits covers every way out: the quit key, the quit menu,
// Ctrl+C at the last resort, and a QuitMsg from anywhere. Each shows a note,
// and the panes stay open.
func TestLearnModeNeverQuits(t *testing.T) {
	tr := newTour(t)
	tr.press(key("n"))
	tr.waitFor(EventWindowOpen, nil)

	quitNote := func(e Event) bool { return strings.Contains(data(e, "message").(string), "No need to quit") }

	// q in window mode is the quit action.
	tr.press(key("q"))
	tr.waitFor(EventAction, func(e Event) bool { return data(e, "name") == "quit" })
	tr.waitFor(EventNotification, quitNote)

	// Ctrl+C in window mode falls back to quitting, and the program's filter
	// turns the tea.Quit that comes back into the note.
	cmd := tr.update(key("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl+c returned no command; the fallback quit path changed")
	}
	msg := Filter(tr.m, runQuit(cmd))
	if _, isQuit := msg.(tea.QuitMsg); isQuit {
		t.Fatal("the filter let a QuitMsg through in Learn mode")
	}
	tr.update(msg)
	tr.waitFor(EventNotification, quitNote)

	// The quit menu's Quit row.
	tr.m.OS.OpenQuitMenu()
	tr.update(nil)
	tr.waitFor(EventOverlayOpen, func(e Event) bool { return data(e, "name") == OverlayQuitMenu })
	tr.update(Filter(tr.m, runQuit(tr.m.OS.QuitMenuActivate(0))))
	tr.waitFor(EventNotification, quitNote)
	if tr.m.OS.ShowQuitMenu {
		t.Error("the quit menu is still open")
	}

	// An interrupt is a quit too.
	tr.update(Filter(tr.m, tea.InterruptMsg{}))
	tr.waitFor(EventNotification, quitNote)

	if len(tr.m.OS.Windows) != 1 || tr.m.OS.QuitRequested {
		t.Fatalf("quitting tore the session down: %d windows, QuitRequested=%v", len(tr.m.OS.Windows), tr.m.OS.QuitRequested)
	}
	if w := tr.m.OS.Windows[0]; w.Pty == nil {
		t.Fatal("the pane's pty was closed")
	}

	// Outside Learn mode the same filter passes a quit through.
	tr.m.OS.LearnMode = false
	if _, ok := Filter(tr.m, tea.QuitMsg{}).(tea.QuitMsg); !ok {
		t.Error("outside Learn mode the filter swallowed a quit")
	}
	tr.m.OS.LearnMode = true
}

// runQuit runs a command that should produce a quit, and returns the message.
func runQuit(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// TestLearnModeExplainsWhatIsMissing checks that the actions the demo cannot
// do say so, and do nothing else.
func TestLearnModeExplainsWhatIsMissing(t *testing.T) {
	tr := newTour(t)
	for _, action := range []string{"prefix_session_switcher", "new_session", "prefix_detach", "screenshot", "toggle_tape_manager", "tape_prefix_record", "file_delete", "paste_clipboard"} {
		t.Run(action, func(t *testing.T) {
			tr.t = t
			tr.m.RunCommand("action", action)
			tr.update(nil)
			tr.waitFor(EventNotification, func(e Event) bool {
				msg := data(e, "message").(string)
				return strings.Contains(msg, "demo") || strings.Contains(msg, "browser")
			})
		})
	}
	tr.t = t
	if tr.m.OS.ShowSessionSwitcher || tr.m.OS.ShowTapeManager || tr.m.OS.ShowHostPicker {
		t.Error("an unavailable action opened its overlay anyway")
	}
	// The palette reaches the session switcher without the dispatcher.
	tr.m.OS.OpenSessionSwitcher()
	if tr.m.OS.ShowSessionSwitcher {
		t.Error("OpenSessionSwitcher opened in Learn mode")
	}
}

// TestFakeAgentDrivesTheRail runs the fake agent in a pane and checks that its
// reports reach the pane through the in-process path: working, an approval
// with its kind, and done once it is answered by typing y.
func TestFakeAgentDrivesTheRail(t *testing.T) {
	tr := newTour(t)
	tr.press(key("n"))
	id := tr.waitFor(EventWindowOpen, nil).WindowID
	tr.press(key("i"))
	tr.typeText("claude\r")

	tr.waitFor(EventAgent, func(e Event) bool { return data(e, "to") == "working" })
	ask := tr.waitFor(EventAgent, func(e Event) bool { return data(e, "to") == "needs_input" })
	if ask.WindowID != id || data(ask, "kind") != "approval" || data(ask, "harness") != webshell.AgentHarness {
		t.Fatalf("needs_input event = %+v", ask)
	}
	w := tr.m.OS.Windows[0]
	if w.AgentState != "needs_input" || w.AgentKind != "approval" || w.AgentMessage == "" {
		t.Fatalf("the pane did not take the report: state=%q kind=%q msg=%q", w.AgentState, w.AgentKind, w.AgentMessage)
	}

	tr.typeText("y")
	tr.waitFor(EventAgent, func(e Event) bool { return data(e, "to") == "done" })
	if w.AgentState != "done" || w.AgentCompletionSeq != 1 {
		t.Fatalf("after y: state=%q seq=%d", w.AgentState, w.AgentCompletionSeq)
	}
	tr.saw(EventShellCommand, func(e Event) bool { return data(e, "command") == "claude" && data(e, "exitCode") == 0 })
}

// TestTapePlaysFromTheShell plays a tape the way a lesson does, from the fake
// shell, and ticks the program until it finishes.
func TestTapePlaysFromTheShell(t *testing.T) {
	tr := newTour(t)
	tr.press(key("n"))
	tr.waitFor(EventWindowOpen, nil)
	tr.press(key("i"))
	tr.typeText("tuios tape play demo.tape\r")
	start := tr.waitFor(EventTapeStart, nil)
	if data(start, "name") != "demo.tape" {
		t.Errorf("tape.start = %+v", start)
	}

	// The tape plays on the maintenance tick, which update keeps running.
	tr.waitFor(EventTapeFinish, nil)
	if n := len(tr.m.OS.Windows); n < 3 {
		t.Errorf("the tape opened %d windows, want at least 3", n)
	}
	tr.saw(EventShellCommand, func(e Event) bool { return data(e, "command") == "neofetch" })
}

func TestResetPutsTheTourBack(t *testing.T) {
	tr := newTour(t)
	tr.press(key("n"), key("n"))
	tr.waitFor(EventWindowOpen, nil)
	tr.m.RunCommand("workspace", "3")
	tr.m.RunCommand("tiling", "off")
	tr.m.RunCommand("reset")
	tr.update(nil)
	s := tr.m.State()
	if len(s.Windows) != 0 || s.Workspace != 1 || !s.Tiling || s.Mode != "window" || s.Layout != config.LayoutModeBSP {
		t.Fatalf("after reset: %+v", s)
	}
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

// TestCopyModeClosesWhenLeft checks that leaving copy mode closes the
// copyMode overlay. Leaving keeps the pane's CopyMode struct and clears
// Active, so a check on the struct alone never saw it close.
func TestCopyModeClosesWhenLeft(t *testing.T) {
	tr := newTour(t)
	tr.press(key("n"))
	tr.waitFor(EventWindowOpen, nil)
	tr.press(key("ctrl+b"), key("["))
	tr.waitFor(EventOverlayOpen, func(e Event) bool { return data(e, "name") == OverlayCopyMode })
	tr.press(key("esc"))
	tr.waitFor(EventOverlayClose, func(e Event) bool { return data(e, "name") == OverlayCopyMode })
}

// TestWindowMoveReportsLayoutChanges checks that a change that only moves or
// resizes panes, such as growing the master, is an event.
func TestWindowMoveReportsLayoutChanges(t *testing.T) {
	tr := newTour(t)
	tr.press(key("n"), key("n"))
	second := tr.waitFor(EventWindowOpen, func(e Event) bool { return data(e, "count") == 2 }).WindowID
	tr.m.RunCommand("layout", "master-stack")
	tr.update(nil)
	tr.waitFor(EventLayout, nil)

	tr.press(key(">"))
	mv := tr.waitFor(EventWindowMove, nil)
	from, _ := mv.Data["from"].(map[string]any)
	if from == nil || mv.Data["width"] == from["width"] || mv.State == nil {
		t.Fatalf("window.move = %+v", mv)
	}

	// Floating a pane is its own event.
	tr.m.RunCommand("action", "toggle_zoom")
	tr.update(nil)
	tr.m.RunCommand("action", "toggle_zoom")
	tr.update(nil)
	tr.m.OS.ToggleFloating()
	tr.update(nil)
	fl := tr.waitFor(EventWindowFloat, nil)
	if fl.WindowID != second || data(fl, "floating") != true {
		t.Fatalf("window.float = %+v", fl)
	}
}

// TestLooksAreEvents covers the settings a lesson about looks checks: the
// border style, the glyph set and the screen saver.
func TestLooksAreEvents(t *testing.T) {
	tr := newTour(t)
	tr.press(key("n"))
	tr.waitFor(EventWindowOpen, nil)

	tr.m.OS.Settings.BorderStyle = "double"
	tr.update(nil)
	ev := tr.waitFor(EventSetting, func(e Event) bool { return data(e, "name") == SettingBorderStyle })
	if data(ev, "to") != "double" || ev.State["borderStyle"] != "double" {
		t.Fatalf("setting = %+v", ev)
	}
	tr.m.OS.Settings.GlyphSet = "ascii"
	tr.update(nil)
	tr.waitFor(EventSetting, func(e Event) bool { return data(e, "name") == SettingGlyphs && data(e, "to") == "ascii" })

	tr.press(key("S"))
	tr.waitFor(EventOverlayOpen, func(e Event) bool { return data(e, "name") == OverlayScreensaver })
}
