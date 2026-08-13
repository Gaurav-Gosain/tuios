package app

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestRenameVerbAddressesTheIdentityAndSendsTheLabel is the contract that keeps
// a rename off the identity: the session is addressed by the name it has always
// had, and the user's text travels as a separate label.
func TestRenameVerbAddressesTheIdentityAndSendsTheLabel(t *testing.T) {
	verb, params, ok := renameVerb(RenameSession, "work", "work", "Payments API")
	if !ok || verb != "set-session-name" {
		t.Fatalf("session rename verb = %q (ok=%v), want set-session-name", verb, ok)
	}
	if params["session"] != "work" {
		t.Errorf("addressed %v, want the identity work", params["session"])
	}
	if params["name"] != "Payments API" {
		t.Errorf("label = %v, want Payments API", params["name"])
	}

	verb, params, ok = renameVerb(RenameWorkspace, "2", "work", "review")
	if !ok || verb != "set-workspace-name" {
		t.Fatalf("workspace rename verb = %q (ok=%v), want set-workspace-name", verb, ok)
	}
	if params["workspace"] != 2 || params["session"] != "work" || params["name"] != "review" {
		t.Errorf("workspace rename params = %v", params)
	}

	// A clearing rename is an empty label, never a missing verb.
	if _, params, ok = renameVerb(RenameSession, "work", "work", ""); !ok || params["name"] != "" {
		t.Errorf("clearing a session label produced ok=%v params=%v", ok, params)
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
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	m := &OS{SessionName: "work", SessionDisplayName: "old"}
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
		t.Errorf("SessionName = %q, want the untouched identity work", m.SessionName)
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

// TestWorkspaceRenameSeedsAndCommits checks the workspace half of the same
// surface: seeded with the current name, and empty for one that has none.
func TestWorkspaceRenameSeedsAndCommits(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	m := &OS{SessionName: "work", NumWorkspaces: 9, CurrentWorkspace: 1}
	m.adoptSessionLabels(&session.SessionState{WorkspaceNames: map[int]string{2: "review"}})

	m.BeginRenameWorkspace(2)
	if m.RenameKind != RenameWorkspace || m.RenameBuffer != "review" {
		t.Fatalf("workspace editor = {kind:%v buffer:%q}, want a workspace rename seeded with review", m.RenameKind, m.RenameBuffer)
	}
	if got := m.RenameDialogTitle(); got != "rename workspace 2" {
		t.Errorf("dialog title = %q, want %q", got, "rename workspace 2")
	}

	m.EndRename()
	m.BeginRenameWorkspace(3)
	if m.RenameBuffer != "" {
		t.Errorf("an unnamed workspace seeded the editor with %q, want empty", m.RenameBuffer)
	}
	if cmd := m.CommitRename(); cmd == nil {
		t.Error("committing a workspace rename returned no command")
	}
}

// TestRenameDialogSaysWhatItRenames renders the one dialog for each target and
// reads its title off the frame, so the user can tell a session rename from a
// pane rename when both look identical otherwise.
func TestRenameDialogSaysWhatItRenames(t *testing.T) {
	m := &OS{Width: 100, Height: 30, SessionName: "work", NumWorkspaces: 9}

	m.BeginRenameSession("work")
	m.RenameBuffer = "Payments API"
	out, _, _, _, ok := m.renderRenameDialog()
	if !ok {
		t.Fatal("no dialog while a session rename is open")
	}
	t.Logf("\n%s", out)
	if !strings.Contains(out, "rename session") || !strings.Contains(out, "Payments API") {
		t.Errorf("session rename dialog does not name its target or show the buffer:\n%s", out)
	}

	m.EndRename()
	m.BeginRenameWorkspace(2)
	out, _, _, _, ok = m.renderRenameDialog()
	if !ok {
		t.Fatal("no dialog while a workspace rename is open")
	}
	t.Logf("\n%s", out)
	if !strings.Contains(out, "rename workspace 2") {
		t.Errorf("workspace rename dialog does not name its target:\n%s", out)
	}

	m.EndRename()
	if _, _, _, _, ok = m.renderRenameDialog(); ok {
		t.Error("a dialog is still drawn after the editor closed")
	}
}

// TestRenameFieldKeepsWhatWasTyped: the field laundered its buffer through the
// trimming sanitizer, so a space the user had just pressed was rubbed off the
// display and the key looked dead even once it reached the buffer. A wide rune
// costs two cells, and the frame has to stay square around it.
func TestRenameFieldKeepsWhatWasTyped(t *testing.T) {
	m := &OS{Width: 100, Height: 30, SessionName: "work", NumWorkspaces: 9}
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

// TestRenameAppendGate is the editor's own rule, checked without a keyboard: it
// takes what the chrome will draw and refuses what the chrome would strip.
func TestRenameAppendGate(t *testing.T) {
	m := &OS{Width: 100, Height: 30, SessionName: "work"}
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

// TestWindowRenameStillCommitsLocally guards the path that already existed: a
// window name is client-owned and must not be routed through a session verb.
func TestWindowRenameStillCommitsLocally(t *testing.T) {
	w := &terminal.Window{ID: "w1", CustomName: "build"}
	m := &OS{Windows: []*terminal.Window{w}}

	m.BeginRenameWindow(w)
	if m.RenameKind != RenameWindow || m.RenameBuffer != "build" {
		t.Fatalf("window editor = {kind:%v buffer:%q}", m.RenameKind, m.RenameBuffer)
	}
	if got := m.RenameDialogTitle(); got != "rename" {
		t.Errorf("dialog title = %q, want %q", got, "rename")
	}

	m.RenameBuffer = "tests"
	if cmd := m.CommitRename(); cmd != nil {
		t.Error("a window rename produced a daemon command")
	}
	if w.CustomName != "tests" {
		t.Errorf("window name = %q, want tests", w.CustomName)
	}
	if m.Renaming() {
		t.Error("the editor is still open after committing")
	}
}
