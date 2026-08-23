package app

import (
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A guest that streams frames over the direct medium (t=d, the payload inline
// in the escape) and one that streams them over shared memory (t=s) are the
// same guest as far as the pane is concerned: same bitmap, same pixel size,
// same cell rectangle. What tuios tells the host to draw must be the same too.
//
// It was not. The file and shared-memory path refreshes a repeat stream's
// placement record in full on every frame, so a pane that changed size is
// measured again against the frame that just arrived. The direct path refreshed
// only the position, because the bitmap behind a repeat frame is usually
// patched rather than re-sent and the cell count was assumed to be unchanged
// along with it. It is not: the cell count is the pane's, not the bitmap's, and
// the pane can change under a bitmap that does not.
//
// The visible result is a pane that grows and an image that does not: the
// placement keeps the column count from the smaller pane, the guest's extra
// pixels are cropped away, and the new columns stay blank. Rows escaped it
// because the refresh recomputes them from the image's own row count, so only
// one axis froze, which is the shape that reads as a distorted picture.

// directFrameStream is one animating guest frame sent the way real direct
// clients send one: an a=T carrying the control keys and the first 4096 bytes
// of base64, then continuation chunks carrying only m=.
func directFrameStream(imageID uint32, w, h int, tint byte) []byte {
	enc := base64.StdEncoding.EncodeToString(testBitmap(w, h, tint))
	var b strings.Builder
	b.WriteString("\x1b[H")
	first := true
	for len(enc) > 0 {
		chunk := enc
		if len(chunk) > 4096 {
			chunk = chunk[:4096]
		}
		enc = enc[len(chunk):]
		more := 0
		if len(enc) > 0 {
			more = 1
		}
		if first {
			fmt.Fprintf(&b, "\x1b_Ga=T,i=%d,p=1,z=0,f=32,s=%d,v=%d,t=d,q=2,C=1,m=%d;",
				imageID, w, h, more)
			first = false
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;", more)
		}
		b.WriteString(chunk)
		b.WriteString("\x1b\\")
	}
	return []byte(b.String())
}

// sharedMemoryFrameStream is the same frame over t=s. The name changes per
// generation the way a real guest's does; the host reads the file itself, so
// nothing here has to exist on disk for the passthrough to forward it.
func sharedMemoryFrameStream(imageID uint32, w, h int, gen int) []byte {
	name := base64.StdEncoding.EncodeToString(
		[]byte(fmt.Sprintf("tuios-direct-resize-%d-%dx%d", gen, w, h)))
	return fmt.Appendf(nil,
		"\x1b[H\x1b_Ga=T,i=%d,p=1,z=0,f=32,s=%d,v=%d,t=s,q=2,C=1;%s\x1b\\",
		imageID, w, h, name)
}

// testBitmap is a bitmap whose every byte is derived from its offset, with one
// small square tinted so successive frames differ in a corner rather than
// everywhere. A frame that differs everywhere is sent whole; the interesting
// path is the one a real animating guest takes, where the difference is small
// enough to be patched into the bitmap the host already holds.
func testBitmap(w, h int, tint byte) []byte {
	raw := make([]byte, w*h*4)
	for i := range raw {
		raw[i] = byte(i * 7)
	}
	for y := range min(h, 8) {
		for x := range min(w, 8) {
			raw[(y*w+x)*4] = tint
		}
	}
	return raw
}

// paneRig drives one long-lived pane through frames and resizes and reports
// everything tuios forwarded to the host for each of them.
type paneRig struct {
	kp       *KittyPassthrough
	em       *vt.Emulator
	hostFile *os.File
	winID    string
	screenW  int
	screenH  int
	border   int
	winW     int
	winH     int
	consumed int
}

func newPaneRig(t *testing.T, screenW, screenH, border int) *paneRig {
	t.Helper()
	clientCapabilities.Store(&HostCapabilities{
		TerminalName: "kitty", KittyGraphics: true, KittyFileTransfer: true,
		KittyAnimation: true, TrueColor: true, CellWidth: 10, CellHeight: 20,
	})
	t.Cleanup(func() { clientCapabilities.Store(nil) })

	hostFile, err := os.CreateTemp(t.TempDir(), "hostout")
	if err != nil {
		t.Fatal(err)
	}
	r := &paneRig{
		hostFile: hostFile, winID: "win-direct-resize",
		screenW: screenW, screenH: screenH, border: border,
		winW: screenW, winH: screenH,
	}
	r.kp = NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: hostFile})
	if !r.kp.IsEnabled() {
		t.Fatal("passthrough not enabled")
	}
	r.em = vt.NewEmulator(screenW-2*border, screenH-2*border)
	r.em.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		cur := r.em.CursorPosition()
		r.kp.ForwardCommand(cmd, rawData, r.winID, 0, 0,
			r.winW-2*r.border, r.winH-2*r.border, r.border, r.border,
			cur.X, cur.Y, r.em.ScrollbackLen(), r.em.IsAltScreen(), func([]byte) {})
	})
	_, _ = r.em.Write([]byte("\x1b[?1049h"))
	return r
}

