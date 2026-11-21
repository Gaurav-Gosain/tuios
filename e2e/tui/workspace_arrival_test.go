package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestWorkspaceArrivalLandsAtOnce opens two panes on a workspace while the
// client shows another, as an agent that starts helpers does, and switches
// back. The panes have to be drawn where the layout puts them from the first
// frame after the switch: nobody saw the change happen, so there is no move
// to show.
//
// The bug it pins: the switch put the panes back at the rectangles this
// client kept when it left, the layout of the two panes it had then, and
// the retile that followed slid them from there into the four-pane layout,
// with the new panes growing in from small boxes. The workspace slide lands
// a pane laid out while it runs, so it hid this, but only when it ran. With
// the transition off, or skipped for a client whose frames are slow, the
// panes slid in for about half a second.
//
// How this could pass wrongly, written down first:
//   - The panes could open on the workspace on screen, and the switch back
//     would find nothing new. The final frame has to hold four panes, and
//     workspace 1 held two when the client left it.
//   - The trace could miss the frames of the switch. At least one frame has
//     to be composed after the switch.
//   - The slide could hide a pane motion by landing it. The run with
//     workspace_transition = "none" has no slide to hide behind.
//   - The client could animate nothing at all, and every switch would pass.
//     The second pane opens on the workspace on screen first, and the first
//     pane has to slide to make room for it, through rectangles between its
//     old one and its new one.
func TestWorkspaceArrivalLandsAtOnce(t *testing.T) {
	for _, tc := range []struct{ name, extra string }{
		{"slide", ""},
		{"transition-none", `workspace_transition = "none"`},
	} {
		t.Run(tc.name, func(t *testing.T) { arrivalRun(t, tc.extra) })
	}
}

func arrivalRun(t *testing.T, extra string) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[startup]\ntiled = true\n[appearance]\nmotion = \"full\"\nanimation_speed = 50\n"+extra+"\n")
	const sess = "e2e-arrival"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "--workspace", "2", "--no-focus", "other"); err != nil {
		t.Fatalf("open the pane on workspace 2: %v\n%s", err, out)
	}
	trace := filepath.Join(t.TempDir(), "render-trace.log")
	term := startIn(t, base, startOpts{args: []string{"attach", sess}, animations: true,
		env: []string{"TUIOS_RENDER_TRACE=" + trace}})
	if err := term.WaitStable(bootTimeout); err != nil {
		t.Fatalf("screen never settled: %v", err)
	}
	dir := artifactDir(t)
	alone := readTraceFrames(t, trace, 0, "")
	if len(alone) == 0 || len(alone[len(alone)-1].panes) != 1 {
		t.Fatalf("want one pane on workspace 1 at the start, the trace holds %d frames", len(alone))
	}
	var first string
	var full drawnRect
	for id, r := range alone[len(alone)-1].panes {
		first, full = id, r
	}

	mark := fileSize(t, trace)
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "--workspace", "1", "--no-focus", "two"); err != nil {
		t.Fatalf("open two on workspace 1: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(tuitest.Screen) bool {
		f := readTraceFrames(t, trace, mark, "")
		return len(f) > 0 && len(f[len(f)-1].panes) == 2 && f[len(f)-1].anims == 0
	}, uiTimeout); err != nil {
		t.Fatalf("the second pane never settled on workspace 1: %v", err)
	}
	opened := readTraceFrames(t, trace, mark, "")
	var widths []int
	for _, f := range opened {
		if r, ok := f.panes[first]; ok {
			widths = append(widths, r.W)
		}
	}
	half := opened[len(opened)-1].panes[first]
	if msg := oneWay(widths, full.W, half.W); msg != "" {
		var b strings.Builder
		for _, f := range opened {
			fmt.Fprintf(&b, "anims=%d %v\n", f.anims, f.panes)
		}
		t.Fatalf("on screen, the first pane's width %s: %v; the client did not animate, so the switch below proves nothing\n%s", msg, compactSeq(widths), b.String())
	}

	if err := term.SendKeys(tuitest.Alt('2')); err != nil {
		t.Fatalf("press alt+2: %v", err)
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled on workspace 2: %v", err)
	}
	for _, name := range []string{"three", "four"} {
		if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "--workspace", "1", "--no-focus", name); err != nil {
			t.Fatalf("open %s on workspace 1: %v\n%s", name, err, out)
		}
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("screen never settled after the opens: %v", err)
	}

	mark = fileSize(t, trace)
	if err := term.SendKeys(tuitest.Alt('1')); err != nil {
		t.Fatalf("press alt+1: %v", err)
	}
	if err := term.WaitFor(func(tuitest.Screen) bool {
		f := readTraceFrames(t, trace, mark, "")
		return len(f) > 0 && len(f[len(f)-1].panes) == 4 && f[len(f)-1].anims == 0
	}, uiTimeout); err != nil {
		t.Fatalf("workspace 1 never settled with four panes: %v", err)
	}
	frames := readTraceFrames(t, trace, mark, "")
	saveArtifact(t, term, dir, "arrived")
	var b strings.Builder
	for _, f := range frames {
		fmt.Fprintf(&b, "anims=%d %v\n", f.anims, f.panes)
	}
	_ = os.WriteFile(filepath.Join(dir, "frames.txt"), []byte(b.String()), 0o644)
	if len(frames) == 0 {
		t.Fatalf("no frame was composed after the switch")
	}
	final := frames[len(frames)-1].panes
	if len(final) != 4 {
		t.Fatalf("want four panes on workspace 1 after the switch, the last frame drew %v", final)
	}
	moved := 0
	for i, f := range frames {
		for id, r := range f.panes {
			if want, ok := final[id]; ok && r != want {
				moved++
				t.Errorf("frame %d of %d drew pane %s at %v, want it at %v from the first frame", i+1, len(frames), id, r, want)
			}
		}
	}
	if moved > 0 {
		t.Logf("frames after the switch:\n%s", b.String())
	}
}
