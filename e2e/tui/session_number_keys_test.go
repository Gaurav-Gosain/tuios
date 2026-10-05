package tuie2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// workspaceNameIn is the name the daemon holds for workspace ws of session
// sess, read through list-workspaces so the test sees daemon state and not the
// client's copy.
func workspaceNameIn(t *testing.T, base, sess string, ws int) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-workspaces", "--json", "--session", sess)
	if err != nil {
		t.Fatalf("list-workspaces: %v\n%s", err, out)
	}
	var res struct {
		Workspaces []struct {
			Workspace int    `json:"workspace"`
			Name      string `json:"name"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("list-workspaces json: %v\n%s", err, out)
	}
	for _, w := range res.Workspaces {
		if w.Workspace == ws {
			return w.Name
		}
	}
	return ""
}

// TestSwitchSessionByNumberAndRenameWorkspace binds switch_session_3,
// switch_session_9 and rename_workspace in config.toml and drives them in a
// client with three sessions. The third rail position is charlie, so the
// session key lands there from terminal mode. Slot 9 is empty, so it says so
// and stays put. The rename goes through the daemon, so the name is read back
// with list-workspaces.
//
// Negative control: build origin/main before #460. The three actions do not
// exist there, and the test fails at the wait for "Session: charlie".
func TestSwitchSessionByNumberAndRenameWorkspace(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[keybindings.window_management]
switch_session_3 = ["alt+shift+c"]
switch_session_9 = ["alt+shift+z"]
rename_workspace = ["f6"]
`)
	killDaemon(t, base)
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}

	term := startIn(t, base, startOpts{cols: 140, rows: 40, args: []string{"attach", "alpha"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached to alpha: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	enterTerminalMode(t, term)

	// switch_session_N is terminal-safe, like next_session, so the chord works
	// while a shell has the keys.
	if err := term.SendKeys(tuitest.Alt('C')); err != nil {
		t.Fatalf("send switch_session_3: %v", err)
	}
	if err := term.WaitForText("Session: charlie", uiTimeout); err != nil {
		t.Fatalf("switch_session_3 never landed on charlie: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "switch-session-3")
	time.Sleep(insertGuard + 150*time.Millisecond)

	if err := term.SendKeys(tuitest.Alt('Z')); err != nil {
		t.Fatalf("send switch_session_9: %v", err)
	}
	if err := term.WaitForText("No session 9", uiTimeout); err != nil {
		t.Fatalf("switch_session_9 with three sessions did not say so: %v\n%s", err, term.Snapshot())
	}

	windowManagementMode(t, term)
	if err := term.SendKeys("\x1b[17~"); err != nil { // F6
		t.Fatalf("send rename_workspace: %v", err)
	}
	if err := term.WaitForText("rename workspace 1", uiTimeout); err != nil {
		t.Fatalf("rename_workspace never opened the editor: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("zebra"); err != nil {
		t.Fatalf("type the name: %v", err)
	}
	if err := term.WaitForText("› zebra", uiTimeout); err != nil {
		t.Fatalf("the editor never showed the name: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("commit the rename: %v", err)
	}

	deadline := time.Now().Add(uiTimeout)
	for workspaceNameIn(t, base, "charlie", 1) != "zebra" {
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never held the name zebra for charlie's workspace 1\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := workspaceNameIn(t, base, "alpha", 1); got != "" {
		t.Fatalf("alpha's workspace 1 is %q, want no name: the rename went to the wrong session", got)
	}
	saveFrame(t, term, "rename-workspace")
}
