package tuie2e

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/vt"
)

// TestCockpitImageViewZooms opens the agents view's image view on a thread's
// image card, on a kitty host, and zooms and moves the picture as quick look
// does: the wheel zooms around the pointer, a drag and h, j, k and l move the
// zoomed picture, + zooms in past the fit, and 0 fits it again. A zoom or a
// move step sends no picture, only a placement.
//
// The two paths each have a subtest. With kitty placements the part in view
// is a source rect of one placement. With Unicode placeholders it is the
// cells of one virtual placement as large as the whole zoomed picture, and a
// zoom step places that virtual placement again under the same id.
//
// Ways it can go wrong, each an assertion here: the wheel does nothing (main,
// where the view only shrank from the fit); a step sends the picture again;
// the zoom moves the point under the pointer; a drag or a key does not move
// the view; 0 leaves a part of the picture; the placeholder cells name the
// picture's first row and column after a zoom into its middle.
//
// Negative control, in NEGATIVE_CONTROLS.md: on main the test fails at "the
// wheel over the picture did not zoom it".
func TestCockpitImageViewZooms(t *testing.T) {
	t.Run("placements", func(t *testing.T) {
		term, host := openThreadImageView(t, "")
		fit := waitPlaced(t, term, host, func(p hostPlace) bool { return p.cols > 40 })
		if fit.srcW != 0 && fit.srcW != fit.imgW {
			t.Fatalf("ASSERTION: the picture at the fit is placed cropped: %+v", fit)
		}
		saveCockpitFrames(t, term, "image-view-fit", hostPictures(t, []byte(host.String()))...)
		cx, cy := fit.col+fit.cols/2, fit.row+fit.rows/2
		id := atoiString(fit.img)

		mark := len(host.String())
		wheelAt(t, term, cx, cy, tuitest.MouseWheelUp, 3)
		if err := term.WaitFor(func(s tuitest.Screen) bool { return quickLookZoomLabel.MatchString(s.Text()) }, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the wheel over the picture did not zoom it: %v\n%s", err, term.Snapshot())
		}
		var crop quickLookCrop
		if err := term.WaitFor(func(tuitest.Screen) bool {
			crop = lastQuickLookCrop(host.String()[mark:])
			return crop.ok && crop.id == id && crop.w < fit.imgW
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the zoom placed no part of the picture: last %+v: %v", crop, err)
		}
		if mx, my := crop.x+crop.w/2, crop.y+crop.h/2; abs(mx-fit.imgW/2) > fit.imgW/20 || abs(my-fit.imgH/2) > fit.imgH/20 {
			t.Errorf("ASSERTION: the zoom moved the point under the pointer: the rect %+v has its middle at %d,%d, want about %d,%d",
				crop, mx, my, fit.imgW/2, fit.imgH/2)
		}
		saveCockpitFrames(t, term, "image-view-zoom", hostPictures(t, []byte(host.String()))...)

		// A drag to the left and up shows more of the right and the bottom.
		mouseDrag(t, term, cx, cy, cx-8, cy-4, tuitest.MouseLeft, 0)
		var moved quickLookCrop
		if err := term.WaitFor(func(tuitest.Screen) bool {
			moved = lastQuickLookCrop(host.String()[mark:])
			return moved.ok && moved.id == id && moved.x > crop.x && moved.y > crop.y
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the drag did not move the zoomed picture: before %+v, after %+v: %v", crop, moved, err)
		}
		saveCockpitFrames(t, term, "image-view-drag", hostPictures(t, []byte(host.String()))...)
		// h moves it back to the left, and k up.
		for _, k := range []struct {
			key   string
			moved func(c quickLookCrop) bool
		}{
			{"h", func(c quickLookCrop) bool { return c.x < moved.x && c.y == moved.y }},
			{"k", func(c quickLookCrop) bool { return c.y < moved.y }},
		} {
			keyMark := len(host.String())
			sendKeys(t, term, k.key)
			if err := term.WaitFor(func(tuitest.Screen) bool {
				c := lastQuickLookCrop(host.String()[keyMark:])
				return c.ok && k.moved(c)
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: %s did not move the zoomed picture from %+v: %v", k.key, moved, err)
			}
		}
		if s := host.String()[mark:]; strings.Contains(s, "a=t,") {
			t.Error("ASSERTION: a zoom or a move step sent the picture again: a step only places it")
		}

		// 0 fits the picture, and + zooms in past the fit again.
		mark = len(host.String())
		sendKeys(t, term, "0")
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			if quickLookZoomLabel.MatchString(s.Text()) {
				return false
			}
			for _, p := range livePlaces([]byte(host.String())) {
				if p.img == fit.img && p.cols == fit.cols && p.rows == fit.rows && (p.srcW == 0 || p.srcW == p.imgW) {
					return true
				}
			}
			return false
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: 0 did not fit the picture again: %v\n%+v", err, livePlaces([]byte(host.String())))
		}
		sendKeys(t, term, "+")
		if err := term.WaitFor(func(s tuitest.Screen) bool { return quickLookZoomLabel.MatchString(s.Text()) }, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: + did not zoom in past the fit: %v\n%s", err, term.Snapshot())
		}
		alive(t, term, "after the zoom of the image view")
	})

	t.Run("placeholders", func(t *testing.T) {
		term, host := openThreadImageView(t, "[appearance]\nkitty_placeholders = \"on\"\n")
		// The view's virtual placement is the box at the fit, wider than the
		// thread's card.
		var id string
		var c0, r0 int
		if err := term.WaitFor(func(tuitest.Screen) bool {
			for _, m := range virtualAPC.FindAllStringSubmatch(host.String(), -1) {
				if c := atoiInt(m[3]); c > 40 {
					id, c0, r0 = m[1], c, atoiInt(m[4])
				}
			}
			return id != ""
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the image view made no virtual placement\n%s", term.Snapshot())
		}
		first, ok := viewPlaceholders(term.Screen(), host, id)
		if !ok || first.row != 0 || first.col != 0 || first.cols != c0 {
			t.Fatalf("ASSERTION: at the fit the cells do not show the whole picture from its first cell: %+v, the box is %dx%d", first, c0, r0)
		}
		cx, cy := first.x+first.cols/2, first.y+r0/2

		mark := len(host.String())
		wheelAt(t, term, cx, cy, tuitest.MouseWheelUp, 3)
		if err := term.WaitFor(func(s tuitest.Screen) bool { return quickLookZoomLabel.MatchString(s.Text()) }, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the wheel over the picture did not zoom it: %v\n%s", err, term.Snapshot())
		}
		var c1, r1 int
		if err := term.WaitFor(func(tuitest.Screen) bool {
			for _, m := range virtualAPC.FindAllStringSubmatch(host.String()[mark:], -1) {
				if m[1] == id {
					c1, r1 = atoiInt(m[3]), atoiInt(m[4])
				}
			}
			return c1 > c0 && r1 > r0
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the zoom did not place the virtual placement again larger under id %s: %dx%d, the fit is %dx%d", id, c1, r1, c0, r0)
		}
		var zoomed phView
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			zoomed, ok = viewPlaceholders(s, host, id)
			return ok && zoomed.row > 0 && zoomed.col > 0
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: after a zoom into the middle the cells still start at the picture's corner: %+v", zoomed)
		}
		// A drag to the right and down shows more of the left and the top.
		mouseDrag(t, term, cx, cy, cx+6, cy+3, tuitest.MouseLeft, 0)
		var dragged phView
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			dragged, ok = viewPlaceholders(s, host, id)
			return ok && dragged.col < zoomed.col && dragged.row < zoomed.row
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the drag did not move the cells: before %+v, after %+v", zoomed, dragged)
		}
		if s := host.String()[mark:]; strings.Contains(s, "a=t,") {
			t.Error("ASSERTION: a zoom or a move step sent the picture again: a step only places it")
		}
		mark = len(host.String())
		sendKeys(t, term, "0")
		if err := term.WaitFor(func(tuitest.Screen) bool {
			m := virtualAPC.FindAllStringSubmatch(host.String()[mark:], -1)
			return len(m) > 0 && m[len(m)-1][1] == id && atoiInt(m[len(m)-1][3]) == c0 && atoiInt(m[len(m)-1][4]) == r0
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: 0 did not place the picture at the fit box %dx%d again", c0, r0)
		}
		alive(t, term, "after the zoom of the image view")
	})

	t.Run("placeholders on a wide screen", func(t *testing.T) {
		// 400 by 120 cells fits the 16:9 picture 380 cells wide, more than
		// placeholder cells can name: the fit box stops at 297.
		term, host := openThreadImageViewAt(t, 400, 120, "[appearance]\nkitty_placeholders = \"on\"\n")
		var c, r int
		if err := term.WaitFor(func(tuitest.Screen) bool {
			for _, m := range virtualAPC.FindAllStringSubmatch(host.String(), -1) {
				if n := atoiInt(m[3]); n > 40 {
					c, r = n, atoiInt(m[4])
				}
			}
			return c > 0
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the image view made no virtual placement\n%s", term.Snapshot())
		}
		if c > 297 || r > 297 {
			t.Fatalf("ASSERTION: the fit box is %dx%d cells, more than the 297 placeholder cells can name", c, r)
		}
	})
}

// openThreadImageView starts a client on the cards thread with cfg, opens
// the agents view and a click on the image card opens the image view.
func openThreadImageView(t *testing.T, cfg string) (*tuitest.Terminal, *lockedBuffer) {
	t.Helper()
	return openThreadImageViewAt(t, 160, 52, cfg)
}

// openThreadImageViewAt is openThreadImageView on a client cols by rows.
func openThreadImageViewAt(t *testing.T, cols, rows int, cfg string) (*tuitest.Terminal, *lockedBuffer) {
	t.Helper()
	term, host := startCardsThread(t, cols, rows, cfg)
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
	row, col := findText(t, term, "PNG image  320×180")
	mouseClick(t, term, col+4, row+3, tuitest.MouseLeft, 0)
	waitText(t, term, "the image view", "Click outside the picture to close it.")
	return term, host
}

// phView is where the image view's placeholder cells are: the screen cell of
// the first, the picture's row and column it names, and the cells in its row.
type phView struct {
	x, y, row, col, cols int
}

// viewPlaceholders finds the first row of placeholder cells that name image
// id on the screen the host stream draws, replayed with placeholders kept
// (see placeholderRows).
func viewPlaceholders(s tuitest.Screen, host *lockedBuffer, id string) (phView, bool) {
	cols, rows := s.Size()
	em := vt.NewEmulator(cols, rows)
	em.SetKittyPlaceholderMode(vt.KittyPlaceholdersKeep)
	_, _ = em.Write([]byte(host.String()))
	want := uint32(atoiInt(id))
	for y := range rows {
		for x := range cols {
			c := em.CellAt(x, y)
			if c == nil || !vt.IsKittyPlaceholder(c.Content) {
				continue
			}
			if got, _ := vt.KittyPlaceholderImageID(c.Content, c.Style.Fg); got != want {
				continue
			}
			marks := []rune(c.Content)[1:]
			if len(marks) < 2 {
				return phView{}, false
			}
			v := phView{x: x, y: y, row: diacriticRow(marks[0]), col: diacriticRow(marks[1])}
			for n := x; n < cols; n++ {
				if cc := em.CellAt(n, y); cc == nil || !vt.IsKittyPlaceholder(cc.Content) {
					break
				}
				v.cols++
			}
			return v, true
		}
	}
	return phView{}, false
}

func atoiString(n uint32) string { return strconv.Itoa(int(n)) }
