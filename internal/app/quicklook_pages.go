package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The pages of a PDF in quick look.
//
// ql.res is the page on the screen, and pageWant the page the keys asked for. They
// differ while a read is out, and the panel says which page it is reading. Update
// asks quickLookPageRead after every message: it shows a held page at once, or
// starts one read. So a held key moves pageWant and the read goes to where it ended.
// A read for a page nobody wants now is cancelled, which kills pdftoppm on this
// machine. A link has no way to stop it, so there the next read waits.
//
// With nothing to do, it reads the next page and then the one before, so J shows
// a page at once. A link whose last read took 300 ms or more gets no such reads.
// Everything here goes when the file changes or the preview closes.
const (
	quickLookPagesHeld  = 5
	quickLookPagesBytes = 32 << 20
	quickLookPageSlow   = 300 * time.Millisecond
)

// quickLookPageMsg carries a finished page read back to the loop.
type quickLookPageMsg struct {
	target    quickLookTarget
	page      int
	res       *quickLookResult
	took      time.Duration
	cancelled bool
}

// quickLookBarBox is the page bar's track in the panel's body, for the mouse.
type quickLookBarBox struct {
	ok          bool
	x0, x1, row int
}

// quickLookPaged reports whether r is a PDF with pages to move through.
func quickLookPaged(r *quickLookResult) bool {
	return r != nil && r.kind == "pdf" && r.pages > 1
}

// quickLookPageTo moves to a page, held to the file's pages.
func (m *OS) quickLookPageTo(page int) bool {
	ql := &m.quickLook
	if !ql.open || !quickLookPaged(ql.res) {
		return false
	}
	ql.pageWant = max(min(page, ql.res.pages), 1)
	ql.status = ""
	return true
}

// quickLookDropPages lets go of what is held for the file's pages.
func (m *OS) quickLookDropPages() {
	ql := &m.quickLook
	if ql.pageCancel != nil {
		ql.pageCancel()
		ql.pageCancel = nil
	}
	ql.pageHeld = nil
	ql.pageWant = 0
	ql.pageSlow = false
	ql.pageBar = quickLookBarBox{}
}

// quickLookHeldPage is a page held in memory, or nil.
func (ql *quickLookState) quickLookHeldPage(page int) *quickLookResult {
	for _, r := range ql.pageHeld {
		if r.page == page {
			return r
		}
	}
	return nil
}

// quickLookPageSize is the bytes a held page keeps.
func quickLookPageSize(r *quickLookResult) int {
	if r.img != nil {
		return len(r.img.Data)
	}
	return 0
}

// quickLookPagesSize is the bytes all the held pages keep.
func (ql *quickLookState) quickLookPagesSize() int {
	n := 0
	for _, r := range ql.pageHeld {
		n += quickLookPageSize(r)
	}
	return n
}

// quickLookHoldPage keeps a page, and lets go of the pages furthest from the one
// asked for until the cache is within its bounds.
func (ql *quickLookState) quickLookHoldPage(res *quickLookResult) {
	if ql.quickLookHeldPage(res.page) == nil {
		ql.pageHeld = append(ql.pageHeld, res)
	}
	for len(ql.pageHeld) > 1 && (len(ql.pageHeld) > quickLookPagesHeld || ql.quickLookPagesSize() > quickLookPagesBytes) {
		far, dist := -1, -1
		for i, r := range ql.pageHeld {
			if d := max(r.page-ql.pageWant, ql.pageWant-r.page); d > dist && r.page != ql.pageWant {
				far, dist = i, d
			}
		}
		if far < 0 {
			return
		}
		ql.pageHeld = append(ql.pageHeld[:far], ql.pageHeld[far+1:]...)
	}
}

// quickLookPageRead shows the page asked for when it is held, or starts the next
// read. Update asks after every message. It is nil unless a read is due, and only
// one read runs at a time.
func (m *OS) quickLookPageRead() tea.Cmd {
	ql := &m.quickLook
	r := ql.res
	if !ql.open || !quickLookPaged(r) || r.target != ql.target {
		return nil
	}
	want := ql.pageWant
	if r.page != want {
		if held := ql.quickLookHeldPage(want); held != nil {
			ql.res, r = held, held
		}
	}
	if ql.fetching {
		if r.page != want && ql.pageOut != want && ql.pageCancel != nil {
			ql.pageCancel()
		}
		return nil
	}
	if r.page != want {
		return m.quickLookReadPage(want)
	}
	if ql.pageSlow || ql.quickLookPagesSize()+quickLookPageSize(r) > quickLookPagesBytes {
		return nil
	}
	for _, page := range []int{want + 1, want - 1} {
		if page >= 1 && page <= r.pages && ql.quickLookHeldPage(page) == nil {
			return m.quickLookReadPage(page)
		}
	}
	return nil
}

