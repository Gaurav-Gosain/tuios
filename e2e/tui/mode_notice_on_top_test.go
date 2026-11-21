package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestAModeNoticeStaysAboveANoticeItDidNotAsk: alt+esc names the mode it
// chose on the dock. A notice tuios raises on its own in the same moment goes
// in under it, and the dock counts it in the +1. Here that notice is the
// session size: the key makes this client the one that sizes the session, and
// the daemon sends the new size a moment after the key.
//
// The size notice used to go on top and cover the mode notice, so the person
// pressed a key and saw no answer. Tests that waited for the mode notice
// failed the same way (TestTaskBoardGroupsCardsByStage, run 44877265143: a
// third client's alt+esc, then "Session size: 160x45 (3 clients) +1").
//
// How this could pass wrongly, written down first:
//   - The session could keep its size, so no size notice comes. The test
//     waits for the daemon to report the new size before it looks.
//   - The mode notice could be read before the size notice arrives. The
//     test waits for the +1 that counts the size notice, and then checks
//     that the mode notice stays for a second.
//
// Negative control: with showNotification putting every notice on top, the
// dock shows "Session size: ... +1" and the first wait fails.
func TestAModeNoticeStaysAboveANoticeItDidNotAsk(t *testing.T) {
	base := t.TempDir()
	// The mode notice lasts long enough that a slow runner still reads it
	// live. The size notice keeps its own two seconds.
	writeConfig(t, base, "[notifications]\nduration = 30\n")
	if out, err := tuiosCLI(t, base, "new", "S", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	// The large client presses a key, so the session takes its size.
	large := attachIn(t, base, "S", startOpts{cols: 160, rows: 45})
	w0, h0 := sessionSize(t, base, "S")

	// The small client attaches and presses nothing, so the session keeps
	// the large client's size.
	small := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "S"}})
	// A join is announced to the client already there, not to the joiner.
	if err := large.WaitForText("2 connected", bootTimeout); err != nil {
		t.Fatalf("the large client was never told a second one joined: %v\n%s", err, large.Snapshot())
	}
	if err := small.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the small client never drew the session's window: %v\n%s", err, small.Snapshot())
	}

	if err := small.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("send alt+esc: %v", err)
	}
	waitUntil(t, "the session takes the size of the client that pressed the key", uiTimeout, func() (bool, string) {
		w, h := sessionSize(t, base, "S")
		return w != w0 || h != h0, fmt.Sprintf("the session is %dx%d", w, h)
	})
	dir := artifactDir(t)
	snap := func(name string) {
		saveArtifact(t, small, dir, name)
		savePNG(t, small.Screen(), shot.XTermPalette(), dir, name)
	}
	// The notices last two seconds, so the dock as it was when the size
	// notice showed is kept for the failure message.
	var covered string
	if err := small.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		if strings.Contains(text, "Session size") && covered == "" {
			covered = text
			savePNG(t, s, shot.XTermPalette(), dir, "size-notice-on-top")
		}
		return strings.Contains(text, "Window management mode") && strings.Contains(text, "+1")
	}, uiTimeout); err != nil {
		snap("mode-notice-covered")
		t.Fatalf("ASSERTION: the dock does not show the mode notice with the size notice counted under it: %v\nthe dock while the size notice showed:\n%s", err, covered)
	}
	snap("mode-notice-on-top")
	stayTrue(t, "the mode notice stays on the dock", time.Second, func() (bool, string) {
		if strings.Contains(small.Screen().Text(), "Window management mode") {
			return true, ""
		}
		snap("mode-notice-covered")
		return false, small.Snapshot()
	})
}
