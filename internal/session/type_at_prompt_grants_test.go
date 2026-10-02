package session

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

// cdAsPane sends MsgTypeAtPrompt for ptyID on a connection the daemon places
// in window (the person when window is empty), and returns the answer.
func cdAsPane(t *testing.T, d *Daemon, sess *Session, window, ptyID, dir string) PromptTypedPayload {
	t.Helper()
	d.setApprovalPeer(func(*connState) (bool, string) { return window != "", window })
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	cs := &connState{conn: server, clientID: "cd-" + window, sessionID: sess.ID}
	msg, err := NewMessage(MsgTypeAtPrompt, &TypeAtPromptPayload{PTYID: ptyID, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *Message, 1)
	go func() {
		_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
		resp, _ := ReadMessage(client)
		done <- resp
	}()
	if err := d.handleTypeAtPrompt(cs, msg); err != nil {
		t.Fatal(err)
	}
	resp := <-done
	if resp == nil || resp.Type != MsgPromptTyped {
		t.Fatalf("no answer to MsgTypeAtPrompt: %v", resp)
	}
	var out PromptTypedPayload
	if err := resp.ParsePayload(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func ptyOf(t *testing.T, sess *Session, windowID string) string {
	t.Helper()
	for _, w := range sess.GetState().Windows {
		if w.ID == windowID {
			return w.PTYID
		}
	}
	t.Fatalf("no window %s", windowID)
	return ""
}

// A pane whose grants do not cover the target's cannot cd it.
func TestAPaneWithoutTheTargetsGrantsCannotCdIt(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	d, _, a1, a2, _ := scopeFixture(t)
	setStrict(d, "read")
	more, verr := parseGrantsParam([]string{"read", "write"})
	if verr != nil {
		t.Fatal(verr)
	}
	if !d.manager.grants.set(a2, &more) {
		t.Fatal("could not give the target more grants")
	}
	sess := d.manager.GetSession("a")
	pty := sess.GetPTY(ptyOf(t, sess, a2))
	if _, ok := readForegroundPGID(pty.ShellPID()); !ok {
		t.Skip("this platform does not report the terminal's foreground group")
	}
	waitPrompt(t, "the shell to take the terminal", func() bool { return shellAtPrompt(pty) })

	if got := cdAsPane(t, d, sess, a1, ptyOf(t, sess, a2), "/tmp"); got.Typed || got.Refused == "" {
		t.Fatalf("a read-only pane's cd into a pane holding more = %+v, want refused", got)
	}
	// The person is not a pane and is held to nothing: the same cd goes in.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := cdAsPane(t, d, sess, "", ptyOf(t, sess, a2), dir); !got.Typed {
		t.Fatalf("the person's cd = %+v, want typed", got)
	}
}

// The request names a directory, and the daemon builds the line. A name that
// would carry a command is refused.
func TestTypeAtPromptRefusesAnUnsafeFolder(t *testing.T) {
	d, _, _, a2, _ := scopeFixture(t)
	sess := d.manager.GetSession("a")
	for _, dir := range []string{"/tmp/x'; touch /tmp/pwn; '", "/tmp/a\rb", "relative", `/tmp/back\slash`} {
		if got := cdAsPane(t, d, sess, "", ptyOf(t, sess, a2), dir); got.Typed {
			t.Fatalf("CdAtPrompt(%q) typed it", dir)
		}
	}
}
