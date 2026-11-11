package app

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

// The card preview: the card under the board's cursor shown whole without
// leaving the columns. Space (the peek key) on a card opens it and closes it;
// while it is open it follows the cursor, as quick look follows the files
// cursor. It is a panel beside the columns on a wide screen and a sheet over
// them otherwise, as [agents] card_preview says. J and K scroll it, [ and ]
// step its attachments, Z shows it over the whole body, and esc closes it. A
// click on a card shows that card, the wheel over the panel scrolls it, a
// click on an attachment selects it, a drag on the panel's edge sets its
// width, and a click outside the sheet closes the sheet.
//
// The preview draws the same body as the list's Card tab (ckCardBody), from
// the row the board holds and the card read whole with get-task, after a
// frame and never in one, as the Card tab reads it. Nothing runs on a timer:
// a key, a click or a task event changes it.

// The preview's widths: the narrowest panel, the default share of the view,
// and the default's bounds.
const (
	kbPreviewMin      = 36
	kbPreviewShare    = 40
	kbPreviewDefMin   = 44
	kbPreviewDefMax   = 96
	kbPreviewSideFrom = 140
)

// kbPreview is the preview's own state.
type kbPreview struct {
	open bool
	// zoom shows the preview over the whole body.
	zoom bool
	// scroll is the first body line drawn; lines and rows are the body's
	// lines and the rows it had in the last frame, for the clamp.
	scroll, lines, rows int
	// att is the selected attachment, by its place on the card, and attAt
	// the body line of each attachment's row in the last frame.
	att   int
	attAt []int
	// key is the card the scroll and the selection are for.
	key string
	// width is the panel's width a drag set, 0 for the config's.
	width int
	edge  ckDivider
	// box is the panel or the sheet on screen in the last frame, and sheet
	// says it was a sheet.
	box   overlay.Rect
	sheet bool
}

// kbPreviewMode is where the preview goes on a view w cells wide:
// config.CardPreviewSide, config.CardPreviewSheet or config.CardPreviewOff.
func (m *OS) kbPreviewMode(w int) string {
	mode := config.CardPreviewAuto
	if m.UserConfig != nil {
		mode = config.NormalizeCardPreview(m.UserConfig.Agents.CardPreview)
	}
	pw := m.kbPreviewWidth(w)
	fits := w-pw-1 >= kbMinW
	switch mode {
	case config.CardPreviewOff, config.CardPreviewSheet:
		return mode
	case config.CardPreviewSide:
		if fits {
			return mode
		}
		return config.CardPreviewSheet
	}
	if w >= kbPreviewSideFrom && w-pw-1 >= 3*kbMinW {
		return config.CardPreviewSide
	}
	return config.CardPreviewSheet
}

// kbPreviewWidth is the side panel's width on a view w cells wide: the
// dragged width, else the config's, else 40%, kept so the columns keep one
// column.
func (m *OS) kbPreviewWidth(w int) int {
	want := m.Inbox.ck.kb.pv.width
	if want <= 0 && m.UserConfig != nil {
		want = m.UserConfig.Agents.CardPreviewCells(w)
	}
	if want <= 0 {
		want = min(max(w*kbPreviewShare/100, kbPreviewDefMin), kbPreviewDefMax)
	}
	return min(max(want, kbPreviewMin), max(w-kbMinW-1, kbPreviewMin))
}

// kbPreviewShown reports whether the preview is drawn: open, on the columns,
// with a card under the cursor.
func (m *OS) kbPreviewShown() bool {
	if !m.Inbox.ck.kb.pv.open || !m.kanbanOn() || !m.kanbanDrawable() {
		return false
	}
	_, _, ok := m.cockpitCard()
	return ok
}

// kbPreviewSide reports whether the preview takes a panel beside the
// columns in this frame, and its width.
func (m *OS) kbPreviewSide(w int) (int, bool) {
	pv := &m.Inbox.ck.kb.pv
	if !m.kbPreviewShown() || pv.zoom || m.kbPreviewMode(w) != config.CardPreviewSide {
		return 0, false
	}
	return m.kbPreviewWidth(w), true
}

