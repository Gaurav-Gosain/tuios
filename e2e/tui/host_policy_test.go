package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// What another machine may do here, a pane that outlives a dropped link, and
// mail that waits for a machine whose link is down, through the real
// transport: the ssh stand-in runs the real stdio-proxy against a second real
// daemon. The stand-in below can also be switched off with a file, which is a
// network that is gone: every dial fails the way ssh does when the machine
// cannot be reached, until the file is removed.

// writeGatedFakeSSH is writeFakeSSHTo with a gate: while gate exists, the
// stand-in fails as ssh does for a machine it cannot reach.
func writeGatedFakeSSH(t *testing.T, dir, remoteBase, gate string) string {
	t.Helper()
	inner := writeFakeSSHTo(t, dir, remoteBase)
	path := filepath.Join(dir, "fake-ssh-gated")
	body := "#!/bin/sh\n" +
		"if [ -f " + gate + " ]; then echo 'ssh: connect to host buildbox port 22: Network is unreachable' >&2; exit 255; fi\n" +
		"exec " + inner + " \"$@\"\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write the gated ssh stand-in: %v", err)
	}
	return path
}

// dumpLinkLogs logs both daemons' logs, for a failure that happened between
// them.
func dumpLinkLogs(t *testing.T, base, remote string) {
	t.Helper()
	for name, root := range map[string]string{"this machine": base, "build": remote} {
		path := filepath.Join(xdgDir(root, "XDG_STATE_HOME"), "tuios", "daemon.log")
		data, _ := os.ReadFile(path)
		t.Logf("%s, %s:\n%s", name, path, data)
	}
}

// writeRemoteConfig writes the far machine's config.toml before its daemon
// starts.
func writeRemoteConfig(t *testing.T, remote, body string) {
	t.Helper()
	dir := filepath.Join(xdgDir(remote, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir the far config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write the far config: %v", err)
	}
}

// cutTheLink closes the gate and kills the link's proxy, so the link drops
// and cannot be dialled again until openTheLink.
func cutTheLink(t *testing.T, gate string) {
	t.Helper()
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatalf("close the gate: %v", err)
	}
	breakTheLink(t)
}

func openTheLink(t *testing.T, gate string) {
	t.Helper()
	if err := os.Remove(gate); err != nil {
		t.Fatalf("open the gate: %v", err)
	}
}

