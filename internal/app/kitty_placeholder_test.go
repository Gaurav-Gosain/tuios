package app

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// pendingString is what the passthrough has queued for the host.
func pendingString(kp *KittyPassthrough) string {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	return string(kp.pendingOutput)
}

// TestAVirtualPlacementIsForwardedAsVirtual is the fix for images in a pager.
//
// An application using Unicode placeholders transmits the image, declares that
// it occupies a box of c by r cells, and then prints the cells that say where
// that box goes. tuios used to strip the U=1 from the declaration and turn it
// into a real placement at wherever the guest's cursor happened to be, which
// put the image in the wrong place and left the cells naming an image the host
// had no virtual placement for. Nothing was drawn.
//
// Negative control: removing the cmd.Virtual branch from forwardPlace sent
// a=p with a cursor-positioning prefix and no U=1, and this failed.
func TestAVirtualPlacementIsForwardedAsVirtual(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	winID := "test-window-id-abcdef12"
	const guestID uint32 = 0x0a0b0c

	place := &vt.KittyCommand{
		Action: vt.KittyActionPlace, ImageID: guestID,
		Columns: 20, Rows: 10, Virtual: true,
	}
	// A cursor far from the origin: a virtual placement must ignore it.
	kp.ForwardCommand(place, nil, winID, 0, 0, 80, 24, 0, 0, 7, 3, 0, false, nil)

	out := pendingString(kp)
	if !strings.Contains(out, "U=1") {
		t.Errorf("the declaration lost its U=1, so the host will not tie the cells to the image:\n%q", out)
	}
	if !strings.Contains(out, "c=20") || !strings.Contains(out, "r=10") {
		t.Errorf("the declaration lost the cell box:\n%q", out)
	}
	if strings.Contains(out, "\x1b7") || strings.Contains(out, "H\x1b_G") {
		t.Errorf("a virtual placement was positioned at the cursor:\n%q", out)
	}
}

