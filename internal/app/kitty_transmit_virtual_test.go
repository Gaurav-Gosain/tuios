package app

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// superfileSequence is what superfile (v1.6.0, src/pkg/file_preview/kitty.go)
// writes for an image preview: one a=T command that both transmits the image
// and declares a virtual placement of c by r cells, sent in chunks, with no
// C=1. Then the cursor goes to the preview panel and the placeholder grid is
// printed there, one CUP per row.
func superfileSequence(guestID uint32, cols, rows, atRow, atCol int) string {
	const w, h = 4, 2
	pixels := make([]byte, w*h*4)
	for i := range pixels {
		pixels[i] = byte(i * 7)
	}
	payload := base64.StdEncoding.EncodeToString(pixels)
	cut := len(payload) / 2 / 4 * 4

	var seq strings.Builder
	fmt.Fprintf(&seq, "\x1b_Ga=d,d=i,i=%d\x1b\\", guestID)
	fmt.Fprintf(&seq, "\x1b_Gq=2,i=%d,s=%d,v=%d,U=1,c=%d,r=%d,a=T,m=1;%s\x1b\\",
		guestID, w, h, cols, rows, payload[:cut])
	fmt.Fprintf(&seq, "\x1b_Gi=%d,q=2;%s\x1b\\", guestID, payload[cut:])
	for row := range rows {
		fmt.Fprintf(&seq, "\x1b[%d;%dH", atRow+row+1, atCol+1)
		fmt.Fprintf(&seq, "\x1b[38;2;%d;%d;%dm", (guestID>>16)&0xff, (guestID>>8)&0xff, guestID&0xff)
		seq.WriteRune(kitty.Placeholder)
		seq.WriteRune(kitty.Diacritic(row))
		seq.WriteRune(kitty.Diacritic(0))
		for range cols - 1 {
			seq.WriteRune(kitty.Placeholder)
		}
		seq.WriteString("\x1b[39m")
	}
	return seq.String()
}

// TestATransmitThatDeclaresAVirtualPlacement is issue 292: superfile's image
// preview inside tuios.
//
// A single a=T command with U=1 transmits the image and creates a virtual
// placement. kitty draws nothing for it and moves no cursor; the placeholder
// cells the application prints next say where the image goes. tuios read the
// a=T as transmit-and-place-at-the-cursor: it reserved r rows below the
// cursor, which moved the pane's cursor under the application and scattered
// its later output, and it placed a real copy of the image wherever that
// cursor was, while the host never learnt of the virtual placement the cells
// name.
func TestATransmitThatDeclaresAVirtualPlacement(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	winID := "test-window-id-abcdef12"
	const guestID uint32 = 40376
	const cols, rows = 6, 3

	term := vt.New(40, 12)
	term.SetKittyPlaceholderMode(vt.KittyPlaceholdersKeep)
	term.SetKittyImageIDTranslator(func(g uint32) (uint32, bool) {
		return kp.HostImageID(winID, g)
	})
	// Wired the way setupKittyPassthrough wires a pane, reservation included.
	reserved := 0
	term.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, raw []byte) {
		pos := term.CursorPosition()
		result := kp.ForwardCommand(cmd, raw, winID, 0, 0, 40, 12, 0, 0, pos.X, pos.Y, 0, true, nil)
		if result != nil && result.Rows > 0 && result.CursorMove == 0 {
			reserved += result.Rows
			term.ReserveImageSpace(result.Rows, result.Cols)
		}
	})

	// Park the cursor on the last row, where a full-screen application often
	// leaves it: a reservation there scrolls the whole screen.
	if _, err := term.Write([]byte("\x1b[?1049h\x1b[1;1HTOP\x1b[12;1H")); err != nil {
		t.Fatal(err)
	}
	if _, err := term.Write([]byte(superfileSequence(guestID, cols, rows, 4, 10))); err != nil {
		t.Fatal(err)
	}

	if reserved != 0 {
		t.Errorf("the transmission reserved %d rows; a virtual placement moves no cursor", reserved)
	}
	if cell := term.CellAt(0, 0); cell == nil || cell.Content != "T" {
		t.Errorf("the screen scrolled: row 0 starts with %+v, want the T of TOP", cell)
	}

	hostID, ok := kp.HostImageID(winID, guestID)
	if !ok {
		t.Fatal("the image was never given a host id")
	}
	host := pendingString(kp)
	decl := fmt.Sprintf("a=p,U=1,i=%d,c=%d,r=%d", hostID, cols, rows)
	if !strings.Contains(host, decl) {
		t.Errorf("the host was never told about the virtual placement (%s):\n%.400q", decl, host)
	}
	kp.mu.Lock()
	placed := len(kp.placements[winID])
	kp.mu.Unlock()
	if placed != 0 {
		t.Errorf("a real placement was recorded for a virtual one; it would be drawn at the cursor")
	}

	// The cells are where the application put them and name the host image.
	for y := 4; y < 4+rows; y++ {
		for x := 10; x < 10+cols; x++ {
			cell := term.CellAt(x, y)
			if cell == nil || !vt.IsKittyPlaceholder(cell.Content) {
				t.Fatalf("cell (%d,%d) is not a placeholder: %+v", x, y, cell)
			}
			if named, _ := vt.KittyPlaceholderImageID(cell.Content, cell.Style.Fg); named != hostID {
				t.Fatalf("cell (%d,%d) names image %d, want host id %d", x, y, named, hostID)
			}
		}
	}
}

