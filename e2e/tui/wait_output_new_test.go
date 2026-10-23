package tuie2e

import (
	"strings"
	"testing"
	"time"
)

// wait-for window-output --new matches only output written after the wait
// starts. A marker already on the screen must not end it, and the same marker
// printed again afterwards must.
//
// How this could pass wrongly, written down first:
//   - The --new wait could time out because the marker was never on the
//     screen at all. A plain wait for the same pattern runs first and must
//     match at once, so the marker is proved to be there.
//   - It could time out because --new never matches anything. The positive
//     half waits with --new for the same pattern and prints the marker again,
//     and that wait must match.
//   - The positive half could match the shell's echo of the typed command.
//     The command prints DONE-%s with the number as an argument, so the echo
//     never holds the text DONE-7 the pattern asks for.
//   - The positive half could match because the marker was printed before the
//     wait was set up and new were ignored. The negative half catches that
//     case on its own: an ignored new matches the old marker at once.
//   - The positive half could race the wait's setup. The command sleeps a
//     second before it prints, and the wait is started first.
//
// Negative control: with the fresh branch cut from waitWindowOutput, the
// --new wait for the old marker matches at once and the test fails. On
// origin/main the flag does not exist and the wait exits with an error.
func TestWaitForWindowOutputNewIgnoresOldOutput(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "waitnew"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "0", "printf 'DONE-%s\\n' 7\n"); err != nil {
		t.Fatalf("send-text: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "wait-for", "window-output", "-s", sess, "-w", "0", "--pattern", "DONE-7", "--timeout", "10000"); err != nil {
		t.Fatalf("the marker never reached the screen: %v: %s", err, out)
	}

	// Negative half: the marker is on the screen, and --new must not see it.
	start := time.Now()
	out, err := tuiosCLI(t, base, "wait-for", "window-output", "-s", sess, "-w", "0", "--pattern", "DONE-7", "--new", "--timeout", "1500")
	if err == nil {
		t.Fatalf("ASSERTION: wait-for --new matched the marker that was already on the screen after %s: %s", time.Since(start), out)
	}
	if !strings.Contains(out, "timed out") {
		t.Fatalf("ASSERTION: wait-for --new failed for a reason other than the timeout: %v: %s", err, out)
	}

	// Positive half: the same marker, printed after the wait starts, matches.
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := tuiosCLI(t, base, "wait-for", "window-output", "-s", sess, "-w", "0", "--pattern", "DONE-7", "--new", "--json", "--timeout", "15000")
		done <- result{out, err}
	}()
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "0", "sleep 1; printf 'DONE-%s\\n' 7\n"); err != nil {
		t.Fatalf("send-text: %v: %s", err, out)
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("ASSERTION: wait-for --new missed the marker printed after it started: %v: %s", r.err, r.out)
	}
	if !strings.Contains(r.out, `"new": true`) && !strings.Contains(r.out, `"new":true`) {
		t.Fatalf("ASSERTION: the result does not say new was used: %s", r.out)
	}

	// The artifact: the pane holds the marker twice, once from before each wait.
	screen, _ := tuiosCLI(t, base, "capture-pane", "-s", sess, "-w", "0")
	if n := strings.Count(screen, "\nDONE-7"); n < 2 {
		t.Fatalf("want the marker printed twice on the pane, got %d:\n%s", n, screen)
	}
	t.Logf("pane after both waits:\n%s", screen)
}
