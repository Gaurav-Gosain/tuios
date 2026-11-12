package tuie2e

import (
	"strings"
	"testing"
	"time"
)

// A live upgrade and the panes of this daemon's sessions that run on another
// machine. Such a pane has no terminal here to hand over. A running one
// refuses the upgrade, and the refusal names every one at once. An exited one
// holds nothing to keep: the upgrade closes it and says so, before the new
// binary starts and in its result line.
//
// How these could pass wrongly, written down first:
//   - The pane on build could be hosted by this same daemon (a link to
//     itself), and the hosted-pane refusal would answer instead. The rig links
//     to a second daemon of its own.
//   - The upgrade could run before the pane's exit is recorded and be refused
//     for a running pane. The test waits for the exit note the pane prints.
//   - The refusal could name one pane per try and still contain the word
//     "another machine". Both panes' names and sessions must be in one answer.

// upgradeRemoteRig is a daemon here that can upgrade live, with a session
// "near" holding a local pane, linked to a second machine's daemon as build.
// It returns the base, the environment that reaches build, a second tuios
// binary to upgrade to, and the local pane's window id.
func upgradeRemoteRig(t *testing.T) (base string, env []string, second, anchor string) {
	t.Helper()
	base = t.TempDir()
	remote := remoteMachine(t)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create a session on build: %v\n%s", err, out)
	}
	env = []string{"TUIOS_SSH=" + writeFakeSSHTo(t, base, remote)}
	writeOneHostConfig(t, base, tuiosBin)
	startHandoffDaemon(t, base, env...)
	second = copyTuios(t)
	anchor = newCommandWindow(t, base, "near", "anchor", false, "sh", "-c", "exec sleep 600")
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") },
		"build never came up")
	return base, env, second, anchor
}

// openOnBuild opens a kept-open window on build in session, running script.
func openOnBuild(t *testing.T, base string, env []string, session, name, script string) {
	t.Helper()
	if out, err := tuiosCLIEnv(t, base, env, "new-window", name, "-s", session, "--host", "build", "--keep-open",
		"--", "/bin/sh", "-c", script); err != nil {
		t.Fatalf("open %s on build: %v\n%s", name, err, out)
	}
}

// TestUpgradeClosesAnExitedPaneOnAnotherMachine: a window on build whose
// program has exited no longer refuses a live upgrade. The upgrade closes it,
// names it in the output before the new binary starts, and its result line
// counts it; the local pane keeps its process. Negative control: count an
// exited pane as running in remotePaneRefusal, and the upgrade is refused
// naming gone-far.
func TestUpgradeClosesAnExitedPaneOnAnotherMachine(t *testing.T) {
	base, env, second, anchor := upgradeRemoteRig(t)
	openOnBuild(t, base, env, "near", "gone-far", "echo FAR-RAN; exit 3")
	waitUntil(t, "the pane on build records its exit", uiTimeout*2, func() (bool, string) {
		out, _ := tuiosOut(base, "capture-pane", "-s", "near", "-w", "gone-far")
		return strings.Contains(out, "The program ended."), out
	})
	before := panePIDs(t, base, "near")[anchor]

	text, err := handoffCLI(t, base, 90*time.Second, "daemon", "upgrade", "--binary", second)
	writeHandoffArtifact(t, map[string]any{"upgrade": text})
	if err != nil {
		t.Fatalf("ASSERTION: the upgrade was refused over an exited pane on another machine: %v\n%s", err, text)
	}
	if !containsAll(text, "Closed 1 exited pane on other machines", "near: gone-far", "on build") {
		t.Errorf("ASSERTION: the upgrade did not say which exited pane it closed:\n%s", text)
	}
	if !strings.Contains(text, "1 exited pane on other machines closed.") {
		t.Errorf("ASSERTION: the result line does not count the closed pane:\n%s", text)
	}
	out, err := tuiosCLI(t, base, "list-windows", "-s", "near", "--json")
	if err != nil {
		t.Fatalf("list-windows after the upgrade: %v\n%s", err, out)
	}
	if strings.Contains(out, "gone-far") {
		t.Errorf("ASSERTION: the exited pane on build is still listed after the upgrade:\n%s", out)
	}
	if after := panePIDs(t, base, "near")[anchor]; after != before || before == 0 {
		t.Errorf("ASSERTION: the local pane's process changed across the upgrade: %d before, %d after", before, after)
	}
}

// TestUpgradeRefusalNamesEveryPaneOnAnotherMachine: with a running pane on
// build in each of two sessions, the refusal names both, with session and
// name, in one answer, and nothing changes. Negative control: return at the
// first pane in remotePaneRefusal, and only one name is in the refusal.
func TestUpgradeRefusalNamesEveryPaneOnAnotherMachine(t *testing.T) {
	base, env, second, anchor := upgradeRemoteRig(t)
	newCommandWindow(t, base, "other", "anchor2", false, "sh", "-c", "exec sleep 600")
	openOnBuild(t, base, env, "near", "far-a", "exec sleep 600")
	openOnBuild(t, base, env, "other", "far-b", "exec sleep 600")
	before := panePIDs(t, base, "near")[anchor]

	text, err := handoffCLI(t, base, 90*time.Second, "daemon", "upgrade", "--binary", second)
	writeHandoffArtifact(t, map[string]any{"upgrade": text})
	if err == nil {
		t.Fatalf("ASSERTION: the upgrade went ahead with running panes on another machine:\n%s", text)
	}
	if !containsAll(text, "2 panes run on another machine", "near: far-a", "other: far-b") {
		t.Errorf("ASSERTION: the refusal does not name both running panes on build at once:\n%s", text)
	}
	if after := panePIDs(t, base, "near")[anchor]; after != before || before == 0 {
		t.Errorf("ASSERTION: the refused upgrade changed the local pane's process: %d before, %d after", before, after)
	}
}
