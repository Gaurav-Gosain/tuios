package tuie2e

import (
	"fmt"
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
