package tuie2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A pane on another machine whose daemon there no longer has it cannot come
// back: the far daemon restarted, and the pane's process went with it. This
// side must say so, everywhere a caller looks, and tell the person once. A
// pane whose machine is only down keeps waiting for it, and says that instead.
//
// How these could pass wrongly, written down first:
//   - The rig could link the daemon to itself, and the pane would be hosted
//     here. It links to a second daemon of its own (remoteMachine).
//   - A pane that is only reconnecting could be called ended. The third test
//     holds the far daemon down and wants reconnecting, not ended.
//   - A refusal could name "ended" and still come back as code internal. The
//     tests want the pane_ended code and the machine's name.
//   - wait-for could fail only because its timeout ran out. It is given 30 s
//     and must answer in a few.

// openFarShell opens a window on build in session near running a shell, and
// waits until it answers.
func openFarShell(t *testing.T, base string, env []string, name string) {
	t.Helper()
	if out, err := tuiosCLIEnv(t, base, env, "new-window", name, "-s", "near", "--host", "build", "--", "/bin/sh"); err != nil {
		t.Fatalf("open %s on build: %v\n%s", name, err, out)
	}
	waitUntil(t, name+" answers", uiTimeout*2, func() (bool, string) {
		_, _ = callVerb(t, base, "send-text", map[string]any{"session": "near", "window": name, "text": "echo READY-FAR\n"})
		out, _ := tuiosOut(base, "capture-pane", "-s", "near", "-w", name)
		return strings.Contains(out, "\nREADY-FAR"), out
	})
}

// farPane is far-pane's entry in near's window list, as list-windows --json
// gives it, and the whole output for a failure message.
func farPane(base string) (map[string]any, string) {
	out, err := tuiosOut(base, "list-windows", "-s", "near", "--json")
	if err != nil {
		return nil, out
	}
	var list struct {
		Windows []map[string]any `json:"windows"`
	}
	if json.Unmarshal([]byte(out), &list) != nil {
		return nil, out
	}
	for _, w := range list.Windows {
		if w["custom_name"] == "far-pane" {
			return w, out
		}
	}
	return nil, out
}

// restartBuild stops build's daemon and starts a new one, as a restart over
// there does: the panes it ran for this machine are gone.
func restartBuild(t *testing.T, remote string) {
	t.Helper()
	if out, err := tuiosCLI(t, remote, "kill-server"); err != nil {
		t.Fatalf("stop build's daemon: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, remote, "new", "far2", "--detach"); err != nil {
		t.Fatalf("start build's daemon again: %v\n%s", err, out)
	}
}

// endedRig is upgradeRemoteRig with the far machine's base kept, so a test
// can restart it.
func endedRig(t *testing.T) (base, remote string, env []string) {
	t.Helper()
	base = t.TempDir()
	remote = remoteMachine(t)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create a session on build: %v\n%s", err, out)
	}
	env = []string{"TUIOS_SSH=" + writeFakeSSHTo(t, base, remote)}
	writeOneHostConfig(t, base, tuiosBin)
	startHandoffDaemon(t, base, env...)
	newCommandWindow(t, base, "near", "anchor", false, "sh", "-c", "exec sleep 600")
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "build never came up")
	return base, remote, env
}

// TestAPaneWhoseDaemonRestartedReadsAsEnded: with no client attached, a pane
// on build reads as ended once build's daemon restarts. list-windows says
// exited with the reason, send-text fails with pane_ended naming build and
// why, and wait-for fails at once with the same. Negative control: drop the
// remotePaneEndedError case from ptyWriteError, and send-text answers
// "federation: stream is closed" as internal again.
func TestAPaneWhoseDaemonRestartedReadsAsEnded(t *testing.T) {
	base, remote, env := endedRig(t)
	openFarShell(t, base, env, "far-pane")
	restartBuild(t, remote)

	waitUntil(t, "list-windows shows far-pane ended", uiTimeout*2, func() (bool, string) {
		w, out := farPane(base)
		return w != nil && w["exited"] == true && w["host_link"] == "ended", out
	})
	if w, out := farPane(base); w == nil || !strings.Contains(fmt.Sprint(w["host_ended"]), "no longer has it") {
		t.Errorf("ASSERTION: list-windows does not say why far-pane ended:\n%s", out)
	}

	_, err := callVerb(t, base, "send-text", map[string]any{"session": "near", "window": "far-pane", "text": "echo AFTER\n"})
	if err == nil {
		t.Fatal("ASSERTION: send-text to a pane that ended on build succeeded")
	}
	if !containsAll(err.Error(), "pane_ended", "the pane on build ended", "no longer has it") {
		t.Errorf("ASSERTION: send-text does not say the pane on build ended, and why: %v", err)
	}

	start := time.Now()
	_, err = callVerb(t, base, "wait-for", map[string]any{"session": "near", "window": "far-pane", "condition": "window-output", "pattern": "NEVER-PRINTED", "timeout": 30000})
	took := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "pane_ended") {
		t.Errorf("ASSERTION: wait-for on the ended pane did not fail with pane_ended: %v", err)
	}
	if took > 10*time.Second {
		t.Errorf("ASSERTION: wait-for on the ended pane took %s, as if it waited out its timeout", took)
	}
}

