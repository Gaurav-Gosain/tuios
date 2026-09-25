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

func TestAskAgentPastesAndSubmitsWithCR(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "paste")
	id, _ := rawReaderPane(t, d, sess)
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"ask-agent","params":{"session":"paste","window":"`+id+`","text":"line one\nline two\n","settle":700,"timeout":8000}}`))
	got, ok := gotBytes(t, res["reply"].(string))
	if !ok {
		t.Fatalf("the pane never saw a carriage return; settled_by %v, reply %q", res["settled_by"], res["reply"])
	}
	if want := "\x1b[200~line one\nline two\x1b[201~\r"; got != want {
		t.Errorf("the pane read %q, want %q", got, want)
	}
}
