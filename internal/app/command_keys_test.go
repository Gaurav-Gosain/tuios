package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// commandOS is scratchOS with [[keybindings.command]] entries.
func commandOS(t *testing.T, daemon bool, entries ...config.CommandBinding) *OS {
	t.Helper()
	m := scratchOS(t, daemon)
	m.UserConfig.Keybindings.Command = entries
	return m
}

// commandArgv runs the line with sh -c, with the variables set through env.
func TestCommandArgvRunsShWithTheVariables(t *testing.T) {
	argv := commandArgv("git log | head", map[string]string{"TUIOS_SESSION": "work", "TUIOS_ACTIVE_PANE_ID": "p1"})
	want := []string{"env", "TUIOS_ACTIVE_PANE_ID=p1", "TUIOS_SESSION=work", "sh", "-c", "git log | head"}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv = %q, want %q", argv, want)
	}
	if commandArgv("  ", nil) != nil {
		t.Fatal("an empty line is not the user's shell")
	}
}

// Each scratch entry keeps its own pane, and only one is shown at a time.
func TestScratchEntriesKeepTheirOwnPanes(t *testing.T) {
	m := commandOS(t, false,
		config.CommandBinding{Key: "prefix+alt+y", Type: "scratch", Command: "sh", Name: "one"},
		config.CommandBinding{Key: "prefix+alt+u", Type: "scratch", Command: "sh", Name: "two"})
	one := &terminal.Window{ID: "one", IsPopup: true, IsScratch: true, IsFloating: true, ScratchName: "one", Workspace: 1, Minimized: true}
	two := &terminal.Window{ID: "two", IsPopup: true, IsScratch: true, IsFloating: true, ScratchName: "two", Workspace: 1, Minimized: true}
	m.Windows = append(m.Windows, one, two)

	m.RunCommandBinding("command:one")
	if one.Minimized || !two.Minimized || m.GetFocusedWindow() != one {
		t.Fatalf("after one: one hidden=%v two hidden=%v", one.Minimized, two.Minimized)
	}
	m.RunCommandBinding("command:two")
	if !one.Minimized || two.Minimized || m.GetFocusedWindow() != two {
		t.Fatalf("after two: one hidden=%v two hidden=%v", one.Minimized, two.Minimized)
	}
	m.RunCommandBinding("command:two")
	if !two.Minimized || m.FocusedWindow != 0 {
		t.Fatal("the second press of two did not hide it")
	}
	if len(m.Windows) != 3 {
		t.Fatalf("windows = %d, want 3: no pane is made or closed", len(m.Windows))
	}
	// The built-in scratch terminal is a third, separate pane.
	if m.scratchIndex() >= 0 {
		t.Fatal("an entry's pane counts as the built-in scratch terminal")
	}
}

// A scratch entry with no pane asks the daemon for one under its name, with
// its command.
func TestScratchEntryCreatesItsPane(t *testing.T) {
	m := commandOS(t, true, config.CommandBinding{Key: "prefix+alt+y", Type: "scratch", Command: "lazygit", Width: "60%"})
	var got []scratchRequest
	prev := scratchOpener
	scratchOpener = func(r scratchRequest) error { got = append(got, r); return nil }
	t.Cleanup(func() { scratchOpener = prev })

	cmd := m.RunCommandBinding("command:lazygit")
	if cmd == nil {
		t.Fatal("no create")
	}
	cmd()
	if len(got) != 1 || got[0].Name != "lazygit" || got[0].Width != "60%" || got[0].Command[len(got[0].Command)-1] != "lazygit" {
		t.Fatalf("request = %+v", got)
	}
}

// A popup entry opens a popup through the daemon, with sh -c and the folder.
func TestPopupEntryOpensAPopup(t *testing.T) {
	m := commandOS(t, true, config.CommandBinding{Key: "alt+t", Command: "htop", Description: "Top"})
	var got []commandPopupRequest
	prev := commandPopupOpener
	commandPopupOpener = func(r commandPopupRequest) error { got = append(got, r); return nil }
	t.Cleanup(func() { commandPopupOpener = prev })

	cmd := m.RunCommandBinding("command:top")
	if cmd == nil {
		t.Fatal("no popup")
	}
	cmd()
	if len(got) != 1 || got[0].Title != "Top" || !slices.Contains(got[0].Command, "htop") || !slices.Contains(got[0].Command, "sh") {
		t.Fatalf("request = %+v", got)
	}
	if !slices.ContainsFunc(got[0].Command, func(s string) bool { return strings.HasPrefix(s, "TUIOS_ACTIVE_PANE_ID=") }) {
		t.Fatalf("the popup has no TUIOS_ACTIVE_PANE_ID: %q", got[0].Command)
	}
}