// TestAPersonIsToldWhenAPaneOnAnotherMachineEnds: with a client attached, the
// person sees one notice naming the pane, build and why when build's daemon
// restarts. Negative control: return early from noteHostEnded, and no notice
// appears.
func TestAPersonIsToldWhenAPaneOnAnotherMachineEnds(t *testing.T) {
	base, remote, env := endedRig(t)
	openFarShell(t, base, env, "far-pane")
	term := startIn(t, base, startOpts{args: []string{"attach", "near"}, env: env, cols: 140, rows: 32})
	if err := term.WaitForText("far-pane", uiTimeout); err != nil {
		t.Fatalf("the client never drew the session: %v\n%s", err, term.Snapshot())
	}

	restartBuild(t, remote)
	if err := term.WaitForText("far-pane on build ended", uiTimeout*2); err != nil {
		t.Fatalf("ASSERTION: the person was not told that far-pane on build ended: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "remote-pane-ended-notice")
	alive(t, term, "after the notice")
}

// TestAPaneWhoseMachineIsDownIsNotEnded: with build's daemon stopped and not
// started again, the pane waits for it: list-windows says reconnecting and
// never ended, and send-text says the link is down, not pane_ended. Negative
// control: call end on the first failed reattach attempt, and the pane reads
// as ended at once.
func TestAPaneWhoseMachineIsDownIsNotEnded(t *testing.T) {
	base, remote, env := endedRig(t)
	openFarShell(t, base, env, "far-pane")
	if out, err := tuiosCLI(t, remote, "kill-server"); err != nil {
		t.Fatalf("stop build's daemon: %v\n%s", err, out)
	}
	waitUntil(t, "far-pane says reconnecting", uiTimeout*2, func() (bool, string) {
		w, out := farPane(base)
		return w != nil && w["host_link"] == "reconnecting", out
	})
	stayTrue(t, "far-pane is not called ended while build is down", 5*time.Second, func() (bool, string) {
		w, out := farPane(base)
		return w != nil && w["host_ended"] == nil && w["exited"] == nil, out
	})
	_, err := callVerb(t, base, "send-text", map[string]any{"session": "near", "window": "far-pane", "text": "echo X\n"})
	if err == nil || strings.Contains(err.Error(), "pane_ended") || !strings.Contains(err.Error(), "reconnecting") {
		t.Errorf("ASSERTION: send-text while build is down should say it is reconnecting: %v", err)
	}
}

// TestAnOrdinaryExitOnAnotherMachineIsNotEnded: a program on build that
// exits by itself, with the link up and build's daemon running, is an
// ordinary end. The kept-open pane shows the plain note and list-windows
// gives no host_ended: nothing warns. Negative control: drop the first-
// attempt EOF case in reconnect, and the pane reads as ended.
func TestAnOrdinaryExitOnAnotherMachineIsNotEnded(t *testing.T) {
	base, _, env := endedRig(t)
	if out, err := tuiosCLIEnv(t, base, env, "new-window", "far-pane", "-s", "near", "--host", "build", "--keep-open",
		"--", "/bin/sh", "-c", "echo FAR-RAN; exit 0"); err != nil {
		t.Fatalf("open far-pane on build: %v\n%s", err, out)
	}
	waitUntil(t, "far-pane shows its exit note", uiTimeout*2, func() (bool, string) {
		out, _ := tuiosOut(base, "capture-pane", "-s", "near", "-w", "far-pane")
		return strings.Contains(out, "The program ended."), out
	})
	stayTrue(t, "far-pane is not called ended", 3*time.Second, func() (bool, string) {
		w, out := farPane(base)
		return w != nil && w["host_ended"] == nil && w["host_link"] == nil, out
	})
}
