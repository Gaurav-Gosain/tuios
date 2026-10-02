package tuie2e

import (
	"strings"
	"testing"
	"time"
)

// A guest streaming over shared memory sends a=T,t=s under one image id, one
// command per frame. Each frame used to wait in the queue for the render loop:
// tuios sent the host an a=t when the frame arrived and an a=p on the next
// render pass. When the image is already placed whole where the frame goes,
// the frame is that placement's new picture, and it now goes out at once as
// one a=T under the placement's id.
//
// The stand-in in ./frameloop fills its pane at 60 frames a second.
func TestKittySharedMemoryStreamIsWrittenAtOnce(t *testing.T) {
	host := newKittyHost()
	term, _ := start(t, startOpts{
		cols: 120, rows: 40,
		env: []string{"TUIOS_SIXEL_GRAPHICS=0"},
		out: host,
	})
	host.answerProbe(t, term)
	waitBoot(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	waitWindowCount(t, term, 1, "one pane")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo IMAG\"\"EPANE", "IMAGEPANE", shellTimeout)
	_, cols, rows, xpx, ypx := startFrameloopOpts(t, term, 0, 60, "shm")
	leaveTerminalMode(t, term)
	time.Sleep(time.Second)

	host.mark("steady")
	time.Sleep(3 * time.Second)

	stream := host.bytes()
	counts := map[string]int{}
	var deletes []string
	for _, c := range wireCmds(stream) {
		if c.phase != "steady" || c.image == 0 {
			continue
		}
		counts["a="+c.action]++
		if c.action == "d" {
			deletes = append(deletes, c.params)
		}
		if c.action == "T" && !strings.Contains(c.params, "p=") {
			t.Fatalf("a frame was placed without the placement's id, so the host stacks a second placement: %q", c.params)
		}
	}
	t.Logf("over 3 s of a 60 fps stream the host was sent: %v", counts)
	if len(deletes) > 0 {
		t.Fatalf("the image was taken down %d times mid-stream: %q", len(deletes), deletes[0])
	}
	// 180 frames are due. Some slack for a loaded machine.
	if counts["a=T"] < 120 {
		t.Fatalf("only %d frames went out at once as a=T; the rest waited for the render loop: %v",
			counts["a=T"], counts)
	}
	if counts["a=t"] > 0 {
		t.Fatalf("%d frames still went out as a transmit to be placed later: %v", counts["a=t"], counts)
	}
	// Every frame still describes the whole image in the pane's whole cells.
	assertWholeImage(t, term, stream, cols, rows, xpx, ypx)
}
