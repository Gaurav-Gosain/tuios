package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestANoticeGoesOnTimeWithoutItsBurn: the harness starts every client with
// the knob notice_burn=off, so a notice draws its rule full and the dock does
// not redraw for it on every frame. The notice must still go when its time is
// up, or the knob would hide a notice that never leaves.
//
// The notice lasts two seconds here. Once its block is drawn the dock must
// hold still while it shows, and the notice must be gone within three seconds
// of showing, and not before one and a half.
//
// How this could pass wrongly, written down first:
//   - The knob could be off in the client: the burn would change the rule row,
//     and the check that the dock rows stay the same fails.
//   - The notice could go at once and pass the "gone" check: it must be there
//     for the first second.
func TestANoticeGoesOnTimeWithoutItsBurn(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[notifications]\nduration = 2\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	disableTiling(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !containsAll(dockRow(s), "Tiling off") }, uiTimeout); err != nil {
		t.Fatalf("the tiling off notice never went: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys("t"); err != nil {
		t.Fatalf("send t: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return containsAll(dockRow(s), "Tiling on") }, uiTimeout); err != nil {
		t.Fatalf("no notice after t: %v\n%s", err, term.Snapshot())
	}
	shown := time.Now()
	_, rows := term.Screen().Size()
	// The burn changes the colours of the rule above the dock, not its text,
	// so the rows are compared cell by cell, colour included.
	dockRows := func(s tuitest.Screen) string {
		cols, _ := s.Size()
		var b strings.Builder
		for _, y := range []int{rows - 2, rows - 1} {
			for x := range cols {
				c := s.Cell(x, y)
				fmt.Fprintf(&b, "%s%v%v|", c.Content, c.Fg, c.Bg)
			}
		}
		return b.String()
	}
	// The block takes a frame or two to draw its cap and rule. Then the dock
	// holds still: with the burn it changes on every frame until the notice
	// goes.
	last, since := "", time.Now()
	waitUntil(t, "the dock holds still while the notice shows", time.Second, func() (bool, string) {
		if now := dockRows(term.Screen()); now != last {
			last, since = now, time.Now()
		}
		return time.Since(since) >= 300*time.Millisecond, term.SnapshotStyled()
	})
	stayTrue(t, "the notice shows, and the dock does not change while it does", 500*time.Millisecond, func() (bool, string) {
		s := term.Screen()
		return containsAll(dockRow(s), "Tiling on") && dockRows(s) == last, term.SnapshotStyled()
	})
	waitUntil(t, "the notice goes when its two seconds are up", 3*time.Second-time.Since(shown), func() (bool, string) {
		return !containsAll(dockRow(term.Screen()), "Tiling on"), term.Snapshot()
	})
	if gone := time.Since(shown); gone < 1500*time.Millisecond {
		t.Fatalf("ASSERTION: the notice went %v after it showed, before its two seconds", gone)
	}
}
