package app

import (
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// syncPane is one pane in a syncHoldHarness: an emulator of the build's
// backend wired to the shared passthrough the way setupKittyPassthrough
// wires it, synchronized-update probe included.
type syncPane struct {
	id   string
	term vt.Terminal
}

func (p *syncPane) write(s string) { _, _ = p.term.Write([]byte(s)) }

type syncHoldHarness struct {
	t     *testing.T
	kp    *KittyPassthrough
	host  *os.File
	panes map[string]*syncPane
	info  map[string]*WindowPositionInfo
}

func newSyncHoldHarness(t *testing.T) *syncHoldHarness {
	t.Helper()
	clientCapabilities.Store(&HostCapabilities{
		TerminalName: "kitty", KittyGraphics: true, KittyFileTransfer: true,
		TrueColor: true, CellWidth: 10, CellHeight: 20,
	})
	t.Cleanup(func() { clientCapabilities.Store(nil) })
	host, err := os.CreateTemp(t.TempDir(), "hostout")
	if err != nil {
		t.Fatal(err)
	}
	return &syncHoldHarness{
		t:     t,
		kp:    NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: host}),
		host:  host,
		panes: map[string]*syncPane{},
		info:  map[string]*WindowPositionInfo{},
	}
}

// pane adds a pane whose content starts at column x of the host screen.
func (h *syncHoldHarness) pane(id string, x int) *syncPane {
	term := vt.New(40, 24)
	h.t.Cleanup(func() { _ = term.Close() })
	p := &syncPane{id: id, term: term}
	kp := h.kp
	kp.SetGuestSyncProbe(id, term.SyncUpdate)
	term.KittyMainState().SetClearCallback(func() { kp.ClearWindow(id) })
	term.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		cur := term.CursorPosition()
		kp.ForwardCommand(cmd, rawData, id, x, 0, 40, 24, 0, 0,
			cur.X, cur.Y, term.ScrollbackLen(), term.IsAltScreen(), func([]byte) {})
	})
	h.panes[id] = p
	h.info[id] = &WindowPositionInfo{
		WindowX: x, Width: 40, Height: 24, ContentWidth: 40, ContentHeight: 24,
		Visible: true, ScreenWidth: 80, ScreenHeight: 24,
	}
	return p
}

// tick is one render loop pass: refresh, then drain.
func (h *syncHoldHarness) tick() string {
	h.kp.RefreshAllPlacements(func() map[string]*WindowPositionInfo { return h.info })
	return string(h.kp.FlushPending())
}

