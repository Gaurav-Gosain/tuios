package tuie2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestLauncherSaysWhenTheScanFoundNothing: with no program on $PATH and no
// desktop entry, the launcher's scan lands with nothing. The launcher said
// "Scanning for programs..." for as long as its list was empty, so a scan
// that had landed with nothing read as one still running, forever. It now
// says the scan found nothing.
//
// The knob hold_scan holds the scan, so the test sees both states in order:
// scanning while it is held, and the empty answer once it lands.
//
// Negative control: with the empty line keyed on the list alone, the panel
// still says "Scanning for programs" after the scan landed.
func TestLauncherSaysWhenTheScanFoundNothing(t *testing.T) {
	base := t.TempDir()
	hold, release := holdScan(t)
	empty := t.TempDir()
	term := startIn(t, base, startOpts{
		args: []string{"--standalone"},
		env:  []string{hold, "PATH=" + empty, "XDG_DATA_DIRS=" + filepath.Join(base, "no-such-data-dir")},
	})
	waitBoot(t, term)
	openLauncher(t, term)
	if err := term.WaitForText("Scanning for programs", uiTimeout); err != nil {
		t.Fatalf("the held scan never said it was scanning: %v\n%s", err, term.Snapshot())
	}
	release()
	if err := term.WaitForText(launcherNothingFound, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the scan landed with nothing and the launcher did not say so: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "launcher-nothing-found")
	stayTrue(t, "the launcher does not go back to scanning", 500*time.Millisecond, func() (bool, string) {
		text := term.Screen().Text()
		return strings.Contains(text, launcherNothingFound) && !strings.Contains(text, "Scanning for programs"), term.Snapshot()
	})
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
}

// launcherNothingFound is what the launcher says once a scan found nothing.
const launcherNothingFound = "No programs found on PATH."
