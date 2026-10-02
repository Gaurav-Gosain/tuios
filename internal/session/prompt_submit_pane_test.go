//go:build !windows

package session

import (
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// rawReaderScript is the program in the pane. Raw mode is set before bracketed
// paste is announced, because setting it discards input already queued, and
// the test writes as soon as the announcement is seen.
const rawReaderScript = `
import os, sys, time, tty
tty.setraw(0)
sys.stdout.write("\x1b[?2004h")
sys.stdout.flush()
buf = b""
while not buf.endswith(b"\r"):
    c = os.read(0, 1)
    if not c:
        break
    buf += c
sys.stdout.write("\r\nGOT:" + buf.hex() + ":END\r\n")
sys.stdout.flush()
time.sleep(60)
`

// rawReaderPane opens a window running rawReaderScript and waits until the
// daemon's emulator has seen it turn bracketed paste on.
func rawReaderPane(t *testing.T, d *Daemon, sess *Session) (string, *PTY) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	w, err := sess.AddDaemonWindowWith(NewWindowOptions{Title: "reader", Command: []string{python, "-c", rawReaderScript}}, nil)
	if err != nil {
		t.Fatalf("AddDaemonWindowWith: %v", err)
	}
	pty, err := d.resolvePTYForTarget(sess, w.ID)
	if err != nil {
		t.Fatalf("resolvePTYForTarget: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !pty.BracketedPasteOn() {
		if time.Now().After(deadline) {
			t.Fatal("the pane never turned bracketed paste on")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return w.ID, pty
}

// gotBytes pulls the bytes the reader reported out of its output.
func gotBytes(t *testing.T, out string) (string, bool) {
	t.Helper()
	flat := strings.ReplaceAll(out, "\n", "")
	_, after, ok := strings.Cut(flat, "GOT:")
	if !ok {
		return "", false
	}
	rest := after
	before0, _, ok0 := strings.Cut(rest, ":END")
	if !ok0 {
		return "", false
	}
	raw, err := hex.DecodeString(strings.TrimSpace(before0))
	if err != nil {
		t.Fatalf("the reader printed %q, which is not hex: %v", before0, err)
	}
	return string(raw), true
}

// TestSendTextPasteBrackets sends text with paste: the control characters go,
// and the text is wrapped in the bracketed paste delimiters because the
// program in the pane turned bracketed paste on. The ESC in the text is what
// would end the paste early; without the sanitizing the pane reads a
// delimiter the text carried.
func TestSendTextPasteBrackets(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "tpaste")
	id, _ := rawReaderPane(t, d, sess)
	c := dialVerb(t, sp)

	result(t, c.call(t, `{"id":1,"verb":"send-text","params":{"session":"tpaste","window":"`+id+`","text":"one\ntwo\u001b[201~x","paste":true}}`))
	result(t, c.call(t, `{"id":2,"verb":"send-text","params":{"session":"tpaste","window":"`+id+`","text":"\r"}}`))
	deadline := time.Now().Add(10 * time.Second)
	for {
		res := result(t, c.call(t, `{"id":3,"verb":"capture-pane","params":{"session":"tpaste","window":"`+id+`"}}`))
		if got, ok := gotBytes(t, res["content"].(string)); ok {
			if want := "\x1b[200~one\ntwo[201~x\x1b[201~\r"; got != want {
				t.Errorf("the pane read %q, want %q", got, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never reported what it read: %q", res["content"])
		}
		time.Sleep(50 * time.Millisecond)
	}
}
