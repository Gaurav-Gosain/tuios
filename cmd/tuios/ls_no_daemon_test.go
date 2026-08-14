package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// seedSavedSessions writes state files for the named sessions, which is all a
// session needs to exist while no daemon is running.
func seedSavedSessions(t *testing.T, names ...string) {
	t.Helper()
	dir := session.ResurrectionStateDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("create state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	for _, name := range names {
		state := session.SessionState{Name: name, Windows: []session.WindowState{{}}}
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("marshal state: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0600); err != nil {
			t.Fatalf("write state: %v", err)
		}
	}
}

// TestLsListsSavedSessionsWithNoDaemon is the listing half of the reported bug:
// with no daemon, ls printed nothing and claimed nothing existed, so attach
// refusing to open those sessions made no sense to read.
func TestLsListsSavedSessionsWithNoDaemon(t *testing.T) {
	seedSavedSessions(t, "notes", "work")
	diag := session.DiagnoseDaemon()
	if diag.Running() {
		t.Skip("a daemon is listening in the test tree")
	}

	var err error
	out := captureStdout(t, func() { err = listSavedSessions(diag, false) })

	for _, want := range []string{"work", "notes", session.SavedTag, "tuios attach"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing does not mention %q:\n%s", want, out)
		}
	}
	if exitStatus(err) != noDaemonStatus {
		t.Errorf("exit status = %d, want %d so a script can tell no daemon from no sessions", exitStatus(err), noDaemonStatus)
	}
}

// TestLsJSONDistinguishesNoDaemonFromNoSessions pins the contract a script
// needs: the same empty-looking listing meant two different things and exited 0
// for both.
func TestLsJSONDistinguishesNoDaemonFromNoSessions(t *testing.T) {
	seedSavedSessions(t, "work")
	diag := session.DiagnoseDaemon()
	if diag.Running() {
		t.Skip("a daemon is listening in the test tree")
	}

	var err error
	out := captureStdout(t, func() { err = listSavedSessions(diag, true) })

	var entries []struct {
		Name  string `json:"name"`
		Saved bool   `json:"saved"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &entries); jsonErr != nil {
		t.Fatalf("output is not JSON: %v\n%s", jsonErr, out)
	}
	if len(entries) != 1 || entries[0].Name != "work" || !entries[0].Saved {
		t.Fatalf("JSON does not report the saved session: %s", out)
	}
	if exitStatus(err) != noDaemonStatus {
		t.Errorf("exit status = %d, want %d", exitStatus(err), noDaemonStatus)
	}
	// The status carries the whole message here; printing an error under the
	// JSON would break every parser reading stdout.
	if err.Error() != "" {
		t.Errorf("the no-daemon status printed a message alongside the JSON: %q", err.Error())
	}
}

// TestLsWithNoDaemonAndNothingSavedSaysSo covers the other half: no daemon and
// genuinely no sessions must still be told apart from a daemon holding none.
func TestLsWithNoDaemonAndNothingSavedSaysSo(t *testing.T) {
	dir := session.ResurrectionStateDir()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("clear state dir: %v", err)
	}
	diag := session.DiagnoseDaemon()
	if diag.Running() {
		t.Skip("a daemon is listening in the test tree")
	}

	var err error
	out := captureStdout(t, func() { err = listSavedSessions(diag, false) })

	if !strings.Contains(out, "no sessions are saved") {
		t.Errorf("listing does not say the disk is empty too:\n%s", out)
	}
	if exitStatus(err) != noDaemonStatus {
		t.Errorf("exit status = %d, want %d", exitStatus(err), noDaemonStatus)
	}
}