// kanbanPreviewToggle is the peek key on the columns: the preview of the
// card under the cursor opens, or closes. On a head it does nothing.
func (m *OS) kanbanPreviewToggle() tea.Cmd {
	pv := &m.Inbox.ck.kb.pv
	if pv.open {
		m.kanbanPreviewClose()
		return nil
	}
	if _, _, ok := m.cockpitCard(); !ok {
		return nil
	}
	if m.kbPreviewMode(m.GetRenderWidth()) == config.CardPreviewOff {
		m.ShowNotification("The card preview is off: [agents] card_preview = \"off\".", "info", m.Settings.NotificationDuration)
		return nil
	}
	pv.open, pv.zoom, pv.scroll, pv.att, pv.key = true, false, 0, 0, ""
	m.cockpitTouch()
	return m.cockpitCardFetch()
}

// kanbanPreviewClose closes the preview.
func (m *OS) kanbanPreviewClose() {
	pv := &m.Inbox.ck.kb.pv
	pv.open, pv.zoom, pv.edge.drag, pv.edge.hover = false, false, false, false
	pv.box = overlay.Rect{}
	m.cockpitTouch()
}

// kanbanPreviewKey runs the preview's keys while it is open. handled is
// false for the keys it leaves to the columns: the moves, which it follows.
func (m *OS) kanbanPreviewKey(action string) (tea.Cmd, bool) {
	pv := &m.Inbox.ck.kb.pv
	if !m.kbPreviewShown() {
		return nil, false
	}
	switch action {
	case config.ActionInboxEscape:
		if pv.zoom {
			pv.zoom = false
			m.cockpitTouch()
			return nil, true
		}
		m.kanbanPreviewClose()
	case config.ActionInboxZoom:
		pv.zoom = !pv.zoom
		m.cockpitTouch()
	case config.ActionInboxDetailDown, config.ActionInboxDetailUp:
		step := max(pv.rows/2, 1)
		if action == config.ActionInboxDetailUp {
			step = -step
		}
		m.kbPreviewScroll(step)
	case config.ActionInboxTabNext, config.ActionInboxTabPrev:
		m.kbPreviewStepAttachment(action == config.ActionInboxTabNext)
	default:
		return nil, false
	}
	return nil, true
}

// kbPreviewScroll moves the preview's body by d lines.
func (m *OS) kbPreviewScroll(d int) {
	pv := &m.Inbox.ck.kb.pv
	pv.scroll = min(max(pv.scroll+d, 0), max(pv.lines-pv.rows, 0))
	m.cockpitTouch()
}

// kbPreviewStepAttachment selects the next or the previous attachment, and
// scrolls it into view.
func (m *OS) kbPreviewStepAttachment(next bool) {
	pv := &m.Inbox.ck.kb.pv
	n := len(pv.attAt)
	if n == 0 {
		return
	}
	if next {
		pv.att = (pv.att + 1) % n
	} else {
		pv.att = (pv.att + n - 1) % n
	}
	m.kbPreviewReveal(pv.attAt[pv.att])
	m.cockpitTouch()
}

// kbPreviewReveal scrolls the body so line shows, with a line above it.
func (m *OS) kbPreviewReveal(line int) {
	pv := &m.Inbox.ck.kb.pv
	switch {
	case line-1 < pv.scroll:
		pv.scroll = max(line-1, 0)
	case line+2 > pv.scroll+pv.rows:
		pv.scroll = min(line+2-pv.rows, max(pv.lines-pv.rows, 0))
	}
}

// kbPreviewHas reports whether the screen cell is on the drawn preview.
func (m *OS) kbPreviewHas(x, y int) bool {
	b := m.Inbox.ck.kb.pv.box
	return b.X1 > b.X0 && b.Contains(x, y)
}

// kanbanPreviewWheel scrolls the preview when the pointer is over it.
func (m *OS) kanbanPreviewWheel(x, y int, up bool) bool {
	if !m.kbPreviewShown() || !m.kbPreviewHas(x, y) {
		return false
	}
	m.kbPreviewScroll(3 * wheelDelta(up))
	return true
}

// kanbanPreviewClick takes a click while the preview shows: on the edge it
// starts a drag, outside a sheet it closes the sheet. It reports whether the
// click is the preview's; a click on the panel's rows goes to the hits.
func (m *OS) kanbanPreviewClick(x, y int) bool {
	pv := &m.Inbox.ck.kb.pv
	if !m.kbPreviewShown() {
		return false
	}
	if pv.edge.has(x, y) {
		now := time.Now()
		if now.Sub(pv.edge.lastClick) <= ckDoubleClick {
			pv.edge.lastClick, pv.edge.drag, pv.width = time.Time{}, false, 0
			m.saveSidebarState()
		} else {
			pv.edge.lastClick, pv.edge.drag = now, true
		}
		m.cockpitTouch()
		return true
	}
	if pv.sheet && !m.kbPreviewHas(x, y) {
		m.kanbanPreviewClose()
		return true
	}
	return false
}

