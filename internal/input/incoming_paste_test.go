package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// pasteHarness builds a focused pane in terminal mode whose PTY writes are
// captured instead of touching a real kernel PTY. The window runs in daemon
// mode so SendInput routes through DaemonWriteFunc, which is the path a real
// daemon session uses. sent accumulates everything delivered to the PTY.
func pasteHarness(t *testing.T, bracketed bool) (*app.OS, *strings.Builder) {
	t.Helper()

	em := vt.NewEmulator(40, 20)
	t.Cleanup(func() { _ = em.Close() })
	if bracketed {
		// ?2004h: the inner app turns on bracketed paste mode.
		if _, err := em.Write([]byte("\x1b[?2004h")); err != nil {
			t.Fatalf("enable bracketed paste: %v", err)
		}
	}

	var sent strings.Builder
	win := &terminal.Window{
		Terminal:   em,
		X:          0,
		Y:          0,
		Width:      42,
		Height:     22,
		DaemonMode: true,
		DaemonWriteFunc: func(b []byte) error {
			sent.Write(b)
			return nil
		},
	}
	o := &app.OS{
		Settings:      config.Global,
		Mode:          app.TerminalMode,
		FocusedWindow: 0,
		Windows:       []*terminal.Window{win},
		Width:         80,
		Height:        30,
	}
	return o, &sent
}

func hasPastedNotification(o *app.OS) bool {
	for _, n := range o.Notifications {
		if strings.Contains(n.Message, "Pasted") {
			return true
		}
	}
	return false
}

// An incoming tea.PasteMsg is passthrough input from the outer terminal (for
// example an fcitx5 IME commit that arrives wrapped in bracketed-paste
// markers). It must reach the pane's PTY verbatim, must not overwrite the
// user's stored clipboard, and must not raise a "Pasted N characters"
// notification. This is the regression from issue #113.
func TestIncomingPasteIsPassthroughNotClipboard(t *testing.T) {
	o, sent := pasteHarness(t, false)

	const sentinel = "user-clipboard-do-not-touch"
	o.ClipboardContent = sentinel

	_, _ = HandleInput(tea.PasteMsg{Content: "中文"}, o)

	if o.ClipboardContent != sentinel {
		t.Errorf("ClipboardContent = %q, want it left as %q: an incoming terminal paste "+
			"must not clobber the user's real clipboard", o.ClipboardContent, sentinel)
	}
	if hasPastedNotification(o) {
		t.Errorf("incoming paste raised a %q notification; IME/terminal paste must be silent",
			"Pasted")
	}
	if got := sent.String(); got != "中文" {
		t.Errorf("PTY received %q, want the CJK text %q delivered raw (inner app has no "+
			"bracketed paste)", got, "中文")
	}
}

// When the inner app has bracketed paste enabled, the incoming paste must be
// re-wrapped so the app still sees a paste, and the multi-byte UTF-8 payload
// must survive the wrapping intact.
func TestIncomingPasteRewrapsWhenBracketedPasteEnabled(t *testing.T) {
	o, sent := pasteHarness(t, true)
	o.ClipboardContent = "keep-me"

	_, _ = HandleInput(tea.PasteMsg{Content: "中文"}, o)

	want := "\x1b[200~中文\x1b[201~"
	if got := sent.String(); got != want {
		t.Errorf("PTY received %q, want bracketed %q", got, want)
	}
	if o.ClipboardContent != "keep-me" {
		t.Errorf("ClipboardContent = %q, want it untouched", o.ClipboardContent)
	}
	if hasPastedNotification(o) {
		t.Error("bracketed incoming paste must still be silent")
	}
}

// TUIOS's own clipboard paste (the OSC 52 read response, tea.ClipboardMsg) is a
// real clipboard operation. It stores the content and notifies. This path must
// keep working so the two are provably distinct.
func TestClipboardMsgStillPastesAndNotifies(t *testing.T) {
	o, sent := pasteHarness(t, false)

	_, _ = HandleInput(tea.ClipboardMsg{Content: "hello"}, o)

	if o.ClipboardContent != "hello" {
		t.Errorf("ClipboardContent = %q, want %q stored from the clipboard read",
			o.ClipboardContent, "hello")
	}
	if !hasPastedNotification(o) {
		t.Error("a real clipboard paste must still show the \"Pasted\" notification")
	}
	if got := sent.String(); got != "hello" {
		t.Errorf("PTY received %q, want %q", got, "hello")
	}
}
