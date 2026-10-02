package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// Some built-in themes name no cursor colour. The theme package handed that
// nil *tint.Color out as a color.Color, which is an interface that is not nil,
// so a nil check passed and the RGBA call on it panicked. Found in the sweep
// for issue #374, which was the same typed nil around the verb client.
//
// horizon is one of those themes. The cursor stays unset: the browser and the
// emulator each have their own default for it. dracula names a cursor colour,
// and is the positive half: the same steps pass with it on a build with the
// bug.

// TestOSC12QueryUnderAThemeWithNoCursorColour asks for the cursor colour from
// a pane. The emulator answered from the theme's nil cursor and the client
// died with SIGSEGV.
func TestOSC12QueryUnderAThemeWithNoCursorColour(t *testing.T) {
	for _, th := range []string{"dracula", "horizon"} {
		t.Run(th, func(t *testing.T) {
			t.Parallel()
			term, _ := start(t, startOpts{cols: 100, rows: 30, args: []string{"--theme", th}})
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			// The marker is printed by the shell after the query, so it is on
			// screen only when the emulator survived the answer.
			cmd := `printf '\033]12;?\007'; sleep 0.3; echo OSC12-$((40+2))-DONE`
			if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("OSC12-42-DONE", shellTimeout); err != nil {
				code, exited := term.ExitCode()
				t.Fatalf("the shell never printed its marker after the OSC 12 query (tuios exited: %v, code %d): %v\n%s",
					exited, code, err, term.Snapshot())
			}
			if code, exited := term.ExitCode(); exited {
				t.Fatalf("tuios exited with code %d after the OSC 12 query", code)
			}
		})
	}
}

// TestListThemesDescribesAThemeWithNoCursorColour asks the daemon to describe
// the theme. The handler panicked and the client read EOF.
func TestListThemesDescribesAThemeWithNoCursorColour(t *testing.T) {
	term, base := start(t, startOpts{cols: 100, rows: 30, daemonDefault: true})
	waitBoot(t, term)
	for _, th := range []string{"dracula", "horizon"} {
		out, err := tuiosCLI(t, base, "list-themes", th)
		if err != nil {
			t.Fatalf("list-themes %s failed: %v\n%s", th, err, out)
		}
		// horizon leaves the cursor unset, so only the fg row is certain.
		if !strings.Contains(out, "fg ") {
			t.Fatalf("list-themes %s printed no fg row:\n%s", th, out)
		}
	}
}
