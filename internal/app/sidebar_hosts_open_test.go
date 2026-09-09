package app

import (
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// TestRemoteOpenArgvRunsThisTuiosWithHold pins the pane's command: this
// machine's own tuios, with 'attach --host' and --hold, so the pane reuses the
// CLI path and stays open on a failure long enough to read it.
func TestRemoteOpenArgvRunsThisTuiosWithHold(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("no executable path in this environment: %v", err)
	}
	argv, err := remoteOpenArgv("attach", "build", "api")
	if err != nil {
		t.Fatalf("remoteOpenArgv: %v", err)
	}
	want := []string{exe, "attach", "--host", "build", "api", "--hold"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Errorf("ASSERTION: attach argv is %v, want %v", argv, want)
	}

	argv, err = remoteOpenArgv("new", "build", "")
	if err != nil {
		t.Fatalf("remoteOpenArgv new: %v", err)
	}
	want = []string{exe, "new", "--host", "build", "--hold"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Errorf("ASSERTION: new argv is %v, want %v", argv, want)
	}
}

// TestOpenRemoteSessionOnADownHostSaysUnavailable is #170's toast: an action
// aimed at a host that is not up says so, rather than opening a pane that will
// only fail. The positive half is the up-host row being a target, proved in
// sidebar_hosts_test.go.
func TestOpenRemoteSessionOnADownHostSaysUnavailable(t *testing.T) {
	m := cachedDownHostOS(t) // "build" up, "stale" connecting
	before := len(m.Windows)

	m.openRemoteSession("stale", "old")
	if len(m.Windows) != before {
		t.Errorf("ASSERTION: opening a session on a host that is not up added a pane")
	}
	if len(m.Notifications) == 0 || !strings.Contains(m.Notifications[len(m.Notifications)-1].Message, "stale is unavailable") {
		t.Errorf("ASSERTION: opening a session on a down host did not say the host is unavailable: %+v", m.Notifications)
	}

	m.createRemoteSession("stale")
	if len(m.Notifications) == 0 || !strings.Contains(m.Notifications[len(m.Notifications)-1].Message, "stale is unavailable") {
		t.Errorf("ASSERTION: creating a session on a down host did not say the host is unavailable: %+v", m.Notifications)
	}

	// Sanity: the fixture really did have an up host, so the guard is what
	// stopped the down one, not a fixture with no up host at all.
	if !m.hostIsUp("build") {
		t.Fatal("the fixture's up host is not up, so this proves nothing")
	}
	_ = federation.StatusUp
}
