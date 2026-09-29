package app

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// clipboardWrites runs cmd and every command it batches, and returns the text
// of each host clipboard write among them.
func clipboardWrites(cmd tea.Cmd) []string {
	if cmd == nil {
		return nil
	}
	var out []string
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			out = append(out, clipboardWrites(c)...)
		}
	case nil:
	default:
		if fmt.Sprintf("%T", msg) == "tea.setClipboardMsg" {
			out = append(out, fmt.Sprint(msg))
		}
	}
	return out
}

// osc52Harness builds two panes, the first focused, with OSC 52 wired the way a
// client wires it.
func osc52Harness(t *testing.T, mode string) (*OS, []*terminal.Window) {
	t.Helper()
	s := config.DefaultSettings()
	if mode != "" {
		s.OSC52Write = mode
	}
	m := &OS{
		Settings:        s,
		Mode:            TerminalMode,
		KeybindRegistry: config.NewKeybindRegistry(config.DefaultConfig()),
	}
	m.PendingClipboardSet = make(chan ClipboardSetMsg, 1)
	var wins []*terminal.Window
	for i := range 2 {
		id := fmt.Sprintf("osc52-win-%d", i)
		w := terminal.NewDaemonWindow(id, "t", 0, 0, 40, 10, 0, "pty-"+id, make(chan struct{}, 1), 100)
		t.Cleanup(w.Close)
		m.setupClipboardPassthrough(w)
		wins = append(wins, w)
	}
	m.Windows = wins
	m.FocusedWindow = 0
	return m, wins
}

// paneSetsClipboard has w print an OSC 52 write of text and delivers the
// resulting message to Update, returning the host clipboard writes it made.
func paneSetsClipboard(t *testing.T, m *OS, w *terminal.Window, text string) []string {
	t.Helper()
	w.WriteOutput([]byte("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"))
	msg := ListenForClipboardSet(m.PendingClipboardSet)()
	if msg == nil {
		t.Fatal("the OSC 52 write raised no message")
	}
	// The listener Update re-arms must not block the test.
	ch := m.PendingClipboardSet
	m.PendingClipboardSet = nil
	defer func() { m.PendingClipboardSet = ch }()
	_, cmd := m.Update(msg)
	return clipboardWrites(cmd)
}

// Any output can carry OSC 52, a file an agent prints included. By default only
// the focused pane may set the host clipboard, and the dock says it did.
func TestOSC52FromABackgroundPaneDoesNotReachTheHostClipboard(t *testing.T) {
	m, wins := osc52Harness(t, "")

	if got := paneSetsClipboard(t, m, wins[1], "evil\n"); len(got) != 0 {
		t.Fatalf("a background pane set the host clipboard: %q", got)
	}
	if got := paneSetsClipboard(t, m, wins[0], "yank"); len(got) != 1 || got[0] != "yank" {
		t.Fatalf("the focused pane's write = %q, want one write of %q", got, "yank")
	}
	if msg := lastMessage(m); !strings.Contains(msg, "clipboard") {
		t.Fatalf("the focused pane's write said nothing on the dock: %q", msg)
	}
}

// A write that waits is allowed by activating its message, and only then.
func TestOSC52WriteThatAsksIsAllowedFromItsMessage(t *testing.T) {
	m, wins := osc52Harness(t, config.OSC52WriteAsk)

	if got := paneSetsClipboard(t, m, wins[0], "asked"); len(got) != 0 {
		t.Fatalf("ask mode set the clipboard before the user allowed it: %q", got)
	}
	if !strings.Contains(lastMessage(m), "clipboard") {
		t.Fatalf("the waiting write raised no message: %q", lastMessage(m))
	}
	if cmd := m.ClipboardApprovalCmd(); cmd != nil {
		t.Fatalf("a write went out with no approval: %q", clipboardWrites(cmd))
	}
	if !m.JumpToNotification() {
		t.Fatal("the waiting write's message could not be activated")
	}
	if got := clipboardWrites(m.ClipboardApprovalCmd()); len(got) != 1 || got[0] != "asked" {
		t.Fatalf("approved write = %q, want %q", got, "asked")
	}
	if cmd := m.ClipboardApprovalCmd(); cmd != nil {
		t.Fatal("one approval wrote the clipboard twice")
	}
}

// Off keeps every write inside the pane. The pane still reads its own copy
// back.
func TestOSC52WriteOffKeepsTheTextInThePane(t *testing.T) {
	m, wins := osc52Harness(t, config.OSC52WriteOff)

	if got := paneSetsClipboard(t, m, wins[0], "mine"); len(got) != 0 {
		t.Fatalf("off mode set the host clipboard: %q", got)
	}
	if len(m.Notifications) != 0 && m.Notifications[len(m.Notifications)-1].Target != nil {
		t.Fatalf("off mode offered to allow the write: %q", lastMessage(m))
	}
}

// On is the old behaviour: every pane writes.
func TestOSC52WriteOnLetsEveryPaneWrite(t *testing.T) {
	m, wins := osc52Harness(t, config.OSC52WriteOn)

	if got := paneSetsClipboard(t, m, wins[1], "bg"); len(got) != 1 || got[0] != "bg" {
		t.Fatalf("on mode write = %q, want %q", got, "bg")
	}
}
