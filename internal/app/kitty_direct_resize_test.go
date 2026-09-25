package app

import (
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
