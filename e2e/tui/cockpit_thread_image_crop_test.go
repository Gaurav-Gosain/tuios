package tuie2e

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
	"github.com/charmbracelet/x/ansi/kitty"
	"tuios.dev/vt"
)

// TestCockpitThreadImageCropsWhenScrolled scrolls the thread of the agents
// view so the pasted image card is cut by the thread's edge, and reads what
// the client put on its kitty host. A card the window cuts shows the part
// that is in the window, cropped, not nothing and not the whole picture
// squeezed in.
//
// The ways it can go wrong, each an assertion here:
//   - nothing is placed while the card is cut, so the picture vanishes
//     (the bug the person saw);
//   - the whole picture is placed over the visible rows, squeezed, or at the
//     card's top past the thread's edge;
//   - the crop shows the wrong rows: the picture's top in the visible cells
//     when its top is the part cut off;
//   - an old placement is left behind after a scroll, a second picture over
//     the text;
//   - a cut card is placed over the line under it;
//   - a card scrolled off the thread is still placed.
//
// The card is 9 rows here. The thread is short enough that its top cuts the
// card as J scrolls the card up, three rows a step. Frames of each step, with
// the host's pictures drawn in, go under artifactDir.
func TestCockpitThreadImageCropsWhenScrolled(t *testing.T) {
	term, host := startCardsThread(t, 160, 52, "")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	// The thread from its top, where the card is whole.
	if err := term.WaitForText("Now fix the padding.", uiTimeout); err != nil {
		t.Fatalf("the thread never showed: %v\n%s", err, term.Snapshot())
	}
	threadTop(t, term)
	headRow, headCol := findText(t, term, "PNG image  320×180")
	whole := waitPlaced(t, term, host, func(p hostPlace) bool { return p.row == headRow+1 && p.col == headCol })
	cardRows := whole.rows
	if whole.srcH != 0 && whole.srcY != 0 {
		t.Fatalf("ASSERTION: the whole card is placed cropped: %+v", whole)
	}
	rowPx := whole.imgH / cardRows
	saveCockpitFrames(t, term, "crop-0-whole", hostPictures(t, []byte(host.String()))...)

	// A shorter screen cuts the card's bottom: the rows on screen show the
	// picture's top.
	if err := term.Resize(160, 40); err != nil {
		t.Fatal(err)
	}
	threadTop(t, term)
	headRow, headCol = findText(t, term, "PNG image  320×180")
	pl, ok := waitPlacedOK(term, host, func(p hostPlace) bool { return p.img == whole.img && p.row == headRow+1 && p.col == headCol })
	saveCockpitFrames(t, term, "crop-bottom", hostPictures(t, []byte(host.String()))...)
	if !ok {
		t.Fatalf("ASSERTION: the card's bottom is cut by the thread's edge and no picture is on its rows; placed: %+v\n%s",
			livePlaces([]byte(host.String())), term.Snapshot())
	}
	if pl.rows <= 0 || pl.rows >= cardRows || pl.srcY != 0 || pl.srcH != pl.rows*rowPx || pl.srcW != whole.imgW {
		t.Fatalf("ASSERTION: the bottom-cut card shows the wrong part: %d of %d rows, want the source rect y=0 h=%d w=%d, got %+v",
			pl.rows, cardRows, pl.rows*rowPx, whole.imgW, pl)
	}
	if n := countPlaced([]byte(host.String()), whole.img); n != 1 {
		t.Fatalf("ASSERTION: the picture is placed %d times after the resize, want once: %+v", n, livePlaces([]byte(host.String())))
	}
	if err := term.Resize(160, 52); err != nil {
		t.Fatal(err)
	}
	threadTop(t, term)

	// J scrolls the thread up three rows a step. The card's last row is the
	// row above its blank line and the turn line.
	cutSeen, goneSeen, threadTopRow := false, false, -1
	for step := 1; step <= 8; step++ {
		if err := term.SendKeys("J"); err != nil {
			t.Fatal(err)
		}
		_ = term.WaitStable(uiTimeout)
		text := term.Screen().Text()
		if !strings.Contains(text, "Worked for 1m 0s") {
			break
		}
		turnRow, _ := findText(t, term, "Worked for 1m 0s")
		cardEnd := turnRow - 2 // the card's last row
		if strings.Contains(text, "PNG image  320×180") {
			continue // the card is whole yet
		}
		frame := fmt.Sprintf("crop-%d", step)
		if threadTopRow >= 0 && cardEnd < threadTopRow {
			// The card has gone above the thread's top: nothing shows it.
			_ = term.WaitStable(uiTimeout)
			saveCockpitFrames(t, term, frame, hostPictures(t, []byte(host.String()))...)
			if n := countPlaced([]byte(host.String()), whole.img); n != 0 {
				t.Fatalf("ASSERTION: step %d: the card is off the thread and its picture is still placed %d times: %+v",
					step, n, livePlaces([]byte(host.String())))
			}
			goneSeen = true
			break
		}
		pl, ok := waitPlacedOK(term, host, func(p hostPlace) bool { return p.img == whole.img && p.row+p.rows-1 == cardEnd })
		saveCockpitFrames(t, term, frame, hostPictures(t, []byte(host.String()))...)
		if !ok {
			t.Fatalf("ASSERTION: step %d: the card is cut by the thread's top and no picture ends on its last row %d; placed: %+v\n%s",
				step, cardEnd, livePlaces([]byte(host.String())), term.Snapshot())
		}
		cut := cardRows - pl.rows
		if pl.rows <= 0 || cut < 0 {
			t.Fatalf("ASSERTION: step %d: the card is placed on %d rows of %d: %+v", step, pl.rows, cardRows, pl)
		}
		if cut == 0 {
			// Only the head line went: every picture row is on screen.
			if pl.srcH != 0 && pl.srcY != 0 {
				t.Fatalf("ASSERTION: step %d: the whole card is placed cropped: %+v", step, pl)
			}
			continue
		}
		cutSeen = true
		threadTopRow = pl.row
		if pl.srcY != cut*rowPx || pl.srcH != pl.rows*rowPx || pl.srcW != whole.imgW {
			t.Fatalf("ASSERTION: step %d: the crop shows the wrong part: %d rows cut, want the source rect y=%d h=%d w=%d, got y=%d h=%d w=%d",
				step, cut, cut*rowPx, pl.rows*rowPx, whole.imgW, pl.srcY, pl.srcH, pl.srcW)
		}
		if n := countPlaced([]byte(host.String()), whole.img); n != 1 {
			t.Fatalf("ASSERTION: step %d: the picture is placed %d times, want once: %+v", step, n, livePlaces([]byte(host.String())))
		}
	}
	if !cutSeen || !goneSeen {
		t.Fatalf("the test never scrolled the card half out and then off (cut %v, gone %v)\n%s", cutSeen, goneSeen, term.Snapshot())
	}
	alive(t, term, "after scrolling the image card out")
}

