package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// saverMarker is the text the saver hides and then puts back. The shell prints
// it, so it sits in the capture the saver animates.
const saverMarker = "SAVERRATEMARK"

// saverOpening starts the saver with one effect at one max_fps and returns how
// long the marker was off the screen: from the frame that first hides it to
// the frame that first shows it whole again. That span is the effect's opening
// in wall time.
func saverOpening(t *testing.T, effect string, maxFPS int, limit time.Duration) time.Duration {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, fmt.Sprintf("[appearance]\nmax_fps = %d\n\n[screensaver]\neffect = %q\n", maxFPS, effect))
	term := startIn(t, base, startOpts{cols: 100, rows: 30})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo "+splitMarker(saverMarker), saverMarker, shellTimeout)
	leaveTerminalMode(t, term)

	if err := term.SendKeys("S"); err != nil {
		t.Fatalf("press S to start the saver: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), saverMarker)
	}, uiTimeout); err != nil {
		t.Fatalf("%s never hid the marker: %v\n%s", effect, err, term.Snapshot())
	}
	hidden := time.Now()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), saverMarker)
	}, limit); err != nil {
		t.Fatalf("%s at max_fps %d kept the marker hidden for more than %s: %v\n%s",
			effect, maxFPS, limit, err, term.Snapshot())
	}
	return time.Since(hidden)
}

// TestScreensaverPaintsAtSixtyAtMost is the saver's frame rate, read off the
// screen as the time an effect keeps the screen hidden.
//
// tuiffects writes its effects for sixty frames a second, and most of them
// count frames. The saver used to tick at the session's NormalFPS, so with
// max_fps = 240 it ran those effects four times too fast at four times the
// CPU. It now paints at sixty at most, and its virtual clock steps at the same
// rate, so an effect that reads the clock keeps its length too.
//
//   - middleout counts frames: 99 of them, 1.65 s at sixty. At max_fps = 240 it
//     must still take at least 1.2 s.
//   - The same effect at max_fps = 30 must take at least 2.6 s (3.3 s at thirty
//     frames a second). This is the positive half: it fails if max_fps never
//     reaches the saver, which would let the first case pass for no reason.
//   - tuffbaby paces its clip by the clock. It must finish within 18 s at
//     max_fps = 240, which fails when the tick and the clock disagree.
//
// Measured on 2026-10-08 at 100x30 and a load average near 30. middleout at
// 240: 0.47 s on main and 1.63 s with the cap. tuffbaby at 240: 9.60 s with the
// cap, and 26.72 s with the cap on the tick but the clock still built from
// NormalFPS. See NEGATIVE_CONTROLS.md.
func TestScreensaverPaintsAtSixtyAtMost(t *testing.T) {
	t.Run("frame counted effect at max_fps 240", func(t *testing.T) {
		if d := saverOpening(t, "middleout", 240, uiTimeout); d < 1200*time.Millisecond {
			t.Fatalf("middleout at max_fps 240 hid the screen for %.2f s, want at least 1.2 s "+
				"(99 frames at sixty a second is 1.65 s)", d.Seconds())
		}
	})
	t.Run("frame counted effect at max_fps 30", func(t *testing.T) {
		if d := saverOpening(t, "middleout", 30, uiTimeout); d < 2600*time.Millisecond {
			t.Fatalf("middleout at max_fps 30 hid the screen for %.2f s, want at least 2.6 s "+
				"(99 frames at thirty a second is 3.3 s)", d.Seconds())
		}
	})
	t.Run("clock paced effect at max_fps 240", func(t *testing.T) {
		d := saverOpening(t, "tuffbaby", 240, 18*time.Second)
		t.Logf("tuffbaby at max_fps 240 hid the screen for %.2f s", d.Seconds())
	})
}
