package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The fleet end to end: what waits on another machine reaches this machine's
// Inbox and dock without a poll, and an agent in a pane whose process runs on
// another machine reports its state with the ordinary command.
//
// build is a second daemon with its own runtime directory, reached over the
// real subprocess transport through the ssh stand-in, as in the other
// federation tests.

// waitForAttention polls this machine's list-attention until it holds want.
func waitForAttention(t *testing.T, base string, env []string, want ...string) string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout * 3)
	var out string
	for time.Now().Before(deadline) {
		out, _ = tuiosCLIEnv(t, base, env, "list-attention")
		if containsAll(out, want...) {
			return out
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("list-attention never held %q:\n%s", want, out)
	return out
}

// TestAnAgentOnAnotherMachineReachesThisInbox is P17: an agent on build that
// errors is in this machine's Inbox, named build:far, raises the dock here,
// and is listed by the leader chord i.
//
// Negative control: with the fleet's start cut from Daemon.Start nothing of
// build reaches this machine's Inbox and the first wait fails.
func TestAnAgentOnAnotherMachineReachesThisInbox(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool {
		return containsAll(s, "build", "up")
	}, "the daemon never reported build up")

	if out, err := tuiosCLI(t, remote, "set-agent-state", "-s", "far", "errored", "-m", "rate limited by the api"); err != nil {
		t.Fatalf("set-agent-state on build: %v\n%s", err, out)
	}
	out := waitForAttention(t, base, env, "build:far", "rate limited by the api")
	t.Logf("list-attention here:\n%s", out)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "build:far:")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the dock never announced the agent on build: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "fleet-dock")

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Errored 1") && strings.Contains(text, "build:far")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the Inbox does not list the agent on build: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "fleet-inbox")

	// The agent on build moves on, and the item leaves this Inbox too.
	if out, err := tuiosCLI(t, remote, "set-agent-state", "-s", "far", "working"); err != nil {
		t.Fatalf("set-agent-state working on build: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout * 3)
	for {
		out, _ = tuiosCLIEnv(t, base, env, "list-attention")
		if !strings.Contains(out, "build:far") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the item stayed after the agent on build moved on:\n%s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	alive(t, term, "after the fleet Inbox round trip")
}

// TestAnAgentInAPaneOnAnotherMachineReportsHere is P18: a pane of this
// machine's session whose process runs on build reports its state with
// 'tuios set-agent-state -w "$TUIOS_PANE_ID"', run on build, and the approval
// lands in this machine's Inbox on this machine's window.
//
// Negative control: with the forward cut from dispatchVerbLine, build's daemon
// answers the report itself and fails, the pane prints a nonzero status, and
// the Inbox here stays empty.
func TestAnAgentInAPaneOnAnotherMachineReportsHere(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool {
		return containsAll(s, "build", "up")
	}, "the daemon never reported build up")

	script := tuiosBin + ` set-agent-state -w "$TUIOS_PANE_ID" needs_input --kind approval -m "approve Bash: make deploy"; echo "REPORTED $?"; sleep 600`
	if out, err := tuiosCLIEnv(t, base, env, "new-window", "hosted", "-s", "home",
		"--host", "build", "--", "/bin/sh", "-c", script); err != nil {
		t.Fatalf("create a window on build: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "REPORTED")
	}, uiTimeout*2); err != nil {
		t.Fatalf("the hosted pane never ran its report: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Snapshot(), "REPORTED 0") {
		t.Fatalf("ASSERTION: the report from the hosted pane failed:\n%s", term.Snapshot())
	}
	out := waitForAttention(t, base, env, "Approvals", "home/", "approve Bash: make deploy")
	if strings.Contains(out, "build:") {
		t.Errorf("ASSERTION: the report was filed as build's, but the window is this machine's:\n%s", out)
	}
	saveFrame(t, term, "fleet-hosted-report")
	alive(t, term, "after a hosted pane reported")
}