// hostPlace is a live placement on the host: its cells, its source rect
// (zero for the whole picture), and the picture's size in pixels.
type hostPlace struct {
	img, p                 uint32
	row, col, cols, rows   int
	srcX, srcY, srcW, srcH int
	imgW, imgH             int
}

// livePlaces replays the kitty graphics in stream as the host does, as
// hostPictures does, and returns the placements live at the end.
func livePlaces(stream []byte) []hostPlace {
	data := map[uint32][]byte{}
	live := map[gfxKey]hostPlace{}
	var sending uint32
	for _, m := range placedAPC.FindAllSubmatch(stream, -1) {
		if bytes.Equal(m[0], []byte("\x1b[2J")) {
			clear(live)
			clear(data)
			continue
		}
		p := kittyParams(string(m[3]))
		id := atoiU32(p["i"])
		switch p["a"] {
		case "t", "T":
			sending = id
			data[id] = append([]byte(nil), m[4]...)
		case "":
			data[sending] = append(data[sending], m[4]...)
		case "p":
			// A virtual placement (U=1) has no position: placeholder cells
			// place it.
			if _, held := data[id]; !held || p["U"] == "1" {
				continue
			}
			at := func(k string) int { v, _ := strconv.Atoi(p[k]); return v }
			r, _ := strconv.Atoi(string(m[1]))
			c, _ := strconv.Atoi(string(m[2]))
			hp := hostPlace{img: id, p: atoiU32(p["p"]), row: r - 1, col: c - 1, cols: at("c"), rows: at("r"),
				srcX: at("x"), srcY: at("y"), srcW: at("w"), srcH: at("h")}
			if raw, err := base64.StdEncoding.DecodeString(string(data[id])); err == nil {
				if cfg, err := png.DecodeConfig(bytes.NewReader(raw)); err == nil {
					hp.imgW, hp.imgH = cfg.Width, cfg.Height
				}
			}
			live[gfxKey{id, hp.p}] = hp
		case "d":
			keep := map[gfxKey]bool{}
			for k := range live {
				keep[k] = true
			}
			del(keep, p, id)
			for k := range live {
				if !keep[k] {
					delete(live, k)
				}
			}
			if p["d"] == "I" {
				delete(data, id)
			}
		}
	}
	var out []hostPlace
	for _, hp := range live {
		out = append(out, hp)
	}
	return out
}

