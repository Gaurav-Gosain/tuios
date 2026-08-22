package app

import (
	"bytes"
	"os"
	"strconv"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// feedTBFrame runs a captured/synthesised kitty graphics stream through the real
// VT emulator + passthrough for a window that fills the whole host screen, then
// runs a render cycle and returns everything tuios forwarded to the host.
//
// The window is border=1 and exactly as tall as the screen, mirroring a
// maximized tuios pane. A full-window graphics app (terminal-browser, awrit)
// draws an image that fills the pane, so its placement reaches the bottom screen
// edge - the geometry that used to be hidden outright.
func feedTBFrame(t *testing.T, stream []byte, screenW, screenH int) []byte {
	return feedTBFrameBorder(t, stream, screenW, screenH, 1)
}

func feedTBFrameBorder(t *testing.T, stream []byte, screenW, screenH, border int) []byte {
	t.Helper()

	clientCapabilities.Store(&HostCapabilities{
		TerminalName:      "kitty",
		KittyGraphics:     true,
		KittyFileTransfer: true,
		TrueColor:         true,
		CellWidth:         10,
		CellHeight:        20,
	})
	t.Cleanup(func() { clientCapabilities.Store(nil) })

	hostFile, err := os.CreateTemp(t.TempDir(), "hostout")
	if err != nil {
		t.Fatal(err)
	}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: hostFile})
	if !kp.IsEnabled() {
		t.Fatal("passthrough not enabled")
	}
	if kp.inlineGraphics {
		t.Fatal("expected native mode (inlineGraphics=false)")
	}

	winID := "win-fullpane"
	winX, winY := 0, 0
	winW, winH := screenW, screenH

	em := vt.NewEmulator(winW-2*border, winH-2*border)
	em.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		cur := em.CursorPosition()
		kp.ForwardCommand(cmd, rawData, winID,
			winX, winY, winW, winH, border, border,
			cur.X, cur.Y, em.ScrollbackLen(), em.IsAltScreen(),
			func([]byte) {})
	})

	_, _ = em.Write(stream)

	winInfo := &WindowPositionInfo{
		WindowX: winX, WindowY: winY,
		ContentOffsetX: border, ContentOffsetY: border,
		Width: winW, Height: winH,
		Visible:       true,
		ScreenWidth:   screenW,
		ScreenHeight:  screenH,
		IsAltScreen:   em.IsAltScreen(),
		ScrollbackLen: em.ScrollbackLen(),
	}
	kp.RefreshAllPlacements(func() map[string]*WindowPositionInfo {
		return map[string]*WindowPositionInfo{winID: winInfo}
	})

	out, _ := os.ReadFile(hostFile.Name())
	return append(out, kp.FlushPending()...)
}

// TestTerminalBrowserFullPaneIsPlaced feeds a real captured terminal-browser
// frame (testdata/tb_inline_frame.bin) through the passthrough for a pane that
// fills the whole screen and asserts tuios both transmits the image bytes and
// emits an a=p placement.
//
// Before the fix, the native bottom-edge guard hid every image whose bottom
// reached the last screen row. terminal-browser fills its pane, so the image
// data was forwarded but never placed: the pane rendered blank. This test fails
// on that revision (no a=p) and passes once the guard clamps instead of hides.
func TestTerminalBrowserFullPaneIsPlaced(t *testing.T) {
	stream, err := os.ReadFile("testdata/tb_inline_frame.bin")
	if err != nil {
		t.Fatal(err)
	}

	forwarded := feedTBFrame(t, stream, 100, 40)

	if !bytes.Contains(forwarded, []byte("\x1b_Ga=t")) &&
		!bytes.Contains(forwarded, []byte("\x1b_Ga=T")) {
		t.Fatal("image bytes were not transmitted to the host")
	}
	if !bytes.Contains(forwarded, []byte("a=p,")) {
		t.Fatal("no placement (a=p) forwarded: full-pane image was dropped, pane renders blank")
	}
}

// TestFullPaneImageClampedNotHidden checks the fix directly: a full-height
// image placement is clamped to leave the final screen row free (so the host
// terminal does not scroll) instead of being hidden, so an a=p is still emitted.
func TestFullPaneImageClampedNotHidden(t *testing.T) {
	// Synthesise a full-window direct-transmit+place frame (a=T, t=d, RGBA) the
	// way terminal-browser / awrit do, sized to fill a full-screen pane.
	frame := buildDirectFrame(1, 1200, 800)
	forwarded := feedTBFrame(t, frame, 120, 40)

	if !bytes.Contains(forwarded, []byte("a=p,")) {
		t.Fatal("full-pane image hidden instead of clamped (no a=p)")
	}
}

// TestBorderlessFullPaneImageClamped exercises the clamp branch: a borderless
// tiled pane whose image genuinely occupies every screen row, including the
// last. The image must still be placed (a=p), but clamped to leave the final
// row free so the host terminal does not scroll.
func TestBorderlessFullPaneImageClamped(t *testing.T) {
	frame := buildDirectFrame(1, 1200, 800)
	// border=0: content fills all 40 rows, so the image bottom would land on the
	// last screen row and the clamp must kick in.
	forwarded := feedTBFrameBorder(t, frame, 120, 40, 0)

	if !bytes.Contains(forwarded, []byte("a=p,")) {
		t.Fatal("borderless full-pane image hidden instead of clamped (no a=p)")
	}
	// Clamped to 39 rows (one short of the 40-row screen) so the last row stays
	// free. Placement must therefore carry r=39, not r=40.
	if !bytes.Contains(forwarded, []byte("r=39")) {
		t.Fatalf("expected clamped placement r=39, forwarded=%q", forwarded)
	}
	// Columns are not clamped at all here. A borderless pane owns every column
	// of its rectangle, and the guest drew 1200px for all 120 of them, so
	// anything narrower is a frame kitty has to scale down to fit.
	if !bytes.Contains(forwarded, []byte("c=120")) {
		t.Fatalf("expected the full pane width c=120, forwarded=%q", forwarded)
	}
}

// buildDirectFrame builds an a=T direct (t=d) RGBA transmit+place APC for a
// width x height image, on the alt screen, as a full-window app would emit it.
func buildDirectFrame(id, width, height int) []byte {
	var b bytes.Buffer
	b.WriteString("\x1b[?1049h\x1b[H") // alt screen + cursor home
	b.WriteString("\x1b[?2026h")
	// tiny 1x1 RGBA payload is fine; s/v declare the display size and cells are
	// derived from them. The passthrough does not decode the payload for direct
	// transmits, it re-chunks it verbatim.
	b.WriteString("\x1b_Ga=T,f=32,s=")
	b.WriteString(strconv.Itoa(width))
	b.WriteString(",v=")
	b.WriteString(strconv.Itoa(height))
	b.WriteString(",t=d,i=")
	b.WriteString(strconv.Itoa(id))
	b.WriteString(",p=1,C=1,q=2;")
	b.WriteString("AAAA") // 3 bytes base64
	b.WriteString("\x1b\\")
	b.WriteString("\x1b[?2026l")
	return b.Bytes()
}
