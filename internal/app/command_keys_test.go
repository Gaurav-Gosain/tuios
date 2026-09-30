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
