package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A clipboard reply that answers no request of tuios is somebody else's
// (a pane that asked the host terminal itself, or the terminal replaying one)
// and must not be typed into the focused pane.
func TestUnrequestedClipboardReplyIsDropped(t *testing.T) {
	o, sent := pasteHarness(t, false)

	_, _ = HandleInput(tea.ClipboardMsg{Content: "rm -rf ~\n", Selection: 'c'}, o)

	if got := sent.String(); got != "" {
		t.Fatalf("an unrequested clipboard reply reached the pane: %q", got)
	}
	if o.ClipboardContent == "rm -rf ~\n" {
		t.Fatalf("an unrequested clipboard reply was stored as the clipboard")
	}
}

// A reply to tuios's own read request is still pasted, once.
func TestRequestedClipboardReplyIsPasted(t *testing.T) {
	o, sent := pasteHarness(t, false)

	_ = o.RequestHostPaste()
	_, _ = HandleInput(tea.ClipboardMsg{Content: "echo hi", Selection: 'c'}, o)
	if got := sent.String(); got != "echo hi" {
		t.Fatalf("PTY received %q, want the requested clipboard text", got)
	}

	// A second reply to the same request is not.
	_, _ = HandleInput(tea.ClipboardMsg{Content: "echo again", Selection: 'c'}, o)
	if got := sent.String(); got != "echo hi" {
		t.Fatalf("a second reply to one request reached the pane: %q", got)
	}
}