// countPlaced is how many live placements show image id.
func countPlaced(stream []byte, id uint32) int {
	n := 0
	for _, p := range livePlaces(stream) {
		if p.img == id {
			n++
		}
	}
	return n
}

// waitPlacedOK waits a few seconds for a live placement that ok accepts.
func waitPlacedOK(term *tuitest.Terminal, host *lockedBuffer, ok func(hostPlace) bool) (hostPlace, bool) {
	var found hostPlace
	err := term.WaitFor(func(tuitest.Screen) bool {
		for _, p := range livePlaces([]byte(host.String())) {
			if ok(p) {
				found = p
				return true
			}
		}
		return false
	}, 3*uiTimeout/2)
	return found, err == nil
}

// waitPlaced is waitPlacedOK that fails the test when nothing is placed.
func waitPlaced(t *testing.T, term *tuitest.Terminal, host *lockedBuffer, ok func(hostPlace) bool) hostPlace {
	t.Helper()
	p, found := waitPlacedOK(term, host, ok)
	if !found {
		t.Fatalf("ASSERTION: no picture where the card is: placed %+v\n%s", livePlaces([]byte(host.String())), term.Snapshot())
	}
	return p
}

// threadTop presses K until the thread stops moving: its top is on screen.
func threadTop(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	_ = term.WaitStable(uiTimeout)
	for range 20 {
		before := term.Screen().Text()
		if err := term.SendKeys("K"); err != nil {
			t.Fatal(err)
		}
		_ = term.WaitStable(uiTimeout)
		if term.Screen().Text() == before {
			return
		}
	}
}

// TestCockpitThreadImagePlaceholdersScroll is the same thread on a host that
// draws kitty Unicode placeholders (appearance.kitty_placeholders = "on").
// The card's rows are placeholder cells that name the picture, so they
// scroll and clip with the text: a card the window cuts keeps the cells of
// the rows in the window, each naming its own picture row. The host gets the
// picture and one virtual placement (U=1) for it, and no placement at a
// position, which could be left behind by a scroll.
//
// The ways it can go wrong, each an assertion here: the cells name another
// image id than the one uploaded, or none; a cut card's first cell names
// picture row 0, which shows the picture's top where its bottom should be;
// rows are missing or out of order; the picture is placed at a position as
// well; the host lets go of the picture while its cells are on screen.
func TestCockpitThreadImagePlaceholdersScroll(t *testing.T) {
	const cfg = "[appearance]\nkitty_placeholders = \"on\"\n"
	// The whole card, then its top cut as J scrolls it up three rows a step.
	term, host, headRow, headCol, id, cardRows := openPlaceholderThread(t, 52, cfg)
	whole := placeholderRows(term, host, headCol)
	saveCockpitFrames(t, term, "ph-0-whole", hostPictures(t, []byte(host.String()))...)
	if len(whole) != cardRows || whole[0].row != headRow+1 || whole[0].pic != 0 || whole[0].id != id {
		t.Fatalf("ASSERTION: the whole card's cells are not its %d picture rows under its head naming image %d: %+v", cardRows, id, whole)
	}
	checkPlaceholderRun(t, whole, id, cardRows)
	if n := countPlaced([]byte(host.String()), id); n != 0 {
		t.Fatalf("ASSERTION: the placeholder picture is also placed at a position %d times", n)
	}
	cutSeen := false
	for step := 1; step <= 8 && !cutSeen; step++ {
		if err := term.SendKeys("J"); err != nil {
			t.Fatal(err)
		}
		_ = term.WaitStable(uiTimeout)
		rows := placeholderRows(term, host, headCol)
		if len(rows) == 0 {
			break
		}
		if rows[0].pic == 0 {
			continue
		}
		cutSeen = true
		saveCockpitFrames(t, term, fmt.Sprintf("ph-cut-%d", step), hostPictures(t, []byte(host.String()))...)
		if rows[len(rows)-1].pic != cardRows-1 {
			t.Fatalf("ASSERTION: step %d: the top-cut card does not end on its last picture row: %+v", step, rows)
		}
		checkPlaceholderRun(t, rows, id, cardRows)
		if !placeholderHeld(host.String(), id) {
			t.Fatalf("ASSERTION: step %d: the host let go of picture %d while its cells are on screen", step, id)
		}
	}
	if !cutSeen {
		t.Fatalf("the test never scrolled the card's top out\n%s", term.Snapshot())
	}
	alive(t, term, "after scrolling the placeholder card")

	// A shorter screen from the start cuts the card's bottom: the cells of
	// its first rows show.
	term, host, _, headCol, id, cardRows = openPlaceholderThread(t, 40, cfg)
	bottom := placeholderRows(term, host, headCol)
	saveCockpitFrames(t, term, "ph-bottom", hostPictures(t, []byte(host.String()))...)
	if len(bottom) == 0 || len(bottom) >= cardRows || bottom[0].pic != 0 {
		t.Fatalf("ASSERTION: the bottom-cut card does not show its first rows: %+v of %d", bottom, cardRows)
	}
	checkPlaceholderRun(t, bottom, id, cardRows)
}

