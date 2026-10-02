package tuie2e

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A pane on a workspace the client does not show is not streamed to it: the
// client subscribes to the panes of the shown workspace only. The two tests
// here cover what the client still has to learn about such a pane.

// newHiddenPane opens a pane named name on workspace 2 of session, without
// focusing it, and returns its window id once the daemon lists it.
func newHiddenPane(t *testing.T, base, session, name string, argv ...string) string {
	t.Helper()
	args := append([]string{"new-window", name, "-s", session, "--workspace", "2", "--no-focus", "--"}, argv...)
	if out, err := tuiosCLI(t, base, args...); err != nil {
		t.Fatalf("new-window on workspace 2: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		wl, err := daemonWindows(base, session)
		if err == nil {
			for _, w := range wl.Windows {
				if w.Workspace == 2 {
					return w.ID
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon never listed a window on workspace 2: %v %+v", err, wl)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestAPaneOnAHiddenWorkspaceClosesWhenItsProgramExits: a pane whose program
// ends has to close, on whichever workspace it sits.
//
// The daemon told only the clients streaming a pane that its program had
// ended, and the client's close of the window is what takes it out of the
// session. A pane on a hidden workspace is streamed to nobody, so it stayed
// listed after its program was gone.
func TestAPaneOnAHiddenWorkspaceClosesWhenItsProgramExits(t *testing.T) {
	term, base := start(t, startOpts{args: []string{"new", "home"}})
	waitBoot(t, term)

	id := newHiddenPane(t, base, "home", "goner", "/bin/sh", "-c", "sleep 3")

	deadline := time.Now().Add(15 * time.Second)
	for {
		wl, err := daemonWindows(base, "home")
		if err != nil {
			t.Fatalf("list-windows: %v", err)
		}
		listed := false
		for _, w := range wl.Windows {
			listed = listed || w.ID == id
		}
		if !listed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the program of the pane on workspace 2 exited, and the session still lists it: %+v\n%s",
				wl.Windows, term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	saveArtifact(t, term, artifactDir(t), "hidden-pane-closed")
}

// TestAPaneShownByFocusWindowStreamsItsOutput: focus-window on a pane on a
// hidden workspace shows that workspace, and the pane has to show what it
// printed while hidden and go on showing what it prints, kitty images
// included.
//
// A workspace switch made on this client moves the streams. The one
// focus-window makes reaches the client as state from the daemon, and that
// path changed the shown workspace without subscribing its panes. The pane
// stayed blank whatever it printed, which in the report was a video stream
// that never showed a frame.
func TestAPaneShownByFocusWindowStreamsItsOutput(t *testing.T) {
	host := newKittyHost()
	term, base := start(t, startOpts{
		cols: 120, rows: 40,
		args: []string{"new", "home"},
		env:  []string{"TUIOS_SIXEL_GRAPHICS=0"},
		out:  host,
	})
	host.answerProbe(t, term)
	waitBoot(t, term)

	_, img := writeIcatPNG(t, t.TempDir())
	frame := fmt.Sprintf(`\033_Ga=T,q=2,f=100,s=%d,v=%d;%s\033\\`, icatImageW, icatImageH,
		base64.StdEncoding.EncodeToString(img))
	// EARLY is printed while the pane is hidden, so it reaches the client in
	// the snapshot. The loop starts after the switch, so LIVE and the frames
	// reach it in the stream. Markers are split so the command line cannot
	// match them.
	script := `echo EARLY""MARK; sleep 3; while :; do printf '` + frame + `'; echo; echo LIVE""MARK; sleep 1; done`
	id := newHiddenPane(t, base, "home", "shown-later", "/bin/sh", "-c", script)

	host.mark("icat")
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "home", id); err != nil {
		t.Fatalf("focus-window: %v\n%s", err, out)
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "EARLYMARK") && strings.Contains(s.Text(), "LIVEMARK")
	}, uiTimeout); err != nil {
		t.Fatalf("focus-window showed workspace 2, and its pane never showed its output: %v\n%s", err, term.Snapshot())
	}

	deadline := time.Now().Add(uiTimeout)
	for {
		_, direct := hostTransmits(t, host.bytes())
		if len(direct) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane drew frames after it was shown, and none reached the host terminal\n%s", term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	saveArtifact(t, term, artifactDir(t), "hidden-pane-shown")
}
