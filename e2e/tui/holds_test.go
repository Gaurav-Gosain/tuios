package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// paneRun types script into the pane id, with its output and exit status
// written to name.out once it ends, and returns that path. The file appears
// whole, so a reader never sees half of it.
func paneRun(t *testing.T, base, session, id, name, script string) string {
	t.Helper()
	out := filepath.Join(base, name+".out")
	mustCLI(t, base, "send-text", "-s", session, "-w", id,
		"("+script+") > "+out+".tmp 2>&1; echo EXIT=$? >> "+out+".tmp; mv "+out+".tmp "+out+"\n")
	return out
}

func paneResult(t *testing.T, path, why string) string {
	t.Helper()
	return waitForFileText(t, path, func(s string) bool { return strings.Contains(s, "EXIT=") }, why)
}

// TestHoldsAreMachineWideAndEndWithTheirHolder: a pane takes a hold, and a
// worker pane that waits on it stays blocked until the holder releases it. A
// second hold ends when its holder pane closes, and a third, taken from
// outside every pane, when its ttl ends.
//
// How it could pass wrongly: the wait could return at once, whether the hold
// is free or not, so the waiter must still be waiting a while after it
// started, and must say the hold was released, not that it was free. A check
// that always passed would pass every free case, so check must fail, and name
// the holder and the reason, while the hold is held.
//
// Negative controls: with noteHoldPaneClosed not called on window-closed
// (daemon.go), the second hold never ends. With the ttl timer not armed in
// take-hold, the third never ends.
func TestHoldsAreMachineWideAndEndWithTheirHolder(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-holds"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	bin := tuiosBin
	var report strings.Builder

	holder := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "holder", "--no-focus", "--print-id"))
	waiter := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "waiter", "--no-focus", "--print-id"))
	other := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "other", "--no-focus", "--print-id"))

	took := paneResult(t, paneRun(t, base, sess, holder, "take", bin+" hold take e2e-quiet --reason 'timing run'"), "the holder pane never took the hold")
	fmt.Fprintf(&report, "take:\n%s\n", took)
	if !strings.Contains(took, "EXIT=0") || !strings.Contains(took, "Took hold e2e-quiet") {
		t.Fatalf("ASSERTION: the holder pane could not take the hold:\n%s", took)
	}

	// Negative control: check fails while the hold is held, and says who
	// holds it and why.
	out, err := tuiosCLI(t, base, "hold", "check", "e2e-quiet")
	fmt.Fprintf(&report, "check while held:\n%s\n", out)
	if err == nil {
		t.Fatalf("ASSERTION: hold check passed while the hold is held:\n%s", out)
	}
	if !containsAll(out, "held by pane holder", "timing run") {
		t.Errorf("ASSERTION: hold check does not name the holder and the reason:\n%s", out)
	}
	if out, err := tuiosCLI(t, base, "hold", "take", "e2e-quiet", "--reason", "other", "--ttl", "1m"); err == nil {
		t.Errorf("ASSERTION: a second holder took a held hold:\n%s", out)
	}
	if got := mustCLI(t, base, "list-agents", "-s", sess, "--all"); !containsAll(got, "Holds on this machine", "e2e-quiet", "timing run") {
		t.Errorf("ASSERTION: list-agents does not show the hold:\n%s", got)
	}

	waited := paneRun(t, base, sess, waiter, "wait", bin+" wait-for hold-released e2e-quiet --timeout 60000")
	stayTrue(t, "the waiter stays blocked while the hold is held", 2*time.Second, func() (bool, string) {
		_, err := os.Stat(waited)
		return os.IsNotExist(err), "the wait ended while the hold was held"
	})
	released := paneResult(t, paneRun(t, base, sess, other, "release-other", bin+" hold release e2e-quiet"), "the other pane's release never ended")
	fmt.Fprintf(&report, "release by another pane, without force:\n%s\n", released)
	if strings.Contains(released, "EXIT=0") {
		t.Fatalf("ASSERTION: another pane released the hold without --force:\n%s", released)
	}

	mustCLI(t, base, "send-text", "-s", sess, "-w", holder, bin+" hold release e2e-quiet\n")
	got := paneResult(t, waited, "the waiter never woke when the hold was released")
	fmt.Fprintf(&report, "wait:\n%s\n", got)
	if !strings.Contains(got, "EXIT=0") || !strings.Contains(got, "Hold e2e-quiet was released") {
		t.Errorf("ASSERTION: the wait did not end with the release:\n%s", got)
	}
	mustCLI(t, base, "hold", "check", "e2e-quiet")

	// The holder pane closes, and its hold ends.
	took = paneResult(t, paneRun(t, base, sess, holder, "take-pane", bin+" hold take e2e-pane --reason 'pane bound'"), "the holder pane never took the second hold")
	if !strings.Contains(took, "EXIT=0") {
		t.Fatalf("ASSERTION: the holder pane could not take the second hold:\n%s", took)
	}
	if out, err := tuiosCLI(t, base, "hold", "check", "e2e-pane"); err == nil {
		t.Fatalf("ASSERTION: hold check passed for the second hold while it is held:\n%s", out)
	}
	mustCLI(t, base, "close-window", "-s", sess, holder)
	waitUntil(t, "the hold of the closed pane ends", uiTimeout, func() (bool, string) {
		out, err := tuiosCLI(t, base, "hold", "check", "e2e-pane")
		return err == nil, out
	})

	// Outside every pane a hold needs a ttl, and ends with it.
	if out, err := tuiosCLI(t, base, "hold", "take", "e2e-ttl", "--reason", "short"); err == nil || !strings.Contains(out, "ttl") {
		t.Errorf("ASSERTION: a hold taken outside a pane without --ttl was not refused for it:\n%s", out)
	}
	mustCLI(t, base, "hold", "take", "e2e-ttl", "--reason", "short", "--ttl", "2s")
	if out, err := tuiosCLI(t, base, "hold", "check", "e2e-ttl"); err == nil {
		t.Fatalf("ASSERTION: hold check passed for the ttl hold right after the take:\n%s", out)
	}
	waitUntil(t, "the ttl hold ends", uiTimeout, func() (bool, string) {
		out, err := tuiosCLI(t, base, "hold", "check", "e2e-ttl")
		return err == nil, out
	})
	if got := mustCLI(t, base, "hold", "ls"); !strings.Contains(got, "No holds.") {
		t.Errorf("ASSERTION: holds remain after every holder let go:\n%s", got)
	}
	saveText(t, "holds", report.String())
}
