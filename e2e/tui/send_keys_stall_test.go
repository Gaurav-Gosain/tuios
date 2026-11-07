package tuie2e

import (
	"testing"
	"time"
)

// TestSendKeysDoNotWaitOnAKeysTimer: send-keys hands its keys to the
// attached client one at a time, and the next key waited for every command
// the key before it started. The agents view arms a minute tick when it
// opens, so a send-keys of ctrl+b i waited a minute for the tick, and the CLI
// timed out with "timed out waiting for the attached client". The send must
// answer at once, the view must be open, and a key after it must still go
// through.
//
// The negative control is in NEGATIVE_CONTROLS.md, "A key's timer holds
// send-keys".
func TestSendKeysDoNotWaitOnAKeysTimer(t *testing.T) {
	base := t.TempDir()
	approvalSession(t, base, 1)
	art := artifactDir(t)
	term := startIn(t, base, startOpts{args: []string{"attach", "work"}})
	if err := term.WaitForText("$", bootTimeout); err != nil {
		t.Fatalf("no shell prompt: %v\n%s", err, term.Snapshot())
	}
	start := time.Now()
	out, err := tuiosCLI(t, base, "send-keys", "-s", "work", "ctrl+b i")
	took := time.Since(start)
	if err != nil {
		t.Fatalf("send-keys of ctrl+b i failed after %v: %v\n%s", took.Round(time.Millisecond), err, out)
	}
	if took > 5*time.Second {
		t.Fatalf("send-keys of ctrl+b i took %v", took.Round(time.Millisecond))
	}
	if err := term.WaitForText("Search agents", uiTimeout); err != nil {
		t.Fatalf("the agents view did not open: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, art, "stall-agents-view")
	start = time.Now()
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "work", "Escape"); err != nil || time.Since(start) > 5*time.Second {
		t.Fatalf("send-keys of Escape after it: %v after %v\n%s", err, time.Since(start).Round(time.Millisecond), out)
	}
	if err := term.WaitForText("$", uiTimeout); err != nil {
		t.Fatalf("Escape did not close the agents view: %v\n%s", err, term.Snapshot())
	}
	t.Logf("ctrl+b i answered in %v", took.Round(time.Millisecond))
}
