package app

import (
	"bytes"
	"os"
	"strconv"
	"testing"

	"tuios.dev/vt"
)

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
			winX, winY, winW-2*border, winH-2*border, border, border,
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

// TestBorderlessFullPaneImageClamped exercises the clamp branch: a borderless
// tiled pane whose image genuinely occupies every screen row, including the
// last. The image must still be placed (a=p), but clamped to leave the final
// row free so the host terminal does not scroll.
func TestBorderlessFullPaneImageClamped(t *testing.T) {
	frame := buildDirectFrame(1, 1200, 800)
	forwarded := feedTBFrameBorder(t, frame, 120, 40, 0)

	if !bytes.Contains(forwarded, []byte("a=p,")) {
		t.Fatal("borderless full-pane image hidden instead of clamped (no a=p)")
	}
	if !bytes.Contains(forwarded, []byte("r=39")) {
		t.Fatalf("expected clamped placement r=39, forwarded=%q", forwarded)
	}
	if !bytes.Contains(forwarded, []byte("c=120")) {
		t.Fatalf("expected the full pane width c=120, forwarded=%q", forwarded)
	}
}

func buildDirectFrame(id, width, height int) []byte {
	var b bytes.Buffer
	b.WriteString("\x1b[?1049h\x1b[H")
	b.WriteString("\x1b[?2026h")
	b.WriteString("\x1b_Ga=T,f=32,s=")
	b.WriteString(strconv.Itoa(width))
	b.WriteString(",v=")
	b.WriteString(strconv.Itoa(height))
	b.WriteString(",t=d,i=")
	b.WriteString(strconv.Itoa(id))
	b.WriteString(",p=1,C=1,q=2;")
	b.WriteString("AAAA")
	b.WriteString("\x1b\\")
	b.WriteString("\x1b[?2026l")
	return b.Bytes()
}
