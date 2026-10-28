package tuie2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestWhichKeyOnATinyClient: a client shrunk to 20x4, 35x6 and 40x10, the
// sizes a narrowed browser tab or a small pane gives, presses the leader at
// each. At the first two the menu has no room for one binding, and drawing it
// used to index the last of no columns and crash the frame: "tuios hit a
// bug", and a crash log. It now draws its title and count.
//
// Negative control: with the cols[-1] guard taken out of renderWhichKey, the
// crash overlay shows at 20x4 and a crash log is written. See
// NEGATIVE_CONTROLS.md.
func TestWhichKeyOnATinyClient(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-tiny", "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	// A client does not start below 40x12, but one that is running shrinks
	// with its window: a browser tab narrowed, a pane made small.
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-tiny"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}
	for _, sz := range []struct{ w, h int }{{20, 4}, {35, 6}, {40, 10}} {
		if err := term.Resize(sz.w, sz.h); err != nil {
			t.Fatalf("resize to %dx%d: %v", sz.w, sz.h, err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return len(strings.Split(strings.TrimRight(s.Text(), "\n"), "\n")) <= sz.h
		}, uiTimeout); err != nil {
			t.Fatalf("the client never drew at %dx%d: %v\n%s", sz.w, sz.h, err, term.Snapshot())
		}
		time.Sleep(insertGuard)
		if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
			t.Fatalf("press the leader at %dx%d: %v", sz.w, sz.h, err)
		}
		// The menu opens at once, and a crash shows its overlay on the next
		// frame, cut to the width ("tuios hit a b…" at 20 columns). Watching
		// for a while proves the overlay never came.
		deadline := time.Now().Add(1500 * time.Millisecond)
		for time.Now().Before(deadline) {
			if text := term.Snapshot(); strings.Contains(text, "tuios hit a") {
				t.Fatalf("ASSERTION: the leader menu crashed the frame at %dx%d:\n%s", sz.w, sz.h, text)
			}
			time.Sleep(100 * time.Millisecond)
		}
		// The menu is up: its title, and the count of what did not fit.
		if text := term.Snapshot(); !strings.Contains(text, "prefix") || !strings.Contains(text, "more") {
			t.Fatalf("the leader menu is not on the screen at %dx%d:\n%s", sz.w, sz.h, text)
		}
		if err := term.SendKeys(tuitest.Esc); err != nil {
			t.Fatalf("close the menu: %v", err)
		}
		time.Sleep(insertGuard)
	}
	logs, _ := filepath.Glob(filepath.Join(base, "*", "tuios", "crash-*.log"))
	more, _ := filepath.Glob(filepath.Join(base, "*", "*", "tuios", "crash-*.log"))
	if logs = append(logs, more...); len(logs) > 0 {
		t.Fatalf("ASSERTION: a crash log was written: %v\n%s", logs, term.Snapshot())
	}
	alive(t, term, "after the leader on a tiny client")
}