func (r *paneRig) resize(w, h int) {
	r.winW, r.winH = w, h
	r.em.Resize(w-2*r.border, h-2*r.border)
}

// frame feeds one guest frame and runs a render cycle, returning the graphics
// commands tuios emitted for it.
func (r *paneRig) frame(stream []byte) []string {
	_, _ = r.em.Write(stream)
	info := &WindowPositionInfo{
		ContentOffsetX: r.border, ContentOffsetY: r.border,
		Width: r.winW, Height: r.winH,
		ContentWidth: r.winW - 2*r.border, ContentHeight: r.winH - 2*r.border,
		Visible:     true,
		ScreenWidth: r.screenW, ScreenHeight: r.screenH,
		IsAltScreen:   r.em.IsAltScreen(),
		ScrollbackLen: r.em.ScrollbackLen(),
	}
	r.kp.RefreshAllPlacements(func() map[string]*WindowPositionInfo {
		return map[string]*WindowPositionInfo{r.winID: info}
	})
	written, _ := os.ReadFile(r.hostFile.Name())
	out := append(append([]byte(nil), written[r.consumed:]...), r.kp.FlushPending()...)
	r.consumed = len(written)
	return graphicsCommands(out)
}

var graphicsParamsRE = regexp.MustCompile(`\x1b_G([^;\x1b]*)`)

// graphicsCommands is the parameter list of every graphics command in a chunk
// of host output, minus the continuation chunks of a chunked transmission,
// which carry payload rather than instructions.
func graphicsCommands(out []byte) []string {
	var res []string
	for _, m := range graphicsParamsRE.FindAllSubmatch(out, -1) {
		params := string(m[1])
		if !strings.Contains(params, "a=") {
			continue
		}
		res = append(res, params)
	}
	return res
}

// lastPlacement is the last a=p in a batch, which is what the host ends up
// drawing from.
func lastPlacementCmd(cmds []string) string {
	for i := len(cmds) - 1; i >= 0; i-- {
		if strings.HasPrefix(cmds[i], "a=p,") {
			return cmds[i]
		}
	}
	return ""
}

// TestDirectStreamFollowsAGrowingPane is the transport A/B.
//
// The guest draws a bitmap wider than its pane, so the placement is legitimately
// cropped to the pane. Then the pane grows to a rectangle the whole bitmap fits
// in, while the guest keeps sending that same bitmap. Both transports must then
// draw the image across the pane's full column count.
func TestDirectStreamFollowsAGrowingPane(t *testing.T) {
	// Content rectangles of 99x25 then 119x31 cells, at 10x20 px per cell.
	const (
		screenW, screenH = 121, 33
		smallW, smallH   = 101, 27
		imgW, imgH       = 1190, 620
		grownCols        = 119
	)

	for _, transport := range []string{"direct", "shm"} {
		t.Run(transport, func(t *testing.T) {
			r := newPaneRig(t, screenW, screenH, 1)
			r.resize(smallW, smallH)

			frame := func(gen int) []string {
				if transport == "shm" {
					return r.frame(sharedMemoryFrameStream(1, imgW, imgH, gen))
				}
				return r.frame(directFrameStream(1, imgW, imgH, byte(gen)))
			}

			// Two frames in the small pane: the first transmits the bitmap and
			// places it, the second is the repeat that takes the refresh path.
			frame(1)
			if got := lastPlacementCmd(frame(2)); got != "" && !strings.Contains(got, "c=99") {
				t.Fatalf("the image was not cropped to the small pane: %q", got)
			}

			// The pane grows to fit the whole bitmap. The guest has not
			// redrawn yet, so the frames arriving are the same size as before.
			r.resize(screenW, screenH)
			var placed string
			for gen := 3; gen <= 5; gen++ {
				if p := lastPlacementCmd(frame(gen)); p != "" {
					placed = p
				}
			}
			if placed == "" {
				t.Fatalf("the grown pane was never placed at all")
			}
			want := fmt.Sprintf("c=%d,", grownCols)
			if !strings.Contains(placed, want) {
				t.Errorf("the pane grew to %d columns but the image is still drawn into "+
					"the column count it was transmitted with: %q (want %s)",
					grownCols, placed, want)
			}
			// A cell box that is the whole pane needs no source rectangle: the
			// bitmap is exactly that many pixels wide. One that appears here is
			// the crop the stale column count kept alive.
			if strings.Contains(placed, ",w=") {
				t.Errorf("the whole bitmap fits the grown pane, so no part of it should be "+
					"cropped away, but a source rectangle was still sent: %q", placed)
			}
		})
	}
}
