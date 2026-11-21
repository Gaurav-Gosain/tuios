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
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const (
	quickLookPagesHeld  = 5
	quickLookPagesBytes = 32 << 20
	quickLookPageSlow   = 300 * time.Millisecond
)

type quickLookPageMsg struct {
	target    quickLookTarget
	page      int
	res       *quickLookResult
	took      time.Duration
	cancelled bool
}

type quickLookBarBox struct {
	ok          bool
	x0, x1, row int
}

func quickLookPaged(r *quickLookResult) bool {
	return r != nil && (r.kind == "pdf" || r.kind == "image") && r.pages > 1
}

func (m *OS) quickLookPageTo(page int) bool {
	ql := &m.quickLook
	if !ql.open || !quickLookPaged(ql.res) {
		return false
	}
	ql.pageWant = max(min(page, ql.res.pages), 1)
	ql.status = ""
	return true
}

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
	ql.barDrag = false
	ql.filmWant = 0
	ql.stripOn = false
	ql.strip = [quickLookStripFrames]*composer.Image{}
	ql.stripFailed = [quickLookStripFrames]bool{}
	ql.stripPics = nil
}

func (ql *quickLookState) quickLookHeldPage(page int) *quickLookResult {
	for _, r := range ql.pageHeld {
		if r.page == page {
			return r
		}
	}
	return nil
}

func quickLookPageSize(r *quickLookResult) int {
	if r.img != nil {
		return len(r.img.Data)
	}
	return 0
}

func (ql *quickLookState) quickLookPagesSize() int {
	n := 0
	for _, r := range ql.pageHeld {
		n += quickLookPageSize(r)
	}
	return n
}

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

func (m *OS) quickLookReadPage(page int) tea.Cmd {
	ql := &m.quickLook
	t, pages, size, kind := ql.target, ql.res.pages, ql.res.size, ql.res.kind
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
		raw, err := quickLookWantReply(ctx, call, t, session.PreviewWant{MaxPx: maxPx, Page: page, PagesKnown: true, Words: words})
		msg := quickLookPageMsg{target: t, page: page, took: time.Since(start), cancelled: errors.Is(ctx.Err(), context.Canceled)}
		if msg.cancelled {
			return msg
		}
		res := quickLookDecode(t, raw, err, ascii)
		res.kind = kind
		res.page, res.pages = page, pages
		res.size = max(res.size, size)
		msg.res = res
		return msg
	}
}

func quickLookWantReply(ctx context.Context, call func(host, verb string, params map[string]any) (json.RawMessage, error), t quickLookTarget, want session.PreviewWant) (json.RawMessage, error) {
	if t.host == "" {
		out, err := session.PreviewLocalWant(ctx, t.path, want)
		if err != nil {
			return nil, err
		}
		return jsonx.Marshal(out)
	}
	params := map[string]any{"path": t.path, "max_px": want.MaxPx}
	if want.Page > 0 {
		params["page"] = want.Page
	}
	if want.PagesKnown {
		params["pages_known"] = true
	}
	if want.Words {
		params["words"] = true
	}
	if want.Seek {
		params["at_ms"] = want.AtMs
	}
	if want.DurationKnown {
		params["duration_known"] = true
	}
	if want.NoFrame {
		params["no_frame"] = true
	}
	if want.Format {
		params["format"] = true
	}
	if want.Details {
		params["details"] = true
	}
	return call(t.host, "file-preview", params)
}

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

func (m *OS) quickLookPagePx() int {
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	return min(max(m.GetRenderWidth()*cw, m.GetRenderHeight()*ch), quickLookMaxPx)
}

func quickLookPagePosition(r *quickLookResult, want int) string {
	s := quickLookUnit(r) + " " + strconv.Itoa(want) + " of " + strconv.Itoa(r.pages)
	if r.page != want {
		return "Reading " + strings.ToLower(s)
	}
	return s
}

func quickLookUnit(r *quickLookResult) string {
	if r.kind == "image" {
		return "Frame"
	}
	return "Page"
}

func quickLookDrawBar(c *ckCanvas, pal overlay.Palette, ql *quickLookState, here, last string, pos, span int64, y, w int) {
	label := ckSeg{t: here + "  ", fg: pal.Fg}
	end := ckSeg{t: "  " + last, fg: pal.FgMute}
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
	at := int(int64(x1-x0-1) * max(min(pos, span), 0) / max(span, 1))
	c.text(x0, y, x1, ckSeg{t: strings.Repeat(done, at), fg: pal.Accent},
		ckSeg{t: thumb, fg: pal.AccentBright}, ckSeg{t: strings.Repeat(rest, x1-x0-1-at), fg: pal.FgMute})
	ql.pageBar = quickLookBarBox{ok: true, x0: x0, x1: x1, row: y}
}

func (m *OS) quickLookBarClick(x, y int) bool {
	ql := &m.quickLook
	b := ql.pageBar
	x, y = x-ql.box.X0-1, y-ql.box.Y0-1
	if !b.ok || !quickLookHasBar(ql.res) || y != b.row || x < b.x0 || x >= b.x1 {
		return false
	}
	ql.barDrag = true
	m.quickLookBarMove(x)
	return true
}

func (m *OS) quickLookBarMove(x int) {
	ql := &m.quickLook
	b := ql.pageBar
	span := int64(max(b.x1-b.x0-1, 1))
	at := int64(max(min(x, b.x1-1), b.x0) - b.x0)
	if quickLookPaged(ql.res) {
		m.quickLookPageTo(int((at*int64(ql.res.pages-1)+span/2)/span) + 1)
		return
	}
	m.quickLookFilmTo(at * ql.res.durationMs / span)
}

// QuickLookBarDrag follows the pointer while a press on the bar or the ruler is
// held. It reports whether one is. The next read goes to where the pointer is when the last read ends,
// so a drag runs one read at a time.
func (m *OS) QuickLookBarDrag(x, y int) bool {
	ql := &m.quickLook
	if m.quickLookRulerDrag(y) {
		return true
	}
	if !ql.open || !ql.barDrag || !ql.pageBar.ok {
		return false
	}
	m.quickLookBarMove(x - ql.box.X0 - 1)
	return true
}

// QuickLookBarRelease ends a drag. It reports whether one was going on.
func (m *OS) QuickLookBarRelease() bool {
	ql := &m.quickLook
	drag := ql.barDrag || ql.rulerDrag
	ql.barDrag, ql.rulerDrag = false, false
	return drag
}

func quickLookDrawPageWords(c *ckCanvas, pal overlay.Palette, lines []string, top, rows, w int) {
	for i := 0; i < rows && i < len(lines); i++ {
		c.text(1, top+i, w-1, ckSeg{t: lines[i], fg: pal.Fg})
	}
}
