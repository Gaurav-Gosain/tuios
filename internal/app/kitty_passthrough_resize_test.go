package app

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// placementHarness wires a real VT emulator to a native kitty passthrough for a
// single full-screen pane and streams a browser-style direct frame (a=T, i=1)
// into it. It returns the passthrough, the emulator, a mutable window-info the
// caller drives, and a refresh function that returns everything forwarded to the
// host on that refresh.
func placementHarness(t *testing.T, screenW, screenH, border int) (*KittyPassthrough, *vt.Emulator, *WindowPositionInfo, func() string) {
	t.Helper()
	clientCapabilities.Store(&HostCapabilities{
		TerminalName: "kitty", KittyGraphics: true, KittyFileTransfer: true,
		TrueColor: true, CellWidth: 10, CellHeight: 20,
	})
	t.Cleanup(func() { clientCapabilities.Store(nil) })

	hostFile, err := os.CreateTemp(t.TempDir(), "hostout")
	if err != nil {
		t.Fatal(err)
	}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: hostFile})
	if kp.inlineGraphics {
		t.Fatal("expected native mode")
	}

	winID := "win"
	em := vt.NewEmulator(screenW-2*border, screenH-2*border)
	t.Cleanup(func() { _ = em.Close() })
	em.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		cur := em.CursorPosition()
		kp.ForwardCommand(cmd, rawData, winID, 0, 0, screenW-2*border, screenH-2*border, border, border,
			cur.X, cur.Y, em.ScrollbackLen(), em.IsAltScreen(), func([]byte) {})
	})
	// Alt screen + a frame that fills the pane exactly (contentW*cellW x
	// contentH*cellH), the way a full-window graphics app draws.
	_, _ = em.Write([]byte("\x1b[?1049h\x1b[H"))
	imgW := (screenW - 2*border) * 10
	imgH := (screenH - 2*border) * 20
	streamBrowserFrame(em, 1, imgW, imgH)

	info := &WindowPositionInfo{
		WindowX: 0, WindowY: 0, ContentOffsetX: border, ContentOffsetY: border,
		Width: screenW, Height: screenH,
		ContentWidth: screenW - 2*border, ContentHeight: screenH - 2*border,
		Visible:     true,
		ScreenWidth: screenW, ScreenHeight: screenH,
		IsAltScreen: em.IsAltScreen(), ScrollbackLen: em.ScrollbackLen(),
	}
	refresh := func() string {
		kp.RefreshAllPlacements(func() map[string]*WindowPositionInfo {
			return map[string]*WindowPositionInfo{winID: info}
		})
		return string(kp.FlushPending())
	}
	return kp, em, info, refresh
}

// streamBrowserFrame writes one direct transmit+place (a=T, reused id) frame, as
// terminal-browser emits every rendered frame.
func streamBrowserFrame(em *vt.Emulator, id, w, h int) {
	var b strings.Builder
	b.WriteString("\x1b_Ga=T,f=32,s=")
	b.WriteString(strconv.Itoa(w))
	b.WriteString(",v=")
	b.WriteString(strconv.Itoa(h))
	b.WriteString(",t=d,i=")
	b.WriteString(strconv.Itoa(id))
	b.WriteString(",p=1,C=1,q=2;AAAA\x1b\\")
	_, _ = em.Write([]byte(b.String()))
}

func countCmd(s, needle string) int { return strings.Count(s, needle) }

func lastPlacement(s string) string {
	i := strings.LastIndex(s, "a=p")
	if i < 0 {
		return ""
	}
	if j := strings.Index(s[i:], "\x1b\\"); j >= 0 {
		return s[i : i+j]
	}
	return s[i:]
}

// TestResizeCoalescesPlacementChurn is the objective anti-flicker proof. During
// an interactive resize the pane size changes every render tick while the guest
// image is still the old size (its PTY resize is deferred). The passthrough must
// NOT re-clip and re-place the stale image on every tick - that churn is the
// flicker. It must hold the last placement and re-place once when the size
// settles.
//
// Fails on main, where every size-changing tick emits a fresh a=p.
func TestResizeCoalescesPlacementChurn(t *testing.T) {
	_, _, info, refresh := placementHarness(t, 120, 40, 1)

	// Prime: one refresh places the image at the starting size.
	_ = refresh()

	// Drag-resize: the window's rectangle shrinks every tick while the guest is
	// told nothing, because the PTY resize is deferred until the gesture settles
	// (resize_deferral.go). ContentWidth/ContentHeight are what the guest has
	// been told, so they deliberately do not move here: the image is still the
	// size they name, and there is nothing to re-place it against.
	info.IsBeingManipulated = true
	sizes := [][2]int{{110, 38}, {100, 36}, {90, 34}, {80, 32}, {70, 30}}
	churn := 0
	for _, s := range sizes {
		info.Width, info.Height = s[0], s[1]
		out := refresh()
		churn += countCmd(out, "a=p") + countCmd(out, "a=d")
	}
	if churn > 0 {
		t.Fatalf("resize emitted %d placement commands over %d size-changing ticks; "+
			"must hold the frame during an interactive resize (flicker)", churn, len(sizes))
	}

	// Settle: the gesture ends and the guest is finally told its new size, which
	// is the first moment the image can be measured against anything new.
	// Exactly one re-place must bring it to the settled geometry.
	info.IsBeingManipulated = false
	info.ContentWidth, info.ContentHeight = 70-2, 30-2
	settle := refresh()
	if countCmd(settle, "a=p") != 1 {
		t.Fatalf("after resize settled, expected exactly one a=p, got %d (%q)",
			countCmd(settle, "a=p"), settle)
	}
}

// TestPlacementIdempotentOnUnrelatedRefresh proves that refreshing repeatedly
// while the browser pane's geometry is unchanged - which is what happens when a
// SIBLING pane redraws or scrolls and re-invokes the render loop - emits nothing
// after the first placement. The visible region must be byte-stable: no stretch,
// no ratcheting shrink to invisible, no delete+place churn.
func TestPlacementIdempotentOnUnrelatedRefresh(t *testing.T) {
	_, _, _, refresh := placementHarness(t, 120, 40, 0)

	first := refresh()
	if countCmd(first, "a=p") != 1 {
		t.Fatalf("first refresh should place once, got %d a=p", countCmd(first, "a=p"))
	}

	// Many unrelated renders (sibling pane activity). The browser geometry never
	// changes, so nothing may be forwarded.
	for i := range 8 {
		out := refresh()
		if out != "" {
			t.Fatalf("unrelated refresh %d churned the placement (%q); a sibling redraw "+
				"must not re-place or resize the browser image", i, out)
		}
	}
}

// TestPlacementStableAcrossStreamedFrames proves the visible region is a pure
// function of the pane rect: streaming identical frames at a fixed pane size
// yields byte-identical placements frame after frame (the #116 edge clamp is
// idempotent, never accumulating into a shrinking region).
func TestPlacementStableAcrossStreamedFrames(t *testing.T) {
	_, em, _, refresh := placementHarness(t, 120, 40, 0)

	var want string
	for frame := range 6 {
		streamBrowserFrame(em, 1, 1200, 800)
		got := lastPlacement(refresh())
		if got == "" {
			t.Fatalf("frame %d produced no placement", frame)
		}
		if frame == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("frame %d placement changed with no geometry change:\n got=%q\nwant=%q",
				frame, got, want)
		}
	}
}