// TestAPaneOnAnotherMachineOutlivesALinkDrop: the link to build drops under a
// window whose process runs there. The window stays, says reconnecting on its
// frame, and when the link is back what the process printed meanwhile is on
// screen and typing reaches it again.
//
// Negative control: with open-pane's grace forced to zero the window closes
// when the link drops, and the first wait after the cut fails.
func TestAPaneOnAnotherMachineOutlivesALinkDrop(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	gate := filepath.Join(base, "link-down")
	flag := filepath.Join(base, "print-now")
	ssh := writeGatedFakeSSH(t, base, remote, gate)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")

	script := "echo READY; while [ ! -f " + flag + " ]; do sleep 0.1; done; echo DURING-THE-DROP; exec cat"
	if out, err := tuiosCLIEnv(t, base, env, "new-window", "deploy", "-s", "home", "--host", "build", "--", "/bin/sh", "-c", script); err != nil {
		t.Fatalf("create a window on build: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "READY", "build:deploy")
	}, uiTimeout); err != nil {
		t.Fatalf("the far pane never appeared: %v\n%s", err, term.Snapshot())
	}

	before, err := daemonWindows(base, "home")
	if err != nil {
		t.Fatalf("list the session's windows: %v", err)
	}
	cutTheLink(t, gate)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return contains(s.Text(), "[reconnecting] build")
	}, uiTimeout*2); err != nil {
		t.Fatalf("ASSERTION: the window's frame never said its link was lost: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "hosted-pane-reconnecting")
	wl, err := daemonWindows(base, "home")
	if err != nil || len(wl.Windows) < len(before.Windows) {
		dumpLinkLogs(t, base, remote)
		t.Fatalf("ASSERTION: the window went with the link: %v %+v", err, wl)
	}
	// The process goes on printing while nobody is attached.
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	openTheLink(t, gate)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return contains(text, "DURING-THE-DROP") && !contains(text, "[reconnecting]")
	}, uiTimeout*6); err != nil {
		t.Fatalf("ASSERTION: the pane never came back with what it printed during the drop: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "hosted-pane-reattached")
	// Typed the way an agent types, so the keys go to the pane whatever mode
	// the client is in.
	if out, err := tuiosCLIEnv(t, base, env, "send-text", "-s", "home", "-w", "deploy", "typed-after-the-drop\n"); err != nil {
		t.Fatalf("type into the pane: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), "typed-after-the-drop") >= 2
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: typing after the reattach did not reach the far process: %v\n%s", err, term.Snapshot())
	}
}

// TestMailWaitsForAMachineWhoseLinkIsDown: a message for build while its link
// is down is kept here, the Inbox and tuios hosts say so, and it arrives on
// build when the link is back.
//
// Negative control: with the outbox kick cut from the fleet's status report
// the message never arrives and the last wait fails.
func TestMailWaitsForAMachineWhoseLinkIsDown(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	gate := filepath.Join(base, "link-down")
	ssh := writeGatedFakeSSH(t, base, remote, gate)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")

	cutTheLink(t, gate)
	deadline := time.Now().Add(30 * time.Second)
	for {
		raw, _ := tuiosCLI(t, base, "hosts", "--json")
		if contains(raw, `"host": "build"`) && !contains(raw, `"status": "up"`) && !contains(raw, `"status":"up"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("build never went down:\n%s", raw)
		}
		time.Sleep(250 * time.Millisecond)
	}

	out, err := tuiosCLIEnv(t, base, env, "send-agent-message", "-s", "build:far", "-w", "human", "--from", "PLANNER", "while you were away")
	if err != nil {
		t.Fatalf("ASSERTION: a message for a machine whose link is down failed rather than waiting: %v\n%s", err, out)
	}
	if !contains(out, "waits here") {
		t.Fatalf("ASSERTION: the send did not say the message waits:\n%s", out)
	}
	hosts, _ := tuiosCLIEnv(t, base, env, "hosts")
	if !contains(hosts, "build: 1 message(s) wait here for the link") {
		t.Errorf("tuios hosts does not say mail waits:\n%s", hosts)
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return containsAll(s.Text(), "Waiting to send", "for build", "1 message waits for the link to build")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the Inbox does not say mail waits for build: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "outbox-inbox")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}

	openTheLink(t, gate)
	deadline = time.Now().Add(uiTimeout * 6)
	for {
		far, _ := tuiosCLI(t, remote, "read-agent-messages", "-s", "far", "-w", "human", "--peek")
		if contains(far, "while you were away") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the queued message never reached build:\n%s", far)
		}
		time.Sleep(250 * time.Millisecond)
	}
	for {
		if att, _ := tuiosCLIEnv(t, base, env, "list-attention"); !contains(att, "Waiting to send") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ASSERTION: the Inbox still says mail waits after it was delivered")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestAMachineHoldsALinkToItsPolicy: build lets machines linked to it list and
// nothing else, and holds their mail for its person. From here a listing
// works, typing into a pane there is refused with the capability named, and
// mail is held.
//
// Negative control: with the policy check cut from dispatchVerbLine the
// send-text goes through and the refusal assertion fails.
func TestAMachineHoldsALinkToItsPolicy(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = [\"list\", \"mail\"]\nhold_mail = true\n")
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	env := hubWithBuild(t, base, remote)

	if out, err := tuiosCLIEnv(t, base, env, "list-windows", "-s", "build:far"); err != nil {
		t.Fatalf("a listing on build was refused: %v\n%s", err, out)
	}
	out, err := tuiosCLIEnv(t, base, env, "send-text", "-s", "build:far", "echo should-not-run")
	if err == nil {
		t.Fatalf("ASSERTION: typing into a pane on a machine that allows only list went through:\n%s", out)
	}
	// The CLI wraps its message to the terminal, so words are compared, not
	// lines.
	if flat := strings.Join(strings.Fields(out), " "); !contains(flat, "may not write on") {
		t.Errorf("the refusal does not name what is missing:\n%s", out)
	}

	wl, err := daemonWindows(remote, "far")
	if err != nil || len(wl.Windows) == 0 {
		t.Fatalf("list the far windows: %v", err)
	}
	out, err = tuiosCLIEnv(t, base, env, "send-agent-message", "-s", "build:far", "-w", wl.Windows[0].ID, "--json", "run the migration")
	if err != nil {
		t.Fatalf("mail to build was refused: %v\n%s", err, out)
	}
	if !contains(out, `"held": true`) && !contains(out, `"held":true`) {
		t.Errorf("ASSERTION: mail from a machine whose mail build holds was not held:\n%s", out)
	}
	far, _ := tuiosCLI(t, remote, "list-attention", "--json")
	if !contains(far, "held_for") {
		t.Errorf("ASSERTION: build's Inbox does not offer the held mail:\n%s", far)
	}
}