// quickLookReadPage reads one page off the loop.
func (m *OS) quickLookReadPage(page int) tea.Cmd {
	ql := &m.quickLook
	t, pages, size := ql.target, ql.res.pages, ql.res.size
	maxPx := m.quickLookPagePx()
	words := m.replyPreviewer().Mode() == composer.PreviewText
	ascii := overlay.UseASCII()
	ctx, cancel := context.WithTimeout(context.Background(), quickLookTimeout)
	var call func(host, verb string, params map[string]any) (json.RawMessage, error)
	if t.host != "" {
		call = m.verbOnHostCaller()
	}
	ql.fetching, ql.pageOut, ql.pageCancel = true, page, cancel
	return func() tea.Msg {
		defer cancel()
		start := time.Now()
		raw, err := quickLookPageReply(ctx, call, t, session.PreviewPDF{MaxPx: maxPx, Page: page, PagesKnown: true, Words: words})
		msg := quickLookPageMsg{target: t, page: page, took: time.Since(start), cancelled: errors.Is(ctx.Err(), context.Canceled)}
		if msg.cancelled {
			return msg
		}
		res := quickLookDecode(t, raw, err, ascii)
		// A page that failed stays a page: its note shows, and J and K go on.
		if res.kind != "pdf" {
			res.kind = "pdf"
		}
		res.page, res.pages = page, pages
		res.size = max(res.size, size)
		msg.res = res
		return msg
	}
}

// quickLookPageReply asks for a page, on this machine or the one with the file.
func quickLookPageReply(ctx context.Context, call func(host, verb string, params map[string]any) (json.RawMessage, error), t quickLookTarget, want session.PreviewPDF) (json.RawMessage, error) {
	if t.host == "" {
		out, err := session.PreviewLocalPDF(ctx, t.path, want)
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}
	params := map[string]any{"path": t.path, "max_px": want.MaxPx, "page": want.Page}
	// An older daemon ignores these: it counts the pages and sends the picture.
	if want.PagesKnown {
		params["pages_known"] = true
	}
	if want.Words {
		params["words"] = true
	}
	return call(t.host, "file-preview", params)
}

// applyQuickLookPage files a finished page away, or asks for the row the cursor
// moved to while it ran.
func (m *OS) applyQuickLookPage(msg quickLookPageMsg) tea.Cmd {
	ql := &m.quickLook
	ql.fetching, ql.pageOut, ql.pageCancel = false, 0, nil
	if !ql.open {
		return nil
	}
	if msg.target != ql.target {
		return m.quickLookShow(ql.target)
	}
	if msg.cancelled || msg.res == nil {
		return nil
	}
	ql.pageSlow = msg.target.host != "" && msg.took >= quickLookPageSlow
	ql.quickLookHoldPage(msg.res)
	if msg.page == ql.pageWant {
		ql.res = msg.res
	}
	return nil
}

// quickLookPagePx is the long side a page is asked for at: the pixels of the panes'
// area, so full size needs no second read, up to what a preview asks for at most.
func (m *OS) quickLookPagePx() int {
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	return min(max(m.GetRenderWidth()*cw, m.GetRenderHeight()*ch), quickLookMaxPx)
}

// quickLookPagePosition is "Page 3 of 12", and says when that page is on its way.
func quickLookPagePosition(r *quickLookResult, want int) string {
	s := "age " + strconv.Itoa(want) + " of " + strconv.Itoa(r.pages)
	if r.page != want {
		return "Reading p" + s
	}
	return "P" + s
}

// quickLookDrawPageBar draws the bar on the last row of the body: the page, a track
// with a thumb that a click moves, and the last page.
func quickLookDrawPageBar(c *ckCanvas, pal overlay.Palette, ql *quickLookState, r *quickLookResult, y, w int) {
	label := ckSeg{t: "Page " + strconv.Itoa(ql.pageWant) + "  ", fg: pal.Fg}
	end := ckSeg{t: "  " + strconv.Itoa(r.pages), fg: pal.FgMute}
	x0 := 1 + ckWidth(label)
	x1 := w - 1 - ckWidth(end)
	if x1-x0 < 2 {
		return
	}
	c.text(1, y, x0, label)
	c.text(x1, y, w, end)
	done, thumb, rest := "━", "●", "─"
	if overlay.UseASCII() {
		done, thumb, rest = "=", "#", "-"
	}
	at := (x1 - x0 - 1) * (ql.pageWant - 1) / (r.pages - 1)
	c.text(x0, y, x1, ckSeg{t: strings.Repeat(done, at), fg: pal.Accent},
		ckSeg{t: thumb, fg: pal.AccentBright}, ckSeg{t: strings.Repeat(rest, x1-x0-1-at), fg: pal.FgMute})
	ql.pageBar = quickLookBarBox{ok: true, x0: x0, x1: x1, row: y}
}

// quickLookBarClick moves to the page under a click on the page bar. x and y are on
// the screen. It reports whether the click was on the bar.
func (m *OS) quickLookBarClick(x, y int) bool {
	ql := &m.quickLook
	b := ql.pageBar
	x, y = x-ql.box.X0-1, y-ql.box.Y0-1
	if !b.ok || !quickLookPaged(ql.res) || y != b.row || x < b.x0 || x >= b.x1 {
		return false
	}
	span := max(b.x1-b.x0-1, 1)
	return m.quickLookPageTo(((x-b.x0)*(ql.res.pages-1)+span/2)/span + 1)
}

// quickLookDrawPageWords draws a page as its words, for a host that cannot draw the
// picture.
func quickLookDrawPageWords(c *ckCanvas, pal overlay.Palette, lines []string, top, rows, w int) {
	for i := 0; i < rows && i < len(lines); i++ {
		c.text(1, top+i, w-1, ckSeg{t: lines[i], fg: pal.Fg})
	}
}
