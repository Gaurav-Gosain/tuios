package main

import (
	"testing"
)

// TestAskpassNoticeAsksNothing: a notice from OpenSSH, such as a security
// key's "Confirm user presence", needs no answer. The program exits 0 at once
// and asks nothing, so no dialog takes the keyboard for an answer ssh never
// reads. A yes or no question with no terminal is refused, since the Inbox
// cannot ask it yet, rather than asked as hidden text.
//
// Negative control: with the SSH_ASKPASS_PROMPT switch cut, the notice goes
// to the daemon and the test fails with exit 1 (no TUIOS_SOCKET here).
func TestAskpassNoticeAsksNothing(t *testing.T) {
	t.Setenv("TUIOS_SOCKET", "")
	t.Setenv("SSH_ASKPASS_PROMPT", "none")
	if foregroundTerminalForTest() {
		t.Skip("the test runs on a terminal")
	}
	if code := runAsAskpass([]string{"Confirm user presence for key ED25519-SK SHA256:x"}); code != 0 {
		t.Fatalf("a notice exited %d, want 0", code)
	}
	t.Setenv("SSH_ASKPASS_PROMPT", "confirm")
	if code := runAsAskpass([]string{"Allow use of key ~/.ssh/id_ed25519?"}); code != 1 {
		t.Fatalf("a yes or no question with no terminal exited %d, want 1", code)
	}
}

// foregroundTerminalForTest reports whether this test process runs as the
// foreground job of a terminal, where the program would ask there.
func foregroundTerminalForTest() bool {
	tty, ok := foregroundTerminal()
	if ok {
		_ = tty.Close()
	}
	return ok
}
