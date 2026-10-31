package tuie2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The reported symptom is a stretched image, and a stretch is a scale factor.
// Cropping is not: an image larger than the space for it is legitimately shown
// in part, and the part is still drawn at its own size. So the invariant that
// actually says "not stretched" is about the ratio between what the host is
// given and the box it is told to put it in:
//
//	source pixels drawn  ==  cell box in pixels
//
// The guest here renders at exactly its pane's pixel size, so every frame the
// host holds is already 1:1 with the cells it belongs in. Any command that
// breaks the equality is asking kitty to rescale that bitmap, and a rescale
// that is not uniform is visibly a stretch while a uniform one is a blur. The
// pane's own size does not enter into it, which is what makes this checkable
// across a resize: the numbers may both change, but they change together or
// something is wrong.

// scaleFault is one command that asks the host to rescale.
type scaleFault struct {
	cmd     wireCmd
	srcW    int
	srcH    int
	boxW    int
	boxH    int
	imageW  int
	imageH  int
	stretch bool // the two axes disagree, so it is a distortion rather than a resample
}

func (f scaleFault) String() string {
	kind := "rescaled"
	if f.stretch {
		kind = "STRETCHED"
	}
	return fmt.Sprintf("%s phase=%s: %dx%d source px into a %dx%d px cell box "+
		"(image on the host is %dx%d): %q",
		kind, f.cmd.phase, f.srcW, f.srcH, f.boxW, f.boxH, f.imageW, f.imageH, f.cmd.params)
}

// scaleFaults walks the stream in order, tracking the bitmap the host holds for
// each image id, and reports every command that draws one at a size other than
// its own.
//
// Tracking the bitmap is what makes this honest. A placement carries no pixel
// dimensions of its own; what it means depends entirely on the transmission it
// lands on, and a placement that was right for the previous frame can be wrong
// for this one without a byte of it changing.
func scaleFaults(stream []byte, cellW, cellH int) []scaleFault {
	type bitmap struct{ w, h int }
	held := map[int]bitmap{}
	var out []scaleFault
	for _, c := range wireCmds(stream) {
		// s= and v= on a transmit are the bitmap the host now holds.
		if (c.action == "t" || c.action == "T" || c.action == "q") && c.pixW > 0 && c.pixH > 0 {
			held[c.image] = bitmap{c.pixW, c.pixH}
		}
		if c.cols <= 0 || c.rows <= 0 {
			continue
		}
		img, ok := held[c.image]
		if !ok {
			continue // nothing known about the bitmap, so nothing to say
		}
		srcW, srcH := c.srcW, c.srcH
		if srcW == 0 {
			srcW = img.w - c.srcX
		}
		if srcH == 0 {
			srcH = img.h - c.srcY
		}
		boxW, boxH := c.cols*cellW, c.rows*cellH
		if srcW == boxW && srcH == boxH {
			continue
		}
		out = append(out, scaleFault{
			cmd: c, srcW: srcW, srcH: srcH, boxW: boxW, boxH: boxH,
			imageW: img.w, imageH: img.h,
			// Equal ratios on both axes is a uniform resample; unequal is the
			// distortion the report is about.
			stretch: srcW*boxH != srcH*boxW,
		})
	}
	return out
}

// reportScaleFaults fails the test with the distinct faults, worst first.
func reportScaleFaults(t *testing.T, faults []scaleFault, total int) {
	t.Helper()
	if len(faults) == 0 {
		return
	}
	stretched := 0
	seen := map[string]int{}
	var order []string
	for _, f := range faults {
		if f.stretch {
			stretched++
		}
		s := f.String()
		if seen[s] == 0 {
			order = append(order, s)
		}
		seen[s]++
	}
	var b strings.Builder
	for _, s := range order {
		fmt.Fprintf(&b, "  x%d  %s\n", seen[s], s)
	}
	t.Errorf("%d of %d draw commands asked the host to rescale the bitmap "+
		"(%d of them non-uniformly, which is the stretch):\n%s",
		len(faults), total, stretched, b.String())
}

// A guest that names no cell box asks for an image at its own pixels, and a
// browser draws a page from patches placed that way, each at a pixel offset. A
// box of whole cells made the host scale each one, and the page showed seams.
// The stand-in sends two such images per frame: one that changes in place, and
// one with the same pixels at a new offset and layer, in the same cells, so only
// the offset and the layer say it moved.
func TestKittyPatchesDrawAtTheirOwnPixels(t *testing.T) {
	requireDevShm(t)
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

	const frames = 30
	names := runShmStream(t, term, frames, 20, 0, "patches")
	for _, n := range names {
		_ = os.Remove("/dev/shm/" + n)
	}

	var draws, boxed []wireCmd
	lastLayer := fmt.Sprintf("z=%d", 3+frames-1)
	lastOffset := fmt.Sprintf("X=%d", 1+(frames-1)%2)
	reached := false
	for _, c := range wireCmds(host.bytes()) {
		if c.action != "p" && c.action != "T" || !strings.Contains(c.params, "X=") {
			continue
		}
		draws = append(draws, c)
		if c.cols != 0 || c.rows != 0 {
			boxed = append(boxed, c)
		}
		if strings.Contains(c.params, lastLayer) && strings.Contains(c.params, lastOffset) {
			reached = true
		}
	}
	t.Logf("%d draw commands with an offset, %d of %d objects reached the host", len(draws), len(forwardedShmNames(host.bytes())), len(names))
	if len(draws) < frames {
		t.Fatalf("only %d draw commands reached the host for %d frames of 2 images", len(draws), frames)
	}
	if len(boxed) > 0 {
		t.Fatalf("%d of %d patches were told a cell box, which scales them: %q", len(boxed), len(draws), boxed[0].params)
	}
	if !reached {
		t.Fatalf("the last frame's offset and layer never reached the host (%s, %s): same pixels at a new place were dropped", lastOffset, lastLayer)
	}
}
