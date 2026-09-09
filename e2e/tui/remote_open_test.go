package tuie2e

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Creating a session on another machine from the command line, and the ssh
// fallback for opening one.
//
// The link is the same loopback the other federation tests use. The ssh
// stand-in drops ssh's options and the address and runs the command locally, so
// the host named "build" is this test's own daemon reached over the real
// subprocess transport. Nothing reads the developer's ssh config, and no
// network connection is made. The rail's own path, against a second daemon,
// is in host_attach_test.go.

// TestNewOnAHostCreatesASessionAcrossTheLink is the CLI reachability proof for
// 'tuios new --host'. The far side creates the session, and it comes back in
// both the local listing and the host's own listing.
func TestNewOnAHostCreatesASessionAcrossTheLink(t *testing.T) {
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	env := []string{"TUIOS_SSH=" + ssh}
	writeHostsConfig(t, base, tuiosBin)

	term := startIn(t, base, startOpts{args: []string{"new", "fed-open"}, env: env})
	waitBoot(t, term)

	// The command a person types. --detach is what lets this run without a
	// terminal: the far side creates the session and returns.
	out, err := tuiosCLIEnv(t, base, env, "new", "--host", "build", "extra", "--detach")
	if err != nil {
		t.Fatalf("ASSERTION: 'tuios new --host build extra --detach' failed: %v\n%s", err, out)
	}
	t.Logf("tuios new --host build extra --detach:\n%s", out)

	// The session the far side created is in this daemon now, because the link
	// loops back to it. Poll the session listing until it lands.
	if err := waitForLS(t, base, func(out string) bool {
		return strings.Contains(out, "extra")
	}); err != nil {
		t.Fatalf("ASSERTION: the session created with 'new --host' never appeared: %v", err)
	}

	// And it is in the host's own listing, which is the listing that crosses the
	// link.
	out, err = tuiosCLIEnv(t, base, env, "ls", "--host", "build")
	if err != nil {
		t.Fatalf("tuios ls --host build: %v\n%s", err, out)
	}
	if !strings.Contains(out, "extra") {
		t.Errorf("ASSERTION: the session created with 'new --host' is not in the host listing:\n%s", out)
	}
	t.Logf("tuios ls --host build:\n%s", out)
}

// waitForLS polls 'tuios ls' until the output satisfies want.
func waitForLS(t *testing.T, base string, want func(string) bool) error {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out, _ = tuiosCLI(t, base, "ls")
		if want(out) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("the last 'tuios ls' was:\n%s", out)
}

// railShows blocks until the rail shows text.
func railShows(t *testing.T, term *tuitest.Terminal, want string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), want)
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never showed %q: %v\n%s", want, err, term.Snapshot())
	}
}

// TestAttachOnAHostWithSSHRunsTheFarTuios keeps the fallback reachable: with
// --ssh the session is opened by the tuios on the far side over an interactive
// ssh, nested in this terminal, the way it was before the link carried an
// attach. The far side here is this test's own daemon, so the nested client
// is a second 'tuios attach' process on the session.
func TestAttachOnAHostWithSSHRunsTheFarTuios(t *testing.T) {
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	env := []string{"TUIOS_SSH=" + ssh}
	writeHostsConfig(t, base, tuiosBin)

	if out, err := tuiosCLIEnv(t, base, env, "new", "nested-target", "--detach"); err != nil {
		t.Fatalf("create the session to open: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "--host", "build", "nested-target", "--ssh"}, env: env})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "╰──")
	}, bootTimeout); err != nil {
		t.Fatalf("the far tuios never drew the session: %v\n%s", err, term.Snapshot())
	}

	// The proof that ssh ran the far tuios: a plain 'tuios attach' process
	// for the session, which the link path never starts.
	out, _ := exec.Command("pgrep", "-af", tuiosBin).CombinedOutput()
	nested := false
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, " attach nested-target") && !strings.Contains(line, "--host") {
			nested = true
		}
	}
	if !nested {
		t.Fatalf("ASSERTION: --ssh did not run the far tuios; no nested client for nested-target:\n%s", out)
	}
	alive(t, term, "after opening a session over ssh")
}
