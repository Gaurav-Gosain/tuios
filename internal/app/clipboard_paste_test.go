package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func lastMessage(m *OS) string {
	if len(m.Notifications) == 0 {
		return ""
	}
	return m.Notifications[len(m.Notifications)-1].Message
}

// TestBrowserPasteSaysWhyInsteadOfWaiting checks the browser client's paste.
//
// The terminal in a browser tab never answers the OSC 52 read query, and
// bubbletea's ReadClipboard has no deadline, so asking for the clipboard there
// used to wait for ever with nothing on screen. The answer is known in advance,
// so the paste says so at once and names the key that does work.
func TestBrowserPasteSaysWhyInsteadOfWaiting(t *testing.T) {
	m := &OS{Settings: config.DefaultSettings(), BrowserClient: true, RemoteClient: true, Mode: TerminalMode}

	if reason := m.ClipboardReadUnsupportedReason(); reason == "" {
		t.Fatalf("a browser client claims it can read the clipboard")
	}
	if cmd := m.RequestHostPaste(); cmd != nil {
		t.Fatalf("a browser client sent a clipboard query that can never be answered")
	}
	msg := lastMessage(m)
	if msg == "" {
		t.Fatalf("the browser paste did nothing and said nothing")
	}
	if !strings.Contains(msg, "ctrl+v") {
		t.Fatalf("the message does not say what to do instead: %q", msg)
	}
	if m.pastePending {
		t.Fatalf("a browser client is waiting for a reply it will never get")
	}
}

// TestTerminalPasteAsksTheTerminal checks the case that does work: a client in a
// real terminal queries, and the query is armed with a deadline.
func TestTerminalPasteAsksTheTerminal(t *testing.T) {
	for _, m := range []*OS{
		{Mode: TerminalMode},
		{Mode: TerminalMode, RemoteClient: true, IsSSHMode: true},
	} {
		if reason := m.ClipboardReadUnsupportedReason(); reason != "" {
			t.Fatalf("a terminal client refuses to paste: %q", reason)
		}
		if cmd := m.RequestHostPaste(); cmd == nil {
			t.Fatalf("a terminal client never asked for the clipboard")
		}
		if !m.pastePending {
			t.Fatalf("the query is not armed, so silence would never be reported")
		}
	}
}

// TestUnansweredPasteIsReported covers a terminal that refuses the read query,
// which kitty does by default. Without the deadline the key looks broken.
func TestUnansweredPasteIsReported(t *testing.T) {
	m := &OS{Settings: config.DefaultSettings(), Mode: TerminalMode, KeybindRegistry: config.NewKeybindRegistry(config.DefaultConfig())}
	m.RequestHostPaste()
	seq := m.pasteSeq

	_, _ = m.Update(PasteTimeoutMsg{Seq: seq})
	msg := lastMessage(m)
	if msg == "" {
		t.Fatalf("a terminal that never answered left the user with nothing")
	}
	if !strings.Contains(msg, "clipboard") {
		t.Fatalf("the message does not say what happened: %q", msg)
	}

	// A second timer for the same query says nothing more.
	m.Notifications = nil
	_, _ = m.Update(PasteTimeoutMsg{Seq: seq})
	if lastMessage(m) != "" {
		t.Fatalf("the same unanswered query was reported twice")
	}
}

// TestAnsweredPasteIsNotReported checks that a reply disarms the deadline, so a
// paste that worked never also complains that it did not.
func TestAnsweredPasteIsNotReported(t *testing.T) {
	m := &OS{Settings: config.DefaultSettings(), Mode: TerminalMode, KeybindRegistry: config.NewKeybindRegistry(config.DefaultConfig())}
	m.RequestHostPaste()
	seq := m.pasteSeq

	m.NotePasteArrived()
	_, _ = m.Update(PasteTimeoutMsg{Seq: seq})
	if msg := lastMessage(m); msg != "" {
		t.Fatalf("a paste that worked also reported a failure: %q", msg)
	}
}

// TestBrowserPaneMenuDimsPaste checks the row, not just the key. It looked fully
// live in a browser and did nothing at all when clicked.
func TestBrowserPaneMenuDimsPaste(t *testing.T) {
	find := func(items []ContextMenuItem, action string) (ContextMenuItem, bool) {
		for _, it := range items {
			if it.Action == action {
				return it, true
			}
		}
		return ContextMenuItem{}, false
	}

	local := &OS{Settings: config.DefaultSettings(), Mode: TerminalMode, KeybindRegistry: config.NewKeybindRegistry(config.DefaultConfig())}
	_, items := local.paneMenu(-1)
	row, ok := find(items, "paste_clipboard")
	if !ok {
		t.Fatalf("the pane menu has no paste row")
	}
	if row.Dim {
		t.Fatalf("a terminal client cannot paste from its own menu")
	}

	web := &OS{Settings: config.DefaultSettings(), Mode: TerminalMode, BrowserClient: true, RemoteClient: true,
		KeybindRegistry: config.NewKeybindRegistry(config.DefaultConfig())}
	_, items = web.paneMenu(-1)
	row, ok = find(items, "paste_clipboard")
	if !ok {
		t.Fatalf("the pane menu has no paste row")
	}
	if !row.Dim {
		t.Fatalf("the browser paste row looks live and does nothing")
	}
	menu := ContextMenu{Items: items}
	for i, it := range items {
		if it.Action == "paste_clipboard" && menu.selectable(i) {
			t.Fatalf("the dimmed browser paste row can still be clicked")
		}
	}
}
