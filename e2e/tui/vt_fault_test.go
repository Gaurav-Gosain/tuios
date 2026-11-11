package tuie2e

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// A panic in one pane's emulator must not end the daemon. The daemon here is
// started with TUIOS_TEST_VT_PANIC, so its emulator loop panics on a chunk
// that holds the marker (internal/session/pane_vt_fault.go). The failure
// modes this checks are in the untangle notes, design/vt-loop-recover.md.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "A panic in a pane's
// emulator"): with the recover in feedVT removed, the daemon dies on the first
// marker and waitNoticeOrDeath fails.

const vtPanicMarker = "VTZPANIC"

var vtFaultNoticeRE = regexp.MustCompile(`terminal emulator of this pane failed`)

func TestEmulatorPanicKeepsTheDaemonAndOtherPanes(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base, "TUIOS_TEST_VT_PANIC="+vtPanicMarker)
	const sess = "vtfault"
	echo := `while read l; do printf 'got-%s\n' "$l"; done`
	a := newCommandWindow(t, base, sess, "a", false, "sh", "-c", echo)
	b := newCommandWindow(t, base, sess, "b", false, "sh", "-c", echo)
	send := func(w, text string) {
		t.Helper()
		_ = mustCall(t, base, "send-text", map[string]any{"session": sess, "window": w, "text": text + "\n"})
	}
	send(a, "before")
	waitHandoffPane(t, base, sess, a, regexp.MustCompile(`^got-before`), 10*time.Second)
	client := attachIn(t, base, sess, startOpts{cols: bigCols, rows: bigRows})
	if err := client.WaitForText("got-before", bootTimeout); err != nil {
		t.Fatalf("the client does not show the pane: %v\n%s", err, client.Snapshot())
	}
	pid := helloImage(t, base).PID

	// One fault: the pane's screen is reset with a notice, everywhere.
	send(a, vtPanicMarker)
	waitNoticeOrDeath(t, base, sess, a)
	if now := helloImage(t, base).PID; now != pid {
		t.Fatalf("ASSERTION: the daemon's pid changed from %d to %d: a pane's emulator fault ended it", pid, now)
	}
	if err := client.WaitForText("pane failed", uiTimeout); err != nil {
		t.Errorf("ASSERTION: the attached client does not show the notice: %v\n%s", err, client.Snapshot())
	}
	send(b, "other")
	waitHandoffPane(t, base, sess, b, regexp.MustCompile(`^got-other`), 10*time.Second)
	send(a, "after")
	waitHandoffPane(t, base, sess, a, regexp.MustCompile(`^got-after`), 10*time.Second)

	// A client attaching now gets the daemon's rebuilt screen, and it holds
	// what the first client shows: the notice and the output after it, and
	// nothing from before the reset.
	late := attachIn(t, base, sess, startOpts{cols: bigCols, rows: bigRows})
	if err := late.WaitForText("got-after", bootTimeout); err != nil {
		t.Fatalf("ASSERTION: a client attaching after the fault does not show the pane's new output: %v\n%s", err, late.Snapshot())
	}
	if strings.Contains(late.Snapshot(), "got-before") {
		t.Errorf("ASSERTION: the late client shows output from before the reset, which the first client no longer shows\n%s", late.Snapshot())
	}

	// Two more faults within the window: the daemon stops feeding the
	// pane's emulator, and the program and the stream go on.
	send(a, vtPanicMarker)
	waitHandoffPane(t, base, sess, a, vtFaultNoticeRE, 10*time.Second)
	send(a, vtPanicMarker)
	waitHandoffPane(t, base, sess, a, regexp.MustCompile(`failed again`), 10*time.Second)
	send(a, "frozen")
	if err := client.WaitForText("got-frozen", uiTimeout); err != nil {
		t.Errorf("ASSERTION: the client stopped drawing the pane's stream after the emulator froze: %v\n%s", err, client.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
	if lines, _ := daemonPane(base, sess, a); strings.Contains(strings.Join(lines, "\n"), "got-frozen") {
		t.Errorf("ASSERTION: the daemon still feeds a pane whose emulator failed three times in a minute")
	}
	send(b, "still")
	waitHandoffPane(t, base, sess, b, regexp.MustCompile(`^got-still`), 10*time.Second)
	if now := helloImage(t, base).PID; now != pid {
		t.Fatalf("ASSERTION: the daemon's pid changed from %d to %d after three faults", pid, now)
	}
}

// waitNoticeOrDeath waits for the fault notice in the pane, and fails at once
// when the daemon stops answering instead.
func waitNoticeOrDeath(t *testing.T, base, sess, window string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := callVerb(t, base, "hello", map[string]any{}); err != nil {
			t.Fatalf("ASSERTION: the daemon is gone after one pane's emulator panicked: %v", err)
		}
		if lines, err := daemonPane(base, sess, window); err == nil && vtFaultNoticeRE.MatchString(strings.Join(lines, "\n")) {
			return
		}
	}
	t.Fatalf("ASSERTION: the pane never showed the fault notice")
}