// A shell entry runs with no window. A failure goes to the dock.
func TestShellEntryRunsAndReportsAFailure(t *testing.T) {
	dir := t.TempDir()
	m := commandOS(t, false,
		config.CommandBinding{Key: "prefix+alt+s", Type: "shell", Command: "echo \"$TUIOS_SESSION\" > " + filepath.Join(dir, "out"), Name: "write"},
		config.CommandBinding{Key: "prefix+alt+f", Type: "shell", Command: "exit 3", Name: "fail"})
	before := len(m.Windows)
	msg := m.RunCommandBinding("command:write")().(CommandRanMsg)
	if msg.Err != nil {
		t.Fatalf("write failed: %v", msg.Err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out"))
	if err != nil || strings.TrimSpace(string(data)) != "work" {
		t.Fatalf("file = %q, %v", data, err)
	}
	if len(m.Windows) != before {
		t.Fatal("a shell entry opened a window")
	}
	fail := m.RunCommandBinding("command:fail")().(CommandRanMsg)
	m.handleCommandRan(fail)
	if n := len(m.Notifications); n == 0 || !strings.Contains(m.Notifications[n-1].Message, "failed") {
		t.Fatalf("notifications = %+v", m.Notifications)
	}
}

// Popup and scratch entries refuse in a session on another machine. A shell
// entry still runs, on this machine.
func TestCommandEntriesOnAnotherMachine(t *testing.T) {
	m := commandOS(t, true,
		config.CommandBinding{Key: "alt+t", Command: "htop", Name: "top"},
		config.CommandBinding{Key: "alt+y", Type: "scratch", Name: "notes"})
	m.AttachedHost = "build"
	if m.RunCommandBinding("command:top") != nil || m.RunCommandBinding("command:notes") != nil {
		t.Fatal("a popup or scratch entry ran in a session on another machine")
	}
	if n := len(m.Notifications); n < 2 {
		t.Fatalf("notifications = %+v, want two refusals", m.Notifications)
	}
}

// The palette lists each entry by its description.
func TestCommandEntriesInThePalette(t *testing.T) {
	m := commandOS(t, false, config.CommandBinding{Key: "prefix+alt+g", Type: "scratch", Command: "lazygit", Description: "Lazygit"})
	m.rebuildPaletteItems()
	it := paletteItemNamed(m.PaletteItems, "Lazygit")
	if it.Category != paletteCategoryCommands || it.Shortcut != "prefix+alt+g" || it.Action == nil {
		t.Fatalf("palette row = %+v", it)
	}
}

// A scratch pane that arrives from the daemon hides the scratch pane on the
// screen, even when the push already focused it.
func TestArrivingScratchHidesTheShownOne(t *testing.T) {
	m := commandOS(t, false)
	m.Mode = TerminalMode
	one := &terminal.Window{ID: "one", IsPopup: true, IsScratch: true, IsFloating: true, ScratchName: "one", Workspace: 1}
	two := &terminal.Window{ID: "two", IsPopup: true, IsScratch: true, IsFloating: true, ScratchName: "two", Workspace: 1}
	m.Windows = append(m.Windows, one, two)
	m.FocusedWindow = 2
	m.scratchPending, m.scratchPendingAt = "two", time.Now()
	m.maybeFocusScratch()
	if !one.Minimized || two.Minimized {
		t.Fatalf("one hidden=%v two hidden=%v, want only two shown", one.Minimized, two.Minimized)
	}
}

// A shell entry that puts a child in the background returns when the
// command does, not when the child does.
func TestShellEntryDoesNotWaitForABackgroundChild(t *testing.T) {
	start := time.Now()
	if err := runCommandShell(commandArgv("sleep 3 &", nil), ""); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("the entry took %v: it waited for the background child", d)
	}
}

// A reload closes a scratch pane whose entry is gone. The built-in scratch
// terminal and a pane whose entry is still there stay.
func TestReloadClosesOrphanScratches(t *testing.T) {
	m := commandOS(t, false, config.CommandBinding{Key: "alt+y", Type: "scratch", Name: "kept"})
	for _, name := range []string{"", "kept", "gone"} {
		m.Windows = append(m.Windows, &terminal.Window{ID: "s-" + name, IsPopup: true, IsScratch: true, IsFloating: true, ScratchName: name, Workspace: 1, Minimized: true})
	}
	m.pruneOrphanScratches()
	var left []string
	for _, w := range m.Windows {
		left = append(left, w.ID)
	}
	if slices.Contains(left, "s-gone") || !slices.Contains(left, "s-") || !slices.Contains(left, "s-kept") {
		t.Fatalf("windows = %v, want s-gone closed and the others kept", left)
	}
	if n := len(m.Notifications); n == 0 || !strings.Contains(m.Notifications[n-1].Message, "no entry") {
		t.Fatalf("notifications = %+v", m.Notifications)
	}
}

// A scratch command that exits at once is reported with its code, and the
// create ends, so the next press may try again.
func TestScratchThatStopsAtOnceIsReported(t *testing.T) {
	m := commandOS(t, true)
	m.scratchPending, m.scratchPendingAt = "lazygit", time.Now()
	m.handleScratchOpened(ScratchOpenedMsg{Label: "Lazygit", Err: ScratchStoppedError{Code: 127}})
	if m.scratchPending != "" {
		t.Fatal("the create stayed on its way")
	}
	if n := len(m.Notifications); n == 0 || m.Notifications[n-1].Message != "The command Lazygit stopped with exit code 127." {
		t.Fatalf("notifications = %+v", m.Notifications)
	}
}

// A local scratch pane that exits at once is reported. One that ran a while
// is not.
func TestLocalScratchThatStopsAtOnceIsReported(t *testing.T) {
	m := commandOS(t, false)
	w := &terminal.Window{ID: "fast", IsPopup: true, IsScratch: true, ScratchName: "x", CustomName: "X"}
	slow := &terminal.Window{ID: "slow", IsPopup: true, IsScratch: true, ScratchName: "y", CustomName: "Y"}
	m.scratchStarted = map[string]time.Time{"fast": time.Now(), "slow": time.Now().Add(-time.Minute)}
	m.noteLocalScratchExit(slow)
	if len(m.Notifications) != 0 {
		t.Fatal("a scratch that ran a minute was reported")
	}
	m.noteLocalScratchExit(w)
	if n := len(m.Notifications); n == 0 || !strings.Contains(m.Notifications[n-1].Message, "X stopped") {
		t.Fatalf("notifications = %+v", m.Notifications)
	}
}

// In a session on another machine a pane entry gets no socket of this
// machine and starts in the focused pane's folder there.
func TestRemotePaneEntryEnv(t *testing.T) {
	m := commandOS(t, true)
	m.AttachedHost = "build"
	m.Windows[0].Cwd = "file://build/srv/app"
	if got := m.remotePaneDir(); got != "/srv/app" {
		t.Fatalf("dir = %q, want /srv/app", got)
	}
	env := m.commandEnv(m.remotePaneDir())
	if _, ok := env["TUIOS_SOCKET"]; ok {
		t.Fatal("a remote pane got this machine's socket")
	}
	if env["TUIOS_ACTIVE_PANE_CWD"] != "/srv/app" {
		t.Fatalf("env = %v", env)
	}
}

// On Windows the variables are set with set, before the command.
func TestCommandArgvOnWindowsSetsTheVariables(t *testing.T) {
	argv := commandArgvFor("windows", "dir", map[string]string{"TUIOS_SESSION": "work"})
	if len(argv) != 3 || argv[0] != "cmd" || argv[2] != `set "TUIOS_SESSION=work" && dir` {
		t.Fatalf("argv = %q", argv)
	}
}

// The palette shows an entry's key as the built-ins show theirs, and no key
// for an entry whose key another action took.
func TestPaletteShortcutOfAnEntry(t *testing.T) {
	m := commandOS(t, false,
		config.CommandBinding{Key: "PREFIX+Alt+G", Command: "lazygit", Name: "live"},
		config.CommandBinding{Key: "prefix+g", Command: "htop", Name: "dead"})
	m.KeybindRegistry = config.NewKeybindRegistry(m.UserConfig)
	m.rebuildPaletteItems()
	if got := paletteItemNamed(m.PaletteItems, "Run lazygit").Shortcut; got != "prefix+alt+g" {
		t.Errorf("live shortcut = %q, want prefix+alt+g", got)
	}
	if got := paletteItemNamed(m.PaletteItems, "Run htop").Shortcut; got != "" {
		t.Errorf("dead shortcut = %q, want none", got)
	}
}

// A press after a stop report starts the command again, even while this
// client still holds the stopped pane: the push that removes it can come
// after the report. The press used to find the dead pane and show or hide
// it, and the person got neither the popup nor a report.
func TestPressAfterAStopReportCreatesAgain(t *testing.T) {
	m := commandOS(t, true, config.CommandBinding{Key: "prefix+alt+y", Type: "scratch", Command: "exit 7", Name: "broken"})
	var asked int
	prev := scratchOpener
	scratchOpener = func(scratchRequest) error { asked++; return nil }
	t.Cleanup(func() { scratchOpener = prev })

	// The stopped pane arrived and is still held, shown.
	m.Windows = append(m.Windows, &terminal.Window{ID: "dead", IsPopup: true, IsScratch: true, IsFloating: true, ScratchName: "broken", Workspace: 1})
	m.handleScratchOpened(ScratchOpenedMsg{Label: "broken", Err: ScratchStoppedError{Code: 7, WindowID: "dead"}})

	cmd := m.RunCommandBinding("command:broken")
	if cmd == nil {
		t.Fatal("the press after the report did not ask for the pane")
	}
	cmd()
	if asked != 1 {
		t.Fatalf("asked %d times, want 1", asked)
	}
	// Once the push drops the pane, the note of it goes too.
	m.Windows = m.Windows[:1]
	m.scratchIndexNamed("broken")
	if len(m.deadScratch) != 0 {
		t.Fatalf("deadScratch = %v, want empty once the pane is gone", m.deadScratch)
	}
}