// openPlaceholderThread starts a client rows high on the cards thread with
// cfg, opens the agents view at the thread's top, and waits for the image
// card's virtual placement. It returns the client, its host stream, the
// card's head line, and the picture's id and rows.
func openPlaceholderThread(t *testing.T, rows int, cfg string) (*tuitest.Terminal, *lockedBuffer, int, int, uint32, int) {
	t.Helper()
	term, host := startCardsThread(t, 160, rows, cfg)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	if err := term.WaitForText("Now fix the padding.", uiTimeout); err != nil {
		t.Fatalf("the thread never showed: %v\n%s", err, term.Snapshot())
	}
	threadTop(t, term)
	headRow, headCol := findText(t, term, "PNG image  320×180")
	var id uint32
	var cardRows int
	if err := term.WaitFor(func(tuitest.Screen) bool {
		m := virtualAPC.FindAllStringSubmatch(host.String(), -1)
		if len(m) == 0 {
			return false
		}
		last := m[len(m)-1]
		id, cardRows = atoiU32(last[1]), atoiInt(last[4])
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: no virtual placement (a=p,U=1) reached the host\n%s", term.Snapshot())
	}
	return term, host, headRow, headCol, id, cardRows
}

// virtualAPC is a virtual placement: the image id, its placement id, and its
// cell box.
var virtualAPC = regexp.MustCompile(`\x1b_Ga=p,U=1,i=(\d+),p=(\d+),c=(\d+),r=(\d+)`)

func atoiInt(s string) int { n, _ := strconv.Atoi(s); return n }

// phRow is a screen row of placeholder cells: the screen row, the picture
// row the cells name, and the image id.
type phRow struct {
	row, pic int
	id       uint32
}

// placeholderRows are the rows of placeholder cells at column col, top to
// bottom, on the screen the host stream draws. tuitest's emulator drops a
// placeholder's base character and runs its marks into the cell before, so
// the stream is replayed through tuios's own emulator with placeholders
// kept, at the screen's size. The client this reads is never resized, so the
// stream draws one screen of one size.
func placeholderRows(term *tuitest.Terminal, host *lockedBuffer, col int) []phRow {
	cols, rows := term.Screen().Size()
	em := vt.NewEmulator(cols, rows)
	em.SetKittyPlaceholderMode(vt.KittyPlaceholdersKeep)
	_, _ = em.Write([]byte(host.String()))
	var out []phRow
	for y := range rows {
		c := em.CellAt(col, y)
		if c == nil || !vt.IsKittyPlaceholder(c.Content) {
			continue
		}
		id, _ := vt.KittyPlaceholderImageID(c.Content, c.Style.Fg)
		marks := []rune(c.Content)[1:]
		pic := -1
		if len(marks) > 0 {
			pic = diacriticRow(marks[0])
		}
		out = append(out, phRow{row: y, pic: pic, id: id})
	}
	return out
}

// diacriticRow is the row a placeholder's first combining mark stands for.
func diacriticRow(r rune) int {
	for i := range 300 {
		if kitty.Diacritic(i) == r {
			return i
		}
	}
	return -1
}

// checkPlaceholderRun asserts rows are one run of screen rows naming id, each
// the picture row after the one above.
func checkPlaceholderRun(t *testing.T, rows []phRow, id uint32, cardRows int) {
	t.Helper()
	for i, r := range rows {
		if r.id != id {
			t.Fatalf("ASSERTION: row %d names image %d, want %d: %+v", r.row, r.id, id, rows)
		}
		if r.pic < 0 || r.pic >= cardRows {
			t.Fatalf("ASSERTION: row %d names picture row %d of %d", r.row, r.pic, cardRows)
		}
		if i > 0 && (r.row != rows[i-1].row+1 || r.pic != rows[i-1].pic+1) {
			t.Fatalf("ASSERTION: the placeholder rows are not one run in order: %+v", rows)
		}
	}
}

// placeholderHeld reports whether the host holds image id at the end of
// stream: its last upload comes after its last delete.
func placeholderHeld(stream string, id uint32) bool {
	up := strings.LastIndex(stream, fmt.Sprintf("\x1b_Ga=t,f=100,i=%d,", id))
	gone := strings.LastIndex(stream, fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,", id))
	return up >= 0 && up > gone
}