// kanbanPreviewMotion follows a drag of the panel's edge and lights the edge
// under the pointer. It reports whether the motion was the edge's.
func (m *OS) kanbanPreviewMotion(x, y int) bool {
	pv := &m.Inbox.ck.kb.pv
	if pv.edge.drag {
		w := m.GetRenderWidth()
		want := min(max(w-x-1, kbPreviewMin), max(w-kbMinW-1, kbPreviewMin))
		if want != pv.width {
			pv.width = want
			m.cockpitTouch()
		}
		return true
	}
	on := m.kbPreviewShown() && pv.edge.has(x, y)
	if on != pv.edge.hover {
		pv.edge.hover = on
		if on {
			m.SetPointerShape(PointerEWResize)
		} else {
			m.ResetPointerShape()
		}
		m.cockpitTouch()
	}
	return on
}

// kanbanPreviewRelease ends a drag of the edge and keeps the width.
func (m *OS) kanbanPreviewRelease() bool {
	pv := &m.Inbox.ck.kb.pv
	if !pv.edge.drag {
		return false
	}
	pv.edge.drag = false
	m.cockpitTouch()
	m.saveSidebarState()
	return true
}

// kanbanPreviewAction runs a click on the preview's own targets: "pv-close"
// and "pv-att:N".
func (m *OS) kanbanPreviewAction(action string) (tea.Cmd, bool) {
	pv := &m.Inbox.ck.kb.pv
	switch {
	case action == "pv-close":
		m.kanbanPreviewClose()
	case strings.HasPrefix(action, "pv-att:"):
		i, err := strconv.Atoi(strings.TrimPrefix(action, "pv-att:"))
		if err != nil || i < 0 || i >= len(pv.attAt) {
			return nil, true
		}
		pv.att = i
		m.cockpitTouch()
	default:
		return nil, false
	}
	return nil, true
}

// kanbanPreviewHints are the footer's keys while the preview shows.
func (m *OS) kanbanPreviewHints() []overlay.Hint {
	pv := &m.Inbox.ck.kb.pv
	hints := m.keyHints(config.ActionInboxPeek, "close")
	if pv.lines > pv.rows {
		hints = append(hints, m.keyHints(config.ActionInboxDetailDown, "scroll")...)
	}
	if len(pv.attAt) > 1 {
		hints = append(hints, m.keyHints(config.ActionInboxTabNext, "attachment")...)
	}
	word := "zoom"
	if pv.zoom {
		word = "unzoom"
	}
	return append(hints, m.keyHints(config.ActionInboxZoom, word)...)
}

