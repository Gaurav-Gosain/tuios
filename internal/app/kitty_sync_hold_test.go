package app

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// syncHoldHarness is a pane emulator of the build's backend wired to a
// passthrough the way setupKittyPassthrough wires it, synchronized-update
// probe included. tick is one render loop pass: refresh, then drain.
func syncHoldHarness(t *testing.T) (write func(string), tick func() string) {
	t.Helper()
	clientCapabilities.Store(&HostCapabilities{
		TerminalName: "kitty", KittyGraphics: true,
		TrueColor: true, CellWidth: 10, CellHeight: 20,
	})
	t.Cleanup(func() { clientCapabilities.Store(nil) })

	hostFile, err := os.CreateTemp(t.TempDir(), "hostout")
	if err != nil {
		t.Fatal(err)
	}
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: hostFile})
	const winID = "win"
	term := vt.New(80, 24)
	t.Cleanup(func() { _ = term.Close() })
	kp.SetGuestSyncProbe(winID, term.SyncUpdate)
	term.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		cur := term.CursorPosition()
		kp.ForwardCommand(cmd, rawData, winID, 0, 0, 80, 24, 0, 0,
			cur.X, cur.Y, term.ScrollbackLen(), term.IsAltScreen(), func([]byte) {})
	})
	info := &WindowPositionInfo{
		Width: 80, Height: 24, ContentWidth: 80, ContentHeight: 24,
		Visible: true, ScreenWidth: 80, ScreenHeight: 24,
	}
	write = func(s string) { _, _ = term.Write([]byte(s)) }
	tick = func() string {
		kp.RefreshAllPlacements(func() map[string]*WindowPositionInfo {
			return map[string]*WindowPositionInfo{winID: info}
		})
		return string(kp.FlushPending())
	}
	return write, tick
}

var hostAction = regexp.MustCompile(`\x1b_G[^;\x1b]*?a=([a-zA-Z])`)

// hostActions lists the kitty actions in one drain, in order.
func hostActions(out string) string {
	var b strings.Builder
	for _, m := range hostAction.FindAllStringSubmatch(out, -1) {
		b.WriteString(m[1])
	}
	return b.String()
}

// OpenTUI's Magick Arena demo, as captured from its own output: every frame
// is one synchronized update that frees the previous image, transmits the new
// one in raw RGB chunks under the same id, and places it with C=1. The render
// loop ticks while the pty is still delivering that megabyte, so a tick lands
// inside the update. That tick must not ship the delete: the host would
// present the pane without its image until a later tick placed the next one.
func TestSyncUpdateHoldsAnImageReplacementTogether(t *testing.T) {
	write, tick := syncHoldHarness(t)

	const (
		first  = "\x1b_Ga=t,f=24,s=2,v=2,i=77,m=1,q=2;AAAAAAAA\x1b\\"
		last   = "\x1b_Gm=0,q=2;AAAAAAAA\x1b\\"
		place  = "\x1b[3;1H\x1b_Ga=p,i=77,p=1,c=8,r=4,x=0,y=0,w=2,h=2,C=1,z=-1499999999,q=2\x1b\\"
		remove = "\x1b_Ga=d,d=I,i=77,q=2\x1b\\"
	)

	write("\x1b[?2026h" + first + last + place + "HUD\x1b[?2026l")
	if got := hostActions(tick()); !strings.Contains(got, "p") {
		t.Fatalf("the first frame reached the host as %q, want a placement", got)
	}

	for frame := range 3 {
		write("\x1b[?2026h" + remove + first)
		if got := hostActions(tick()); got != "" {
			t.Fatalf("frame %d: a tick inside the update sent %q to the host; the image is gone until the next placement", frame, got)
		}
		write(last + place)
		if got := hostActions(tick()); got != "" {
			t.Fatalf("frame %d: a tick inside the update sent %q to the host", frame, got)
		}
		write("HUD\x1b[?2026l")
		got := hostActions(tick())
		if !strings.HasPrefix(got, "dtp") {
			t.Fatalf("frame %d: the closed update reached the host as %q, want the delete, transmit and placement together", frame, got)
		}
	}
}

// A guest that closes one update and opens the next before the render loop
// ticks still has its finished frame shipped: only the open update waits.
func TestSyncUpdateReleasesTheFinishedFrame(t *testing.T) {
	write, tick := syncHoldHarness(t)
	frame := "\x1b_Ga=d,d=I,i=5,q=2\x1b\\" +
		"\x1b_Ga=t,f=24,s=2,v=2,i=5,q=2;AAAAAAAA\x1b\\" +
		"\x1b[3;1H\x1b_Ga=p,i=5,p=1,c=8,r=4,C=1,q=2\x1b\\"
	write("\x1b[?2026h" + frame + "\x1b[?2026l")
	tick()

	write("\x1b[?2026h" + frame + "\x1b[?2026l\x1b[?2026h\x1b_Ga=d,d=I,i=5,q=2\x1b\\")
	if got := hostActions(tick()); !strings.HasPrefix(got, "dtp") || strings.HasSuffix(got, "d") {
		t.Fatalf("the tick sent %q, want the finished frame and not the open one's delete", got)
	}
	write("\x1b[?2026l")
	if got := hostActions(tick()); got != "d" {
		t.Fatalf("closing the update sent %q, want its delete", got)
	}
}

// A guest that sends outside any synchronized update is not held at all.
func TestUnsynchronizedKittyOutputIsNotHeld(t *testing.T) {
	write, tick := syncHoldHarness(t)
	write("\x1b_Ga=t,f=24,s=2,v=2,i=9,q=2;AAAAAAAA\x1b\\")
	if got := hostActions(tick()); got != "t" {
		t.Fatalf("a transmit outside an update reached the host as %q, want it at once", got)
	}
}