// TestASingleChunkTransmitWithAVirtualPlacement is the unchunked form, which
// takes the other branch in ForwardCommand.
func TestASingleChunkTransmitWithAVirtualPlacement(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	winID := "test-window-id-abcdef12"
	body := "a=T,U=1,i=77,f=32,s=1,v=1,c=5,r=2,q=2;AAAAAA=="
	cmd, err := vt.ParseKittyCommand([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if got := kp.ForwardCommand(cmd, []byte("\x1b_G"+body+"\x1b\\"), winID, 0, 0, 80, 24, 0, 0, 3, 20, 0, false, nil); got != nil {
		t.Errorf("reserved %d rows for a virtual placement", got.Rows)
	}
	hostID, _ := kp.HostImageID(winID, 77)
	if out := pendingString(kp); !strings.Contains(out, fmt.Sprintf("a=p,U=1,i=%d,c=5,r=2", hostID)) {
		t.Errorf("the virtual placement did not reach the host:\n%q", out)
	}
	kp.mu.Lock()
	placed := len(kp.placements[winID])
	kp.mu.Unlock()
	if placed != 0 {
		t.Error("a real placement was recorded for a virtual one")
	}
}

// TestCellsBeforeTheImageNameTheSameHostID is superfile inside a pane. Its
// view (the placeholder cells) can reach the pane before the image, and each
// preview deletes the image's id by d=i and then transmits under that id. The
// cells get a host id when they are stored and are not printed again, so the
// id the image then arrives under has to be that same one.
//
// Dropping the mapping on the delete gave the transmit a fresh host id, and
// the cells named an image the host never got a placement for.
//
// Negative control: deleting the mapping in the d=i branch of forwardDelete
// failed this, cells naming host id 1 and the placement naming 2.
func TestCellsBeforeTheImageNameTheSameHostID(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	winID := "test-window-id-abcdef12"
	const guestID uint32 = 31485
	term := vt.New(40, 6)
	term.SetKittyPlaceholderMode(vt.KittyPlaceholdersKeep)
	term.SetKittyImageIDTranslator(func(g uint32) (uint32, bool) {
		return kp.HostImageIDForPlaceholder(winID, g)
	})
	term.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, raw []byte) {
		kp.ForwardCommand(cmd, raw, winID, 0, 0, 40, 6, 0, 0, 0, 0, 0, true, nil)
	})
	seq := superfileSequence(guestID, 4, 2, 1, 1)
	cells := seq[strings.Index(seq, "\x1b[2;2H"):]
	image := seq[:strings.Index(seq, "\x1b[2;2H")]
	if _, err := term.Write([]byte(cells + image)); err != nil {
		t.Fatal(err)
	}
	cell := term.CellAt(1, 1)
	named, _ := vt.KittyPlaceholderImageID(cell.Content, cell.Style.Fg)
	host := pendingString(kp)
	if !strings.Contains(host, fmt.Sprintf("a=p,U=1,i=%d,", named)) {
		t.Errorf("the cells name host image %d, and the host was told about another:\n%q", named, host)
	}
}

// TestAClosedWindowGivesItsLowIDsBack keeps host ids small. A placeholder
// cell names an id below 256 with a 256-colour index, which survives a
// 256-colour host. Ids were only ever counted upward, so every window opened
// and closed used that range up for good.
//
// Negative control: allocating from the counter alone gave the new window's
// image host id 2.
func TestAClosedWindowGivesItsLowIDsBack(t *testing.T) {
	kp := newTestKittyPassthrough(t)
	send := func(win, body string) {
		cmd, err := vt.ParseKittyCommand([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		kp.ForwardCommand(cmd, []byte("\x1b_G"+body+"\x1b\\"), win, 0, 0, 80, 24, 0, 0, 0, 0, 0, true, nil)
	}
	send("window-one-abcdef12", "a=T,U=1,i=7,f=32,s=1,v=1,c=4,r=2,q=2;AAAAAA==")
	first, _ := kp.HostImageID("window-one-abcdef12", 7)
	kp.OnWindowClose("window-one-abcdef12")
	if out := pendingString(kp); !strings.Contains(out, fmt.Sprintf("a=d,d=I,i=%d,", first)) {
		t.Errorf("closing the window did not free its image on the host:\n%q", out)
	}
	send("window-two-abcdef12", "a=T,U=1,i=9,f=32,s=1,v=1,c=4,r=2,q=2;AAAAAA==")
	if second, _ := kp.HostImageID("window-two-abcdef12", 9); second != first {
		t.Errorf("the next image got host id %d, want the freed %d", second, first)
	}
}
