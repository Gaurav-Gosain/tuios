package session

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestRenameSessionChangesTheNameEverywhere is issue #266 at the daemon: the
// UI renamed a session and `tuios ls` kept the old name, because the rename
// only set a display label. rename-session changes the one name the session
// has, so list-sessions, the verbs, the state and the state file all agree.
func TestRenameSessionChangesTheNameEverywhere(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "test")
	if err := sess.SetDisplayName("Old label"); err != nil {
		t.Fatal(err)
	}
	if err := SaveSessionForResurrection(sess.ResurrectionState()); err != nil {
		t.Fatal(err)
	}
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"rename-session","params":{"session":"test","name":"work"}}`))
	if res["session"] != "work" || res["old_name"] != "test" {
		t.Fatalf("rename-session returned %v", res)
	}

	var names []string
	for _, info := range d.manager.ListSessions() {
		names = append(names, info.Name)
	}
	if !slices.Equal(names, []string{"work"}) {
		t.Errorf("list-sessions names %v, want [work]", names)
	}
	if sess.Name() != "work" || sess.GetState().Name != "work" {
		t.Errorf("session name %q, state name %q, want work for both", sess.Name(), sess.GetState().Name)
	}
	if got := sess.GetState().DisplayName; got != "" {
		t.Errorf("display label %q survived the rename, so the UI would still show it", got)
	}
	if d.manager.GetSession("work") != sess {
		t.Error("the new name does not find the session")
	}

	// The state file moved with the name, so a daemon restart brings back one
	// session called work and no session called test.
	if _, err := os.Stat(getResurrectionPath("test")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old state file is still there: %v", err)
	}
	loaded, err := LoadResurrectionState("work")
	if err != nil {
		t.Fatalf("no state file under the new name: %v", err)
	}
	if loaded.Name != "work" {
		t.Errorf("the saved state is named %q, want work", loaded.Name)
	}

	// A pane started before the rename keeps TUIOS_SESSION=test, and a verb
	// it sends by that name still reaches the session.
	info := result(t, c.call(t, `{"id":2,"verb":"session-info","params":{"session":"test"}}`))
	if info["session_name"] != "work" {
		t.Errorf("session-info by the old name answered for %v, want work", info["session_name"])
	}
}

// TestRenameSessionRefusesBadNames covers the names a rename must not take: a
// name another session has, an empty one, and one that cannot be a file name.
func TestRenameSessionRefusesBadNames(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "alpha")
	makeSessionWithWindow(t, d, "bravo")
	c := dialVerb(t, sp)

	for _, name := range []string{"bravo", "", "   ", "a/b", "..", "tab\there"} {
		resp := c.call(t, `{"id":1,"verb":"rename-session","params":{"session":"alpha","name":"`+jsonEscape(name)+`"}}`)
		if code := errCode(t, resp); code != ErrVerbInvalidParams {
			t.Errorf("rename to %q: code %q, want %s", name, code, ErrVerbInvalidParams)
		}
	}
	if d.manager.GetSession("alpha") == nil || d.manager.GetSession("bravo") == nil {
		t.Error("a refused rename still moved a session")
	}
	resp := c.call(t, `{"id":2,"verb":"rename-session","params":{"session":"nope","name":"x"}}`)
	if code := errCode(t, resp); code != ErrVerbSessionNotFound {
		t.Errorf("rename of a missing session: code %q, want %s", code, ErrVerbSessionNotFound)
	}
}

// TestRenamedNameYieldsToANewSession: an old name is only an alias. A new
// session may take it, and then the name means the new session.
func TestRenamedNameYieldsToANewSession(t *testing.T) {
	d, _ := startTestDaemon(t)
	first := makeSessionWithWindow(t, d, "test")
	if _, err := d.manager.RenameSession("test", "work"); err != nil {
		t.Fatal(err)
	}
	if got, renamed := d.manager.ResolveSession("test"); got != first || !renamed {
		t.Fatalf("the old name resolved to %v (renamed=%v), want the renamed session", got, renamed)
	}
	second := makeSessionWithWindow(t, d, "test")
	if got, renamed := d.manager.ResolveSession("test"); got != second || renamed {
		t.Errorf("after a new session took the name, it resolved to %v (renamed=%v)", got, renamed)
	}
}

// TestClientPushKeepsTheDaemonsName: a client that built its push before the
// rename still carries the old name, and taking it would undo the rename.
func TestClientPushKeepsTheDaemonsName(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "test")
	stale := sess.GetState()
	if _, err := d.manager.RenameSession("test", "work"); err != nil {
		t.Fatal(err)
	}
	sess.UpdateState(stale)
	if got := sess.GetState().Name; got != "work" {
		t.Errorf("a stale push set the state name back to %q", got)
	}
}

// TestPersistDropsASnapshotUnderTheOldName: a periodic save that took its
// snapshot before the rename must not write it after, or the next daemon
// start would restore the old name as a second session.
func TestPersistDropsASnapshotUnderTheOldName(t *testing.T) {
	d, _ := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "test")
	snap := sess.ResurrectionState()
	if _, err := d.manager.RenameSession("test", "work"); err != nil {
		t.Fatal(err)
	}
	if err := sess.persist(snap); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(getResurrectionPath("test")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a snapshot taken before the rename brought the old state file back: %v", err)
	}
}

// TestRenamedSessionTargetReadsTheMessage pins the attach refusal's wording to
// its parser.
func TestRenamedSessionTargetReadsTheMessage(t *testing.T) {
	got, ok := RenamedSessionTarget(errors.New(RenamedSessionMessage("test", "work")))
	if !ok || got != "work" {
		t.Errorf("RenamedSessionTarget = %q, %v, want work, true", got, ok)
	}
	if _, ok := RenamedSessionTarget(errors.New("session 'test' not found")); ok {
		t.Error("a plain not-found read as a rename")
	}
}

func jsonEscape(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '\t':
			out = append(out, '\\', 't')
		case '"', '\\':
			out = append(out, '\\', r)
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

// TestKillByOldNameNamesTheNewOne: a kill never follows an old name. It says
// what the session is called now, the way attach does.
func TestKillByOldNameNamesTheNewOne(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "test")
	if _, err := d.manager.RenameSession("test", "work"); err != nil {
		t.Fatal(err)
	}
	c := dialVerb(t, sp)
	resp := c.call(t, `{"id":1,"verb":"kill-session","params":{"session":"test"}}`)
	msg, _ := errorOf(t, resp)["message"].(string)
	if got, ok := RenamedSessionTarget(errors.New(msg)); !ok || got != "work" {
		t.Errorf("kill-session by the old name said %q, want the new name work", msg)
	}
	if d.manager.GetSession("work") == nil {
		t.Error("a kill by the old name killed the renamed session")
	}
}

// TestRenameRefusesASavedSessionsName: a saved session that is not running
// owns its state file, and a rename to its name would overwrite it.
func TestRenameRefusesASavedSessionsName(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "test")
	if err := SaveSessionForResurrection(&SessionState{Name: "saved"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.manager.RenameSession("test", "saved"); err == nil {
		t.Fatal("a rename took the name of a saved session")
	}
	if loaded, err := LoadResurrectionState("saved"); err != nil || loaded.Name != "saved" {
		t.Errorf("the saved session's file changed: %v %v", loaded, err)
	}
}

// TestStaleDaemonErrorNamesTheRestart: an older daemon answers unknown_verb
// for rename-session, and the person is told to restart it.
func TestStaleDaemonErrorNamesTheRestart(t *testing.T) {
	err := StaleDaemonError("rename-session", &VerbCallError{Code: ErrVerbUnknownVerb, Message: "unknown verb"})
	if err == nil || !strings.Contains(err.Error(), "tuios kill-server") {
		t.Errorf("StaleDaemonError = %v, want the kill-server restart", err)
	}
	if StaleDaemonError("no-such-verb", &VerbCallError{Code: ErrVerbUnknownVerb}) != nil {
		t.Error("a verb this tuios does not know either was blamed on the daemon")
	}
}
