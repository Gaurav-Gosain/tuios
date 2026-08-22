package tuie2e

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestKittyStretchWhileGuestLagsAResize is the reported stretch made
// deterministic.
//
// The bug is a race, and left to chance it shows up in roughly one run in
// three, which is how three fixes shipped for it and the report kept coming.
// The race is between the pane being given a new size and the guest getting
// round to drawing at it: for that interval tuios holds the new cell count
// while every frame arriving is still the old bitmap. So both halves are held
// open here rather than waited for. The guest is told to take most of a second
// to relay out, the way a browser does, and the pane is resized by a mouse
// press that is never released - a press untiles a tiled pane, which drops its
// borderless allowance and hands the guest a box two cells smaller in each
// direction (internal/input/mouse_click.go, beginWindowDrag).
//
// What must not happen in that interval is a rescale. The pane is genuinely
// smaller and the bitmap is genuinely the old size, so there is only one honest
// thing to send: as much of the image as fits, at its own size. Sending all of
// it in fewer cells scales it, and scaling the width without the height is the
// stretched picture in the report.
func TestKittyStretchWhileGuestLagsAResize(t *testing.T) {
	host := newKittyHost()
	term, _ := start(t, startOpts{
		cols: 120, rows: 40,
		args: []string{"--shared-borders"},
		env:  []string{"TUIOS_SIXEL_GRAPHICS=0"},
		out:  host,
	})
	host.answerProbe(t, term)
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	waitWindowCount(t, term, 2, "two tiled panes")

	mouseClick(t, term, 20, 12, tuitest.MouseLeft, 0)
	time.Sleep(400 * time.Millisecond)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo IMAGEPANE", "IMAGEPANE", shellTimeout)
	// 900ms to relay out: long enough that the interval is many frames wide
	// rather than something to be caught between two of them.
	_, cols, rows, xpx, ypx := startFrameloop(t, term, 900)
	t.Logf("left pane: %dx%d cells, %dx%d px", cols, rows, xpx, ypx)
	leaveTerminalMode(t, term)

	// The neighbour prints throughout, as reported: it is what keeps the render
	// loop awake, and an idle render loop re-places nothing.
	mouseClick(t, term, 95, 12, tuitest.MouseLeft, 0)
	time.Sleep(400 * time.Millisecond)
	enterTerminalMode(t, term)
	typeLine(t, term, "while :; do ls; done")
	leaveTerminalMode(t, term)
	time.Sleep(time.Second)

	// Hold the pane at its smaller size for most of the guest's relayout, so
	// every frame that arrives is one the pane has outgrown.
	host.mark("guest-behind")
	mousePress(t, term, 20, 12, tuitest.MouseLeft, 0)
	time.Sleep(800 * time.Millisecond)
	drawn := phaseCmds(host.bytes(), "guest-behind")
	mouseRelease(t, term, 20, 12, tuitest.MouseLeft, 0)
	time.Sleep(1500 * time.Millisecond)

	if len(drawn) == 0 {
		t.Fatalf("nothing was drawn while the guest was behind, so the interval the "+
			"bug lives in was never entered\n%s", term.Snapshot())
	}
	t.Logf("%d draw commands reached the host while the guest was behind", len(drawn))
	reportScaleFaults(t, scaleFaults(host.bytes(), xpx/cols, ypx/rows), len(drawn))
}