// TestAVirtualPlacementKeepsTheIDTheImageArrivedUnder is the trap in the
// middle of this. A transmit-only command, which is what these applications
// send, is passed through under a host id tuios allocates for the pane, and
// the declaration has to name that same id, or it names an image the host
// has never been sent.
//
// The transmission used to keep the guest's own id, which on the host could
// be another pane's image (FuzzKittyPassthrough). The declaration followed
// it, so both have changed together.
func TestAVirtualPlacementKeepsTheIDTheImageArrivedUnder(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	winID := "test-window-id-abcdef12"
	const guestID uint32 = 0x0a0b0c

	body := "a=t,f=24,s=1,v=1,i=658188;AAAA"
	transmit, err := vt.ParseKittyCommand([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	kp.ForwardCommand(transmit, []byte("\x1b_G"+body+"\x1b\\"), winID, 0, 0, 80, 24, 0, 0, 0, 0, 0, false, nil)
	hostID, ok := kp.HostImageID(winID, guestID)
	if !ok || hostID == guestID {
		t.Fatalf("the transmission was not given a host id of the pane's own (got %d, %v)", hostID, ok)
	}
	if out := pendingString(kp); !strings.Contains(out, fmt.Sprintf("i=%d;", hostID)) {
		t.Errorf("the transmission did not reach the host under its host id %d:\n%q", hostID, out)
	}
	kp.mu.Lock()
	kp.pendingOutput = nil
	kp.mu.Unlock()

	place := &vt.KittyCommand{
		Action: vt.KittyActionPlace, ImageID: guestID,
		Columns: 4, Rows: 2, Virtual: true,
	}
	kp.ForwardCommand(place, nil, winID, 0, 0, 80, 24, 0, 0, 0, 0, 0, false, nil)

	if out := pendingString(kp); !strings.Contains(out, fmt.Sprintf("i=%d,", hostID)) {
		t.Errorf("declaration names the wrong image, want host id %d:\n%q", hostID, out)
	}
}

// TestAVirtualPlacementUsesAnExistingHostID is the other half: when some other
// path already re-registered the image under an id of tuios's, the declaration
// has to name that one, because that is also what the cells are rewritten to.
func TestAVirtualPlacementUsesAnExistingHostID(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	winID := "test-window-id-abcdef12"
	const guestID uint32 = 42

	kp.mu.Lock()
	kp.imageIDMap[winID] = map[uint32]uint32{guestID: 9001}
	kp.mu.Unlock()

	place := &vt.KittyCommand{
		Action: vt.KittyActionPlace, ImageID: guestID,
		Columns: 4, Rows: 2, Virtual: true,
	}
	kp.ForwardCommand(place, nil, winID, 0, 0, 80, 24, 0, 0, 0, 0, 0, false, nil)

	if out := pendingString(kp); !strings.Contains(out, "i=9001") {
		t.Errorf("declaration did not use the host id the image was registered under:\n%q", out)
	}
}

// TestClosingAWindowDeletesItsPlaceholderImages stops a leak. These images
// carry no placement, so the placement teardown cannot see them, and a pager
// scrolled through a long document would otherwise leave every picture
// resident in the host terminal after the pane was gone.
//
// Negative control: dropping the deleteVirtualImages call from OnWindowClose
// emitted no delete at all and this failed.
func TestClosingAWindowDeletesItsPlaceholderImages(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	winID := "test-window-id-abcdef12"

	place := &vt.KittyCommand{
		Action: vt.KittyActionPlace, ImageID: 1234,
		Columns: 4, Rows: 2, Virtual: true,
	}
	kp.ForwardCommand(place, nil, winID, 0, 0, 80, 24, 0, 0, 0, 0, 0, false, nil)

	kp.mu.Lock()
	kp.pendingOutput = nil
	kp.mu.Unlock()

	hostID, _ := kp.HostImageID(winID, 1234)
	kp.OnWindowClose(winID)

	out := pendingString(kp)
	if !strings.Contains(out, fmt.Sprintf("a=d,d=I,i=%d,", hostID)) {
		t.Errorf("closing the window did not free the image:\n%q", out)
	}
	kp.mu.Lock()
	left := len(kp.virtualImages[winID])
	kp.mu.Unlock()
	if left != 0 {
		t.Errorf("%d placeholder image(s) still recorded after the window closed", left)
	}
}

// TestDimmingLeavesAPlaceholderCellAlone is the subtle one. The foreground of
// a placeholder cell is not a colour, it is the image's id, so blending it
// renames the image to one the host has never heard of and the picture
// disappears on every unfocused pane.
//
// Negative control: removing the IsKittyPlaceholder guard from dimCell changed
// the foreground and this failed.
func TestDimmingLeavesAPlaceholderCellAlone(t *testing.T) {
	const id = 0x0a0b0c
	src := &uv.Cell{
		Content: string(kitty.Placeholder) + string(kitty.Diacritic(0)),
		Width:   1,
		Style:   uv.Style{Fg: color.RGBA{R: 0x0a, G: 0x0b, B: 0x0c, A: 0xff}},
	}
	var scratch uv.Cell
	got := dimCell(&scratch, src, color.White, color.Black, 0.5)

	r, g, b, _ := got.Style.Fg.RGBA()
	gotID := uint32(r>>8)<<16 | uint32(g>>8)<<8 | uint32(b>>8)
	if gotID != id {
		t.Errorf("dimming renamed the image to %#x, want %#x", gotID, id)
	}

	// An ordinary cell must still dim, or the guard is too wide.
	text := &uv.Cell{Content: "a", Width: 1, Style: uv.Style{Fg: color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}}}
	var scratch2 uv.Cell
	dimmed := dimCell(&scratch2, text, color.White, color.Black, 0.5)
	if dr, _, _, _ := dimmed.Style.Fg.RGBA(); dr>>8 == 0xff {
		t.Error("an ordinary cell was not dimmed")
	}
}

// TestAVirtualPlacementReservesNoRows keeps tuios out of the way. The
// reservation exists so an image placed at the cursor does not overwrite rows
// the guest believes are empty. An application using placeholders prints the
// cells the image occupies itself, so reserving rows for it would push its own
// output down by the height of every image on the page.
//
// Negative control: dropping the cmd.Virtual break returned a 10-row
// reservation and this failed.
func TestAVirtualPlacementReservesNoRows(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	winID := "test-window-id-abcdef12"

	place := &vt.KittyCommand{
		Action: vt.KittyActionPlace, ImageID: 3, Columns: 20, Rows: 10, Virtual: true,
	}
	got := kp.ForwardCommand(place, nil, winID, 0, 0, 80, 24, 0, 0, 0, 0, 0, false, nil)
	if got != nil {
		t.Errorf("a virtual placement asked for %d rows of space, want none", got.Rows)
	}

	// A real placement still reserves, or the guard is too wide.
	real := &vt.KittyCommand{
		Action: vt.KittyActionPlace, ImageID: 4, Columns: 20, Rows: 10,
	}
	if got := kp.ForwardCommand(real, nil, winID, 0, 0, 80, 24, 0, 0, 0, 0, 0, false, nil); got == nil {
		t.Error("a real placement reserved nothing")
	}
}

// TestTheWholePlaceholderFlow drives the exact shape glamour emits for an
// image in a pager: transmit the image, declare the box it occupies, print the
// placeholder grid. It checks the two halves agree, which is the thing that
// was broken and the thing unit tests of either half alone cannot see.
func TestTheWholePlaceholderFlow(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	winID := "test-window-id-abcdef12"
	const guestID uint32 = 0x0a0b0c
	const cols, rows = 4, 2

	// The guest's emulator, wired the way a real pane's is.
	term := vt.New(40, 10)
	// Placeholder cells are kept only on a host that draws them, which is what
	// setupKittyPassthrough decides per pane. See kitty_placeholder_caps.go.
	term.SetKittyPlaceholderMode(vt.KittyPlaceholdersKeep)
	term.SetKittyImageIDTranslator(func(g uint32) (uint32, bool) {
		return kp.HostImageID(winID, g)
	})
	term.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, raw []byte) {
		kp.ForwardCommand(cmd, raw, winID, 0, 0, 80, 24, 0, 0, 0, 0, 0, false, nil)
	})

	var seq strings.Builder
	fmt.Fprintf(&seq, "\x1b_Ga=t,f=100,t=d,i=%d,q=2;aVZCT1J3MEtHZ28=\x1b\\", guestID)
	fmt.Fprintf(&seq, "\x1b_Ga=p,U=1,i=%d,c=%d,r=%d,q=2\x1b\\", guestID, cols, rows)
	for row := range rows {
		fmt.Fprintf(&seq, "\x1b[38;2;%d;%d;%dm", (guestID>>16)&0xff, (guestID>>8)&0xff, guestID&0xff)
		seq.WriteRune(kitty.Placeholder)
		seq.WriteRune(kitty.Diacritic(row))
		for range cols - 1 {
			seq.WriteRune(kitty.Placeholder)
		}
		seq.WriteString("\x1b[39m\r\n")
	}
	if _, err := term.Write([]byte(seq.String())); err != nil {
		t.Fatalf("write: %v", err)
	}

	host := pendingString(kp)
	if !strings.Contains(host, "a=t") {
		t.Errorf("the image never reached the host:\n%q", host)
	}
	if !strings.Contains(host, "U=1") {
		t.Errorf("the host was never told the image is placed by its cells:\n%q", host)
	}

	// Every cell of the grid is present, and names the same image the host was
	// told about.
	for y := range rows {
		for x := range cols {
			cell := term.CellAt(x, y)
			if cell == nil || !vt.IsKittyPlaceholder(cell.Content) {
				t.Fatalf("cell (%d,%d) is not a placeholder: %+v", x, y, cell)
			}
			r, g, b, _ := cell.Style.Fg.RGBA()
			named := uint32(r>>8)<<16 | uint32(g>>8)<<8 | uint32(b>>8)
			if !strings.Contains(host, fmt.Sprintf("i=%d", named)) {
				t.Errorf("cell (%d,%d) names image %d, which the host was never told about:\n%q",
					x, y, named, host)
			}
		}
	}
}

// TestClearingTheScreenFreesPlaceholderImages stops a pager walked through a
// directory of pictures from leaving every one of them resident in the host.
// The clear takes the cells with it, so nothing names those images again.
func TestClearingTheScreenFreesPlaceholderImages(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	winID := "test-window-id-abcdef12"

	place := &vt.KittyCommand{
		Action: vt.KittyActionPlace, ImageID: 777, Columns: 4, Rows: 2, Virtual: true,
	}
	kp.ForwardCommand(place, nil, winID, 0, 0, 80, 24, 0, 0, 0, 0, 0, false, nil)
	kp.mu.Lock()
	kp.pendingOutput = nil
	kp.mu.Unlock()

	hostID, _ := kp.HostImageID(winID, 777)
	kp.ClearWindow(winID)

	if out := pendingString(kp); !strings.Contains(out, fmt.Sprintf("a=d,d=I,i=%d,", hostID)) {
		t.Errorf("clearing the screen did not free the image:\n%q", out)
	}
}
