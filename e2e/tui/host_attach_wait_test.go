package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestAttachOnAHostSaysWhatItWaitsFor: while tuios attach --host waits for
// the link, the terminal says so. The knob hold_host_dial holds the first
// link attempt of the daemon on this machine.
//
// With the hold lifted, the far session is drawn. With the hold kept, the
// attach ends with an error that names the link and what to do.
//
// Negative control: on main the terminal shows only "Starting TUIOS
// daemon..." and the terminal probes, and the wait for the status line
// fails.
func TestAttachOnAHostSaysWhatItWaitsFor(t *testing.T) {
	setup := func(t *testing.T, session string) (string, []string, string) {
		t.Helper()
		base := t.TempDir()
		remote := remoteMachine(t)
		ssh := writeFakeSSHTo(t, base, remote)
		writeOneHostConfig(t, base, tuiosBin)
		if out, err := tuiosCLI(t, remote, "new", session, "--detach"); err != nil {
			t.Fatalf("create the far session: %v\n%s", err, out)
		}
		hold := filepath.Join(base, "hold-dial")
		if err := os.WriteFile(hold, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(hold) })
		return base, []string{"TUIOS_SSH=" + ssh, knob("hold_host_dial=" + hold)}, hold
	}
	const status = "Connecting to build. Waiting for the link..."

	t.Run("link comes", func(t *testing.T) {
		base, env, hold := setup(t, "far-wait")
		term := startIn(t, base, startOpts{args: []string{"attach", "--host", "build", "far-wait"}, env: env})
		if err := term.WaitForText(status, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the terminal did not say it waits for the link: %v\n%s", err, term.Snapshot())
		}
		if strings.Contains(term.Screen().Text(), "╰──") {
			t.Fatalf("the far session was drawn while the link was held:\n%s", term.Snapshot())
		}
		if err := os.Remove(hold); err != nil {
			t.Fatal(err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Contains(s.Text(), "╰──")
		}, bootTimeout); err != nil {
			t.Fatalf("ASSERTION: the client never drew the far session after the link came: %v\n%s", err, term.Snapshot())
		}
	})

	t.Run("link never comes", func(t *testing.T) {
		base, env, _ := setup(t, "far-never")
		term := startIn(t, base, startOpts{args: []string{"attach", "--host", "build", "far-never"}, env: env})
		if err := term.WaitForText(status, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the terminal did not say it waits for the link: %v\n%s", err, term.Snapshot())
		}
		// This machine's daemon gives the link 15 seconds and then refuses
		// the connection. The wait is that budget and room.
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			text := s.Text()
			return strings.Contains(text, "The link to build did not come up in 15 seconds.") && strings.Contains(text, "tuios hosts")
		}, 25*time.Second); err != nil {
			t.Fatalf("ASSERTION: the attach did not end with an error about the link and what to do: %v\n%s", err, term.Snapshot())
		}
		t.Logf("the attach after the link budget ran out:\n%s", term.Snapshot())
	})
}
