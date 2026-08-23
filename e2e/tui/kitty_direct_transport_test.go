package tuie2e

import (
	"testing"
	"time"
)

// TestKittyDirectTransportDrawsWholeImage is the reported "base64 frames render
// upscaled, shared-memory frames render crisp" made into an A/B.
//
// The same guest fills the same pane with a bitmap of exactly its own pixel
// size, and the only thing that changes between the two subtests is the kitty
// transmission medium: t=s hands the host a shared-memory name, t=d streams the
// payload inline in m= chunks. The bitmap, the geometry and the pane are
// identical, so any difference in what the host is told to draw belongs to the
// transport and to nothing else.
//
// The invariant is assertWholeImage's: the pane's cell count, and all of the
// image. Anything else asks the host to rescale a bitmap that already matches
// its box, which is the blur in the report.
func TestKittyDirectTransportDrawsWholeImage(t *testing.T) {
	for _, transport := range []string{"shm", "b64"} {
		t.Run(transport, func(t *testing.T) {
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
			runInShell(t, term, "echo IMAGEPANE", "IMAGEPANE", shellTimeout)
			// 10 fps: a full-pane RGBA frame is over a megabyte of base64, and
			// the question here is what the host is told, not how fast.
			_, cols, rows, xpx, ypx := startFrameloopOpts(t, term, 0, 10, transport)
			t.Logf("pane: %dx%d cells, %dx%d px (%d x %d px per cell)",
				cols, rows, xpx, ypx, xpx/cols, ypx/rows)
			leaveTerminalMode(t, term)

			host.mark("steady")
			time.Sleep(4 * time.Second)

			assertWholeImage(t, term, host.bytes(), cols, rows, xpx, ypx)
		})
	}
}