// drawKanbanPreview draws the preview of the card under the cursor on the
// columns' rows y0 to y1: a panel from x, w cells wide, or with sheet a box
// over the columns. It records the box, the edge and the click targets.
func (m *OS) drawKanbanPreview(c *ckCanvas, l ckLook, x, w, y0, y1, top int, sheet bool, now time.Time) {
	pv := &m.Inbox.ck.kb.pv
	r, card, ok := m.cockpitCard()
	if !ok {
		return
	}
	pal := l.pal
	pv.edge.x0, pv.edge.x1 = 0, 0
	pv.sheet = sheet
	if sheet {
		// A box over the columns, a cell in from each side, with a rule
		// above and below so it reads as a sheet at 16 colours too.
		w = min(c.w-4, max(c.w*3/4, 60))
		x = (c.w - w) / 2
		// A cell of the canvas around it keeps the columns' cut text off its
		// edges, and a rule on each side closes it.
		c.fill(x-1, y0, w+2, y1-y0, l.canvas)
		c.fill(x, y0, w, y1-y0, l.panel)
		c.hline(x, y0, w, hzGlyph(), l.edgePanel)
		c.hline(x, y1-1, w, hzGlyph(), l.edgePanel)
		for y := y0; y < y1; y++ {
			c.text(x, y, x+1, ckSeg{t: vtGlyph(), fg: l.edgePanel})
			c.text(x+w-1, y, x+w, ckSeg{t: vtGlyph(), fg: l.edgePanel})
		}
		y0, y1 = y0+1, y1-1
	} else {
		// The edge: a rule one cell left of the panel, lit while the
		// pointer is on it or drags it.
		ink := l.edge
		if pv.edge.hover || pv.edge.drag {
			ink = pal.Accent
		}
		for y := y0; y < y1; y++ {
			c.text(x-1, y, x, ckSeg{t: vtGlyph(), fg: ink})
		}
		pv.edge.x0, pv.edge.x1, pv.edge.y0, pv.edge.y1 = x-1, x, top+y0, top+y1
		c.fill(x, y0, w, y1-y0, l.panel)
	}
	pv.box = overlay.Rect{X0: x, Y0: top + y0, X1: x + w, Y1: top + y1}
	if pv.key != card.Session+"/"+card.ID {
		pv.key, pv.scroll, pv.att = card.Session+"/"+card.ID, 0, 0
	}
	m.cockpitNoteCard(card)
	inner, ix := w-4, x+2
	// The head: what this is, and a close target.
	c.text(ix, y0, ix+inner-2, ckSeg{t: "Card " + card.ID, fg: pal.FgDim, attrs: ckBold})
	cx := c.textR(ix+inner, y0, ckSeg{t: "x", fg: pal.FgMute})
	m.Inbox.ck.hits = append(m.Inbox.ck.hits, ckHit{x0: cx, y0: top + y0, x1: ix + inner, y1: top + y0 + 1, kind: ckHitAction, action: "pv-close"})
	body := m.ckCardBody(l, r, card, inner, now, true)
	pv.attAt = pv.attAt[:0]
	for i, ln := range body {
		if strings.HasPrefix(ln.hit, "pv-att:") {
			pv.attAt = append(pv.attAt, i)
		}
	}
	if pv.att >= len(pv.attAt) {
		pv.att = max(len(pv.attAt)-1, 0)
	}
	by0 := y0 + 2
	pv.rows, pv.lines = max(y1-by0, 0), len(body)
	pv.scroll = min(pv.scroll, max(pv.lines-pv.rows, 0))
	for i := pv.scroll; i < len(body) && by0+i-pv.scroll < y1; i++ {
		ln, y := body[i], by0+i-pv.scroll
		if strings.HasPrefix(ln.hit, "pv-att:") {
			n, _ := strconv.Atoi(strings.TrimPrefix(ln.hit, "pv-att:"))
			if n == pv.att {
				// The selected attachment: the cursor's ground, and a bar so
				// it shows at 16 colours too.
				c.fill(ix-1, y, inner+2, 1, l.cursor)
				ln.bar, ln.barInk = "▌", pal.Accent
				if l.ascii {
					ln.bar = ">"
				}
				ln.indent = max(ln.indent, 1)
			}
			m.Inbox.ck.hits = append(m.Inbox.ck.hits, ckHit{x0: ix - 1, y0: top + y, x1: ix + inner + 1, y1: top + y + 1, kind: ckHitAction, action: ln.hit})
		}
		drawCkLineAt(c, ix, y, inner, ln)
	}
	if more := pv.lines - pv.scroll - pv.rows; more > 0 {
		down := "↓"
		if l.ascii {
			down = "v"
		}
		c.textR(ix+inner, y1-1, ckSeg{t: down + " " + strconv.Itoa(more) + " more", fg: pal.AccentBright})
	}
}