// hostWritten is what the passthrough wrote to the host directly, outside
// the render loop's drain.
func (h *syncHoldHarness) hostWritten() string {
	b, err := os.ReadFile(h.host.Name())
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

var hostAction = regexp.MustCompile(`\x1b_G[^;\x1b]*?a=([a-zA-Z])`)

// hostActions lists the kitty actions in some host output, in order.
func hostActions(out string) string {
	var b strings.Builder
	for _, m := range hostAction.FindAllStringSubmatch(out, -1) {
		b.WriteString(m[1])
	}
	return b.String()
}

// OpenTUI's Magick Arena demo, as captured from its own output: every frame
// is one synchronized update that frees the previous image, transmits the new
// one in raw RGB chunks under the same id, and places it with C=1.
const (
	arenaFirst  = "\x1b_Ga=t,f=24,s=2,v=2,i=77,m=1,q=2;AAAAAAAA\x1b\\"
	arenaLast   = "\x1b_Gm=0,q=2;AAAAAAAA\x1b\\"
	arenaPlace  = "\x1b[3;1H\x1b_Ga=p,i=77,p=1,c=8,r=4,x=0,y=0,w=2,h=2,C=1,z=-1499999999,q=2\x1b\\"
	arenaDelete = "\x1b_Ga=d,d=I,i=77,q=2\x1b\\"
	arenaFrame  = arenaDelete + arenaFirst + arenaLast + arenaPlace
)

// The render loop ticks while the pty is still delivering a frame, so a tick
// lands inside the update. That tick must not ship the delete: the host
// would present the pane without its image until a later tick placed the
// next one.
func TestSyncUpdateHoldsAnImageReplacementTogether(t *testing.T) {
	h := newSyncHoldHarness(t)
	a := h.pane("a", 0)

	a.write("\x1b[?2026h" + arenaFirst + arenaLast + arenaPlace + "HUD\x1b[?2026l")
	if got := hostActions(h.tick()); !strings.Contains(got, "p") {
		t.Fatalf("the first frame reached the host as %q, want a placement", got)
	}
	for frame := range 3 {
		a.write("\x1b[?2026h" + arenaDelete + arenaFirst)
		if got := hostActions(h.tick()); got != "" {
			t.Fatalf("frame %d: a tick inside the update sent %q to the host; the image is gone until the next placement", frame, got)
		}
		a.write(arenaLast + arenaPlace)
		if got := hostActions(h.tick()); got != "" {
			t.Fatalf("frame %d: a tick inside the update sent %q to the host", frame, got)
		}
		a.write("HUD\x1b[?2026l")
		if got := hostActions(h.tick()); !strings.HasPrefix(got, "dtp") {
			t.Fatalf("frame %d: the closed update reached the host as %q, want the delete, transmit and placement together", frame, got)
		}
	}
}

// A guest that closes one update and opens the next before the render loop
// ticks still has its finished frame shipped: only the open update waits.
func TestSyncUpdateReleasesTheFinishedFrame(t *testing.T) {
	h := newSyncHoldHarness(t)
	a := h.pane("a", 0)
	a.write("\x1b[?2026h" + arenaFrame + "\x1b[?2026l")
	h.tick()

	a.write("\x1b[?2026h" + arenaFrame + "\x1b[?2026l\x1b[?2026h" + arenaDelete)
	if got := hostActions(h.tick()); !strings.HasPrefix(got, "dtp") || strings.HasSuffix(got, "d") {
		t.Fatalf("the tick sent %q, want the finished frame and not the open one's delete", got)
	}
	a.write("\x1b[?2026l")
	if got := hostActions(h.tick()); got != "d" {
		t.Fatalf("closing the update sent %q, want its delete", got)
	}
}

// A guest that sends outside any synchronized update is not held at all.
func TestUnsynchronizedKittyOutputIsNotHeld(t *testing.T) {
	h := newSyncHoldHarness(t)
	a := h.pane("a", 0)
	a.write("\x1b_Ga=t,f=24,s=2,v=2,i=9,q=2;AAAAAAAA\x1b\\")
	if got := hostActions(h.tick()); got != "t" {
		t.Fatalf("a transmit outside an update reached the host as %q, want it at once", got)
	}
}

// One pane inside an update holds only its own output. Another pane's image
// goes out on the next tick.
func TestSyncHoldIsPerWindow(t *testing.T) {
	h := newSyncHoldHarness(t)
	a := h.pane("a", 0)
	b := h.pane("b", 40)
	a.write("\x1b[?2026h" + arenaFrame + "\x1b[?2026l")
	h.tick()

	a.write("\x1b[?2026h" + arenaDelete)
	b.write("\x1b_Ga=t,f=24,s=2,v=2,i=9,q=2;AAAAAAAA\x1b\\")
	if got := hostActions(h.tick()); got != "t" {
		t.Fatalf("with pane a inside an update, the tick sent %q, want pane b's transmit alone", got)
	}
}

// tuios hiding the images (a resize) goes to the host at once while a guest
// holds an update, and the held placement does not show the image again when
// the update closes.
func TestTuiosHideIsNotHeldBehindAGuestUpdate(t *testing.T) {
	h := newSyncHoldHarness(t)
	a := h.pane("a", 0)
	a.write("\x1b[?2026h" + arenaFrame + "\x1b[?2026l")
	h.tick()

	a.write("\x1b[?2026h" + arenaFrame)
	h.kp.HideAllPlacements()
	if got := hostActions(h.hostWritten()); got != "d" {
		t.Fatalf("the hide reached the host as %q, want its delete at once", got)
	}
	a.write("\x1b[?2026l")
	h.info["a"].Visible = false
	if got := hostActions(h.tick()); strings.Contains(got, "p") {
		t.Fatalf("the update released after the hide sent %q; its placement shows the hidden image again", got)
	}
}

// A guest that repeats 2026h and never closes the update is held no longer
// than the limit.
func TestSyncHoldEndsAtTheLimit(t *testing.T) {
	h := newSyncHoldHarness(t)
	now := time.Unix(1000, 0)
	h.kp.clock = func() time.Time { return now }
	h.kp.SetGuestSyncProbe("a", func() (bool, uint64) { return true, 1 })
	h.info["a"] = &WindowPositionInfo{Width: 40, Height: 24, ContentWidth: 40, ContentHeight: 24, Visible: true, ScreenWidth: 80, ScreenHeight: 24}
	h.kp.mu.Lock()
	h.kp.beginGuestCapture()
	h.kp.pendingOutput = append(h.kp.pendingOutput, arenaDelete...)
	h.kp.endGuestCapture("a")
	h.kp.mu.Unlock()
	if got := hostActions(h.tick()); got != "" {
		t.Fatalf("a tick inside the update sent %q", got)
	}
	now = now.Add(vt.SyncMaxHold)
	if got := hostActions(h.tick()); got != "d" {
		t.Fatalf("after the limit the tick sent %q, want the held delete", got)
	}
}

// A window may not hold more than heldWindowMaxBytes. Past it, what it holds
// is released, whole transmissions only.
func TestSyncHoldIsBounded(t *testing.T) {
	h := newSyncHoldHarness(t)
	a := h.pane("a", 0)
	const side = 512
	pixels := base64.StdEncoding.EncodeToString(make([]byte, side*side*3))
	var frame strings.Builder
	for i := 0; i < len(pixels); i += 4096 {
		end := min(i+4096, len(pixels))
		more := 0
		if end < len(pixels) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(&frame, "\x1b_Ga=t,f=24,s=%d,v=%d,i=77,m=%d,q=2;%s\x1b\\", side, side, more, pixels[i:end])
		} else {
			fmt.Fprintf(&frame, "\x1b_Gm=%d,q=2;%s\x1b\\", more, pixels[i:end])
		}
	}
	frame.WriteString(arenaPlace)

	a.write("\x1b[?2026h")
	var sent strings.Builder
	for range 2 * heldWindowMaxBytes / (side * side * 4) {
		a.write(arenaDelete + frame.String())
		sent.WriteString(h.tick())
		h.kp.mu.Lock()
		held := h.kp.heldBytes
		h.kp.mu.Unlock()
		if held > heldWindowMaxBytes {
			t.Fatalf("the window holds %d bytes, over the %d limit", held, heldWindowMaxBytes)
		}
	}
	out := sent.String()
	if out == "" {
		t.Fatal("nothing was released past the limit")
	}
	starts := strings.Count(out, "a=t,")
	ends := strings.Count(out, "m=0")
	if starts == 0 || starts != ends {
		t.Fatalf("the release holds %d transmission starts and %d ends; a transmission was split", starts, ends)
	}
}

// A write that skips the queue (a file-backed video frame placed at once)
// must not overtake a delete the same window holds for the same image.
func TestDirectVideoWriteDoesNotOvertakeHeldDelete(t *testing.T) {
	h := newSyncHoldHarness(t)
	a := h.pane("a", 0)
	f, err := os.CreateTemp(t.TempDir(), "frame")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(make([]byte, 12))
	_ = f.Close()
	path := base64.StdEncoding.EncodeToString([]byte(f.Name()))
	video := "\x1b[1;1H\x1b_Ga=T,t=f,f=24,s=2,v=2,i=5,m=1,q=2;" + path + "\x1b\\"

	a.write(video)
	h.tick()
	a.write("\x1b[?2026h\x1b[2J" + video)

	out := h.hostWritten()
	del := strings.Index(out, "a=d")
	put := strings.LastIndex(out, "a=T")
	if put < 0 {
		t.Fatalf("the video frame did not reach the host directly: %q", hostActions(out))
	}
	if del < 0 || del > put {
		t.Fatalf("the host saw %q: the direct frame overtook the held delete, so the delete removes it", hostActions(out))
	}
}
