package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestRenameVerbRenamesTheSession is issue #266: a session rename from the UI
// must change the session's name in the daemon, so the name the UI shows is
// the name ls lists. It used to send set-session-name, which only set a label,
// and ls kept the old name.
func TestRenameVerbRenamesTheSession(t *testing.T) {
	verb, params, ok := renameVerb(RenameSession, "test", "test", "work")
	if !ok || verb != "rename-session" {
		t.Fatalf("session rename verb = %q (ok=%v), want rename-session", verb, ok)
	}
	if params["session"] != "test" {
		t.Errorf("addressed %v, want the current name test", params["session"])
	}
	if params["name"] != "work" {
		t.Errorf("new name = %v, want work", params["name"])
	}

	verb, params, ok = renameVerb(RenameWorkspace, "2", "work", "review")
	if !ok || verb != "set-workspace-name" {
		t.Fatalf("workspace rename verb = %q (ok=%v), want set-workspace-name", verb, ok)
	}
	if params["workspace"] != 2 || params["session"] != "work" || params["name"] != "review" {
		t.Errorf("workspace rename params = %v", params)
	}

	// A session cannot be without a name, so an empty field sends nothing.
	if _, _, ok = renameVerb(RenameSession, "work", "work", ""); ok {
		t.Error("an empty session name produced a verb")
	}
	if _, _, ok = renameVerb(RenameWindow, "w1", "work", "x"); ok {
		t.Error("a window rename must not go through a session verb")
	}
}

// TestSessionRenameDoesNotBlockUpdate is the rule this design turns on: the verb
// call is a blocking round trip serialised behind the client's round-trip mutex,
// so it belongs in a command. Committing must hand back a command and return at
// once, doing no network work itself.
func TestSessionRenameDoesNotBlockUpdate(t *testing.T) {
	// An empty runtime dir means the socket does not exist, so a call made
	// inline would fail here rather than reach a daemon.
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))

	m := &OS{Settings: config.Global, SessionName: "work", SessionDisplayName: "old"}
	m.BeginRenameSession("work")
	if m.RenameBuffer != "old" {
		t.Fatalf("editor seeded with %q, want the existing label", m.RenameBuffer)
	}
	m.RenameBuffer = "Payments API"

	start := time.Now()
	cmd := m.CommitRename()
	elapsed := time.Since(start)

	if cmd == nil {
		t.Fatal("committing a session rename returned no command, so the call ran on this goroutine")
	}
	if elapsed > 20*time.Millisecond {
		t.Errorf("CommitRename blocked for %s before returning", elapsed)
	}
	if m.Renaming() {
		t.Error("the editor is still open after committing")
	}
	if m.SessionName != "work" {
		t.Errorf("SessionName = %q, want work until the daemon pushes the new name", m.SessionName)
	}

	// The round trip happens when the runtime runs the command, not before.
	msg := cmd()
	applied, ok := msg.(RenameAppliedMsg)
	if !ok {
		t.Fatalf("command returned %T, want RenameAppliedMsg", msg)
	}
	if applied.Err == nil {
		t.Error("expected a dial error with no daemon listening")
	}
}

// TestSessionRenameSeedsTheName: the editor opens on the name the session
// shows, so a person edits the name rather than typing it from nothing, and
// saving it unchanged sends nothing.
func TestSessionRenameSeedsTheName(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	m := &OS{Settings: config.Global, SessionName: "test"}
	m.BeginRenameSession("test")
	if m.RenameBuffer != "test" {
		t.Fatalf("editor seeded with %q, want the session's name test", m.RenameBuffer)
	}
	if cmd := m.CommitRename(); cmd != nil {
		t.Error("saving the name unchanged sent a rename")
	}
}

// TestRenameAppendGate is the editor's own rule, checked without a keyboard: it
// takes what the chrome will draw and refuses what the chrome would strip.
func TestRenameAppendGate(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 100, Height: 30, SessionName: "work"}
	m.BeginRenameSession("work")
	m.RenameBuffer = ""

	for _, in := range []string{" ", "é", "日", "a"} {
		before := m.RenameBuffer
		m.RenameAppend(in)
		if m.RenameBuffer != before+in {
			t.Errorf("appending %q gave %q, want %q", in, m.RenameBuffer, before+in)
		}
	}
	for _, in := range []string{"\x1b", "\u0301", "\U0001f600", "\ue0a0", "\u25b6"} {
		before := m.RenameBuffer
		m.RenameAppend(in)
		if m.RenameBuffer != before {
			t.Errorf("appending %q was accepted: %q", in, m.RenameBuffer)
		}
	}

	// Nothing lands anywhere once the editor is closed.
	m.EndRename()
	m.RenameAppend("x")
	if m.RenameBuffer != "" {
		t.Errorf("typing after the editor closed left %q", m.RenameBuffer)
	}
}

// TestRenameFieldKeepsWhatWasTyped: the field laundered its buffer through the
// trimming sanitizer, so a space the user had just pressed was rubbed off the
// display and the key looked dead even once it reached the buffer. A wide rune
// costs two cells, and the frame has to stay square around it.
func TestRenameFieldKeepsWhatWasTyped(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 100, Height: 30, SessionName: "work", NumWorkspaces: 9}
	m.BeginRenameSession("work")

	m.RenameBuffer = "build "
	out, _, _, _, ok := m.renderRenameDialog()
	if !ok {
		t.Fatal("no dialog while a rename is open")
	}
	t.Logf("\n%s", out)
	if !strings.Contains(out, "build ") {
		t.Errorf("the trailing space is missing from the field:\n%s", out)
	}

	m.RenameBuffer = "日本語 café"
	out, geo, _, _, _ := m.renderRenameDialog()
	t.Logf("\n%s", out)
	if !strings.Contains(out, "日本語 café") {
		t.Errorf("a non-ASCII name does not reach the field:\n%s", out)
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w != geo.Width {
			t.Errorf("row %d is %d cells wide, want %d: a wide rune knocked the frame out of square\n%s", i, w, geo.Width, out)
		}
	}
}

// TestSessionRenameThroughHostDialsTheHost: a client attached through a host
// shows the host's sessions, so a rename or an accent must reach the host's
// daemon. labelVerbCmd dialed this machine's socket every time, which renamed
// a local session of the same name, or failed.
func TestSessionRenameThroughHostDialsTheHost(t *testing.T) {
	var hosts []string
	local := 0
	prevHost, prevLocal := dialVerbThroughHost, dialVerbLocal
	t.Cleanup(func() { dialVerbThroughHost, dialVerbLocal = prevHost, prevLocal })
	dialVerbThroughHost = func(host, _ string) (*session.VerbClient, session.HostConnectionInfo, error) {
		hosts = append(hosts, host)
		return nil, session.HostConnectionInfo{}, errors.New("no link in this test")
	}
	dialVerbLocal = func() (*session.VerbClient, error) {
		local++
		return nil, errors.New("no daemon in this test")
	}

	m := &OS{Settings: config.Global, SessionName: "test", AttachedHost: "build"}
	m.BeginRenameSession("test")
	m.RenameBuffer = "work"
	cmd := m.CommitRename()
	if cmd == nil {
		t.Fatal("the rename sent nothing")
	}
	_ = cmd()
	if accent := m.setSessionAccentCmd("test", "cyan"); accent != nil {
		_ = accent()
	}
	if local != 0 || len(hosts) != 2 || hosts[0] != "build" || hosts[1] != "build" {
		t.Errorf("dialed the local socket %d times and hosts %v, want host build twice and no local dial", local, hosts)
	}
}