// ckCardBody is a card's body as lines width cells wide: who holds it, its
// lifecycle, the block, the prompt as Markdown, the attachments, the
// history and the end of the verify output. With head it starts with the
// title and the stage line, which the Card tab draws above its tabs.
func (m *OS) ckCardBody(l ckLook, r ckRow, card session.TaskRow, width int, now time.Time, head bool) []ckLine {
	pal := l.pal
	var out []ckLine
	line := func(segs ...ckSeg) { out = append(out, ckLine{segs: segs}) }
	gap := func() {
		if len(out) > 0 && !out[len(out)-1].blank() {
			out = append(out, ckLine{})
		}
	}
	if head {
		for _, t := range kbTitleLines(printableTitle(card.Title), width) {
			line(ckSeg{t: t, fg: pal.Fg, attrs: ckBold})
		}
		line(ckSeg{t: ckStageTitle(r.group), fg: pal.FgDim},
			ckSeg{t: sepWord() + units.Waited(card.StageAt, now) + sepWord() + printableTitle(card.Session) + " " + card.ID, fg: pal.FgMute})
		gap()
	}
	switch {
	case card.Holder != nil:
		who := printableTitle(card.Holder.Name)
		if who == "" {
			who = shortWindowLabel(card.Holder.Window)
		}
		line(ckSeg{t: "Held by ", fg: pal.FgDim}, ckSeg{t: who, fg: pal.Fg, attrs: ckBold},
			ckSeg{t: "  " + units.Waited(card.Holder.HeldAt, now), fg: pal.FgMute})
	case card.Stage == session.TaskStageVerify || card.Stage == session.TaskStageReview:
		// Let go by its agent: the lifecycle lines below say where it is.
	case card.Stage != session.TaskStageDone && card.Blocked == nil:
		line(ckSeg{t: "Free. ", fg: pal.FgDim}, ckSeg{t: m.ckFreeWords(card), fg: pal.FgMute})
	}
	for _, segs := range m.ckLifeFocus(pal, card) {
		line(segs...)
	}
	if b := card.Blocked; b != nil {
		line(ckSeg{t: "Blocked ", fg: pal.Warn, attrs: ckBold}, ckSeg{t: printableTitle(b.Reason), fg: pal.FgDim})
	}
	d := m.Inbox.ck.card
	switch {
	case d == nil || d.key != ckTaskKey(card) || d.loading:
		gap()
		line(ckSeg{t: "Reading...", fg: pal.FgMute})
		return out
	case d.err != "":
		gap()
		line(ckSeg{t: d.err, fg: pal.Warning})
		return out
	}
	if d.prompt != "" {
		gap()
		line(ckSeg{t: "Prompt", fg: pal.FgDim, attrs: ckBold})
		out = append(out, m.ckMarkdown(l, d.prompt, max(width, 8))...)
	}
	if len(d.attachments) > 0 {
		gap()
		line(ckSeg{t: "Attachments", fg: pal.FgDim, attrs: ckBold}, ckSeg{t: "  " + strconv.Itoa(len(d.attachments)), fg: pal.FgMute})
		for i, a := range d.attachments {
			name := ckTrunc(printableTitle(a.Name), max(width-24, 8))
			out = append(out, ckLine{
				hit:   "pv-att:" + strconv.Itoa(i),
				segs:  []ckSeg{{t: name, fg: pal.Fg, attrs: ckBold}},
				right: []ckSeg{{t: ckAttachWhat(a), fg: pal.FgMute}},
			})
			meta := m.ckHistoryBy(a.By) + ", " + units.Waited(a.At, now)
			if a.Note != "" {
				meta += sepWord() + printableTitle(a.Note)
			}
			for _, s := range strings.Split(ansi.Wordwrap(meta, max(width-2, 8), ""), "\n") {
				out = append(out, ckLine{indent: 2, segs: []ckSeg{{t: s, fg: pal.FgDim}}})
			}
		}
	}
	if len(d.history) > 0 {
		gap()
		line(ckSeg{t: "History", fg: pal.FgDim, attrs: ckBold})
		for _, ev := range slices.Backward(d.history) {
			words := ev.Action
			switch {
			case ev.Stage != "" && ev.Action == session.TaskActionStaged:
				words += " to " + ev.Stage
			case ev.Reason != "":
				words += ", " + printableTitle(ev.Reason)
			}
			line(ckSeg{t: ckPad(units.Waited(ev.At, now), 5), fg: pal.FgMute}, ckSeg{t: words, fg: pal.Fg},
				ckSeg{t: "  " + m.ckHistoryBy(ev.By), fg: pal.FgMute})
		}
	}
	if v := card.Verify; v != nil && v.Tail != "" && v.State == session.VerifyFailed {
		gap()
		line(ckSeg{t: "Verify output", fg: pal.FgDim, attrs: ckBold}, ckSeg{t: "  the end", fg: pal.FgMute})
		for s := range strings.SplitSeq(strings.TrimRight(v.Tail, "\n"), "\n") {
			line(ckSeg{t: ckTrunc(printableTitle(s), width), fg: pal.FgDim})
		}
	}
	return out
}

// ckAttachWhat is an attachment's kind and size as the preview says it.
func ckAttachWhat(a session.TaskAttachment) string {
	if a.URL != "" {
		if a.Kind == session.AttachPR {
			return "pull request"
		}
		return "link"
	}
	return a.Kind + sepWord() + ckBytes(int(a.Size))
}
