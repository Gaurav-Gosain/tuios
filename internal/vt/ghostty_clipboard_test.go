//go:build ghostty

package vt

import (
	"strings"
	"testing"
)

// TestGhosttyClipboardWriteEffectFires covers the clipboard-write effect.
//
// tuios answers OSC 52 itself in handleOSC and never forwards it, so the only
// clipboard writes that reach libghostty are the iTerm2 OSC 1337 Copy form and
// the Kitty OSC 5522 protocol.
func TestGhosttyClipboardWriteEffectFires(t *testing.T) {
	term := NewGhosttyTerminal(20, 5)
	defer term.Close()
	var got [][2]string
	term.SetCallbacks(Callbacks{
		ClipboardSet: func(sel, content string) { got = append(got, [2]string{sel, content}) },
	})
	// OSC 1337 ; Copy=: base64("hello") ST
	if _, err := term.Write([]byte("\x1b]1337;Copy=:aGVsbG8=\x1b\\")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ClipboardSet calls = %d, want 1: the library dropped the write", len(got))
	}
	if got[0][0] != "c" {
		t.Errorf("selection = %q, want %q", got[0][0], "c")
	}
	if got[0][1] != "hello" {
		t.Errorf("clipboard content = %q, want %q", got[0][1], "hello")
	}
}

// TestGhosttyClipboardWriteIsAnswered pins the reply contract.
//
// libghostty asks the handler to answer a clipboard write. A handler that
// returns without a reply denies it. The Kitty clipboard protocol reports the
// outcome back to the guest, so the answer is visible: a granted write ends in
// status=DONE and a denied one in status=EPERM. This test fails if the handler
// stops answering, or answers with a refusal.
func TestGhosttyClipboardWriteIsAnswered(t *testing.T) {
	term := NewGhosttyTerminal(20, 5)
	defer term.Close()
	var got [][2]string
	term.SetCallbacks(Callbacks{
		ClipboardSet: func(sel, content string) { got = append(got, [2]string{sel, content}) },
	})
	// A full OSC 5522 write transaction: begin, one data chunk, commit.
	// dGV4dC9wbGFpbg== is "text/plain" and R2hvc3R0eQ== is "Ghostty".
	seqs := []string{
		"\x1b]5522;type=write:id=c1\x1b\\",
		"\x1b]5522;type=wdata:mime=dGV4dC9wbGFpbg==;R2hvc3R0eQ==\x1b\\",
		"\x1b]5522;type=wdata\x1b\\",
	}
	for _, s := range seqs {
		if _, err := term.Write([]byte(s)); err != nil {
			t.Fatalf("write %q: %v", s, err)
		}
	}
	if len(got) != 1 {
		t.Fatalf("ClipboardSet calls = %d, want 1", len(got))
	}
	if got[0][1] != "Ghostty" {
		t.Errorf("clipboard content = %q, want %q", got[0][1], "Ghostty")
	}
	buf := make([]byte, 256)
	n, err := term.Read(buf)
	if err != nil {
		t.Fatalf("read the reply: %v", err)
	}
	reply := string(buf[:n])
	if !strings.Contains(reply, "status=DONE") {
		t.Fatalf("reply = %q, want status=DONE: the write was not granted", reply)
	}
}
