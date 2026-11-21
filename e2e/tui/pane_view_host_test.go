package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestPaneViewOnAHost shows a pane of a session on the host build, named as
// -s build:SESSION, through this machine's daemon and its link.
//
// pane-view used to dial this machine's daemon and send the flag as typed,
// so it asked for a session named build:pv-far here and got
// session_not_found, while every other session verb reached the host.
//
// NEGATIVE CONTROL (e2e/tui/NEGATIVE_CONTROLS.md, "pane-view on a host"): with
// paneView.dial calling dialVerb for every host, the test fails at "did not
// reach the pane on build".
func TestPaneViewOnAHost(t *testing.T) {
	requireReal(t, "real ssh or sshd")
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "pv-far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	win := firstWindow(t, remote, "pv-far").ID

	// The positive half: the same session on this machine's daemon is not
	// there, so a view that works reached build.
	if out, err := tuiosCLI(t, base, "list-windows", "-s", "pv-far"); err == nil {
		t.Fatalf("a session pv-far exists on this machine too, so the test proves nothing:\n%s", out)
	}

	view := startIn(t, base, startOpts{cols: 90, rows: 25, animations: true, env: env,
		args: []string{"pane-view", "-s", "build:pv-far", "-w", win}})
	if err := view.SendKeys("printf 'FAR-%s-OK\\n' $((6*7))", tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := view.WaitForText("FAR-42-OK", shellTimeout); err != nil {
		t.Fatalf("ASSERTION: pane-view -s build:pv-far did not reach the pane on build: %v\n%s", err, view.Snapshot())
	}
	if snap := view.Snapshot(); strings.Contains(snap, "session_not_found") {
		t.Fatalf("ASSERTION: pane-view looked for build:pv-far on this machine:\n%s", snap)
	}
	_ = view.Close()
}
