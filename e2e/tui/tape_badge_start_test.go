package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAShellThatStartsInAProjectShowsItsTape: tuios started in a project
// folder, standalone and as a daemon client, and a new pane whose shell starts
// there with no cd. The shell names its folder with OSC 7 at its first prompt,
// as fish does. The dock must show the tape badge.
//
// A daemon client primes a new pane from the daemon's copy of the screen, then
// reads its live output. A shell fast enough to print its first prompt before
// that copy put its OSC 7 into the copy only, which holds cells and no OSC 7,
// so the client never looked for the tape. The knob hold_pane_prime holds the
// priming until the daemon shows the prompt, which makes that order certain.
// The prompt shows "ready>" ahead of its OSC 7, so the test can see it.
//
// Negative control: with detection keyed on an OSC 7 the client parsed, the
// daemon case shows no badge.
func TestAShellThatStartsInAProjectShowsItsTape(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			writeTapeConfigFile(t, base, "[tape]\nautorun = \"ask\"\n")
			dir := writeProjectTape(t, base, "proj-start", "Type \"echo SHOULD_NOT_RUN\" Enter\n")
			hold := filepath.Join(t.TempDir(), "hold-pane-prime")
			if err := os.WriteFile(hold, nil, 0o644); err != nil {
				t.Fatalf("hold file: %v", err)
			}
			const session = "projstart"
			args := []string{"--standalone"}
			if daemon {
				args = []string{"new", session}
			}
			term := startIn(t, base, startOpts{cols: 120, rows: 40, dir: dir, daemonDefault: daemon,
				args: args,
				env: []string{knob("hold_pane_prime=" + hold),
					"PS1=ready> \x1b]7;file://localhost" + dir + "\x1b\\"}})
			waitBoot(t, term)
			if !daemon {
				newWindow(t, term)
			} else {
				// The key, not newWindow: the client is held before it primes
				// the new pane, so the pane is not on screen until the release.
				_ = settledWindowCount(t, term)
				if err := term.SendKeys("n"); err != nil {
					t.Fatalf("send n: %v", err)
				}
				waitUntil(t, "the daemon shows the first prompt", uiTimeout, func() (bool, string) {
					out, err := tuiosCLI(t, base, "capture-pane", "-s", session)
					return strings.Contains(out, "ready>"), fmt.Sprintf("capture-pane: %v\n%s", err, out)
				})
			}
			if err := os.Remove(hold); err != nil {
				t.Fatalf("release the priming: %v", err)
			}
			if err := term.WaitForText("tape ?", uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the pane's folder holds a tape and the dock shows no badge: %v\n%s", err, term.Snapshot())
			}
		})
	}
}
