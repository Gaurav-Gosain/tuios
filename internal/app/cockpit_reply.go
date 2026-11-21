package app

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

func (m *OS) drawReplyArea(c *ckCanvas, l ckLook, e sidebarAgentEntry, name string, x, w, bodyTop, y1, top int, fold bool) int {
	st := &m.Inbox
	st.ck.edW = w
	pal := l.pal
	right := x + w
	when := "typed at once"
	if e.State == "working" {
		when = "typed when it rests"
	}
	label, key := pal.FgDim, overlay.ReadableAt(pal.AccentBright, l.panel, overlay.MarkFloor)
	hints := []ckSeg{{t: "@", fg: key, attrs: ckBold}, {t: " mention   ", fg: label},
		{t: "ctrl+v", fg: key, attrs: ckBold}, {t: " image   ", fg: label},
		{t: "ctrl+j", fg: key, attrs: ckBold}, {t: " new line", fg: label}}
	verb := "Send"
	if replyEnterQueues(e.State) {
		verb = "Queue"
		if ed := st.reply.editor; e.Host == "" && (ed == nil || (ed.host == "" && len(ed.targets) == 0)) {
			hints = append([]ckSeg{{t: m.sendNowKeyName(), fg: key, attrs: ckBold}, {t: " send now   ", fg: label}}, hints...)
		}
	}
	send := ckSeg{t: " " + overlay.EnterGlyph + " " + verb + " ", fg: overlay.Readable(pal.Fg, pal.Card), bg: pal.Card, attrs: ckBold}
	tx := x + 4
	hints = ckFitHints(hints, right-14-tx)

	ed := st.reply.editor
	if ed == nil || m.cockpitReplyMismatch() {
		if ed != nil {
			ed.at.ok, ed.at.drawn = false, false
		}
		if fold {
			y := y1 - 1
			c.hline(x, y-1, w, hzGlyph(), l.edge)
			c.fill(x, y, w, 1, l.panel)
			rx := c.textR(right-2, y, ckSeg{t: when, fg: label})
			segs := []ckSeg{{t: "Reply to ", fg: label}, {t: name, fg: pal.Fg, attrs: ckBold}, {t: "   "},
				{t: m.inboxKeyOr(config.ActionInboxReply, "r"), fg: key, attrs: ckBold}, {t: " write", fg: label}}
			if d := st.reply.drafts[inboxDraftKey(e.SessionID, e.WindowID)]; d != nil && !d.Empty() {
				segs[4].t = " draft: "
				segs = append(segs, ckSeg{t: ansi.Truncate(ckDraftLine(d), max(rx-tx-ckWidth(segs...)-2, 1), "…"), fg: pal.Fg})
			}
			c.text(tx, y, rx-2, segs...)
			st.ck.hits = append(st.ck.hits, ckHit{x0: x, y0: top + y, x1: right, y1: top + y1, kind: ckHitAction, action: "reply"})
			return y - 1
		}
		h := 3
		if c.h >= 30 {
			h = 4
		}
		y := y1 - h
		c.hline(x, y-1, w, hzGlyph(), l.edge)
		c.fill(x, y, w, h, l.panel)
		c.text(tx, y, right-16, ckSeg{t: "Reply to ", fg: label}, ckSeg{t: name, fg: pal.Fg, attrs: ckBold})
		c.textR(right-2, y, ckSeg{t: when, fg: label})
		prompt := []ckSeg{{t: " ", bg: pal.FgMute}, {t: " Write a reply.", fg: label}}
		if d := st.reply.drafts[inboxDraftKey(e.SessionID, e.WindowID)]; d != nil && !d.Empty() {
			prompt = []ckSeg{{t: "Draft: ", fg: label}, {t: ansi.Truncate(ckDraftLine(d), max(w-11, 1), "…"), fg: pal.Fg}}
		}
		c.text(x+2, y+1, right-2, prompt...)
		c.text(tx, y1-1, right-14, hints...)
		bx := c.textR(right-2, y1-1, send)
		c.mark(pal, bx, y1-1, right-2-bx, 1, ckOn)
		st.ck.hits = append(st.ck.hits, ckHit{x0: x, y0: top + y, x1: right, y1: top + y1, kind: ckHitAction, action: "reply"})
		return y - 1
	}

	inner := max(w-4, 8)
	images := ckDraftImages(ed.comp)
	img := 0
	if len(images) > 0 {
		img = 1
	}
	ed.comp.SetMaxRows(m.composerRows(inboxReplyRows))
	ed.comp.SetRoom(y1 - bodyTop - 3 - img)
	body := ed.comp.Render(inner, replyStyles(l), l.panel)
	h := 3 + len(body)
	if len(images) > 0 {
		h++
	}
	y := max(y1-h, bodyTop+1)
	c.hline(x, y-1, w, hzGlyph(), l.edge)
	c.fill(x, y, w, y1-y, l.panel)
	bar := "▌"
	if overlay.UseASCII() {
		bar = "|"
	}
	for i := y; i < y1; i++ {
		c.text(x, i, 0, ckSeg{t: bar, fg: pal.Accent})
	}
	c.text(tx, y, right-24, ckSeg{t: "Reply to ", fg: label}, ckSeg{t: ed.who, fg: pal.Fg, attrs: ckBold})
	ctx := ed.context
	if strings.HasPrefix(ctx, "working") {
		ctx = "typed when it rests" + sepWord() + ctx
	} else if replyEnterQueues(e.State) {
		ctx = "typed when it rests"
	}
	if ctx != "" {
		c.textR(right-2, y, ckSeg{t: ctx, fg: label})
	}
	row := y + 1
	if len(images) > 0 {
		cx := tx
		for i, img := range images {
			label := l.g(ckGlyphImage) + " " + ckImageName(img, i) + "  " + ckImageSize(img)
			if cx+ansi.StringWidth(label)+4 > right-14 {
				break
			}
			x0 := cx
			cx = c.text(cx, row, 0, ckSeg{t: " " + label + " ", fg: overlay.Readable(pal.Fg, pal.Card), bg: pal.Card})
			c.mark(pal, x0, row, cx-x0, 1, ckOn)
			st.ck.hits = append(st.ck.hits, ckHit{x0: x0, y0: top + row, x1: cx, y1: top + row + 1, kind: ckHitAction, action: "image:" + strconv.Itoa(i)})
			cx += 2
		}
		c.text(cx, row, right-2, ckSeg{t: "click to view", fg: label})
		row++
	}
	fy := y1 - 1
	cut := max(len(body)-max(fy-row, 0), 0)
	for i, s := range body[cut:] {
		c.draw(x+2, row+i, inner, s)
		if j, ok := ed.comp.MentionAt(cut + i); ok {
			st.ck.hits = append(st.ck.hits, ckHit{x0: x + 2, y0: top + row + i, x1: x + 2 + inner, y1: top + row + i + 1, kind: ckHitAction, action: "mention:" + strconv.Itoa(j)})
		}
	}
	ed.at.x, ed.at.y, ed.at.ok = x+2, row-cut, cut == 0
	ed.at.drawn, ed.at.top = true, cut
	c.text(tx, fy, right-14, hints...)
	sx := c.textR(right-2, fy, send)
	c.mark(pal, sx, fy, right-2-sx, 1, ckOn)
	st.ck.hits = append(st.ck.hits, ckHit{x0: sx, y0: top + fy, x1: right - 2, y1: top + fy + 1, kind: ckHitAction, action: "send"})
	return y - 1
}

func ckDraftLine(d *composer.Model) string {
	return printableTitle(strings.Join(strings.Fields(composer.JoinParts(d.Parts())), " "))
}

func replyStyles(l ckLook) composer.Styles {
	st := composerStyles(l.pal)
	st.Selected = l.pal.Card
	st.Field = l.panel
	return st
}

func ckDraftImages(comp *composer.Model) []*composer.Image {
	if comp == nil {
		return nil
	}
	var out []*composer.Image
	for _, p := range comp.Parts() {
		if p.Kind == composer.PartImage && p.Image != nil {
			out = append(out, p.Image)
		}
	}
	return out
}

func ckImageName(img *composer.Image, i int) string {
	if img.Name != "" {
		return printableTitle(img.Name)
	}
	return "Image " + strconv.Itoa(i+1) + "." + img.Format
}

func ckImageSize(img *composer.Image) string {
	if img.Width <= 0 || img.Height <= 0 {
		return strings.ToUpper(img.Format)
	}
	x := "×"
	if overlay.UseASCII() {
		x = "x"
	}
	return strconv.Itoa(img.Width) + x + strconv.Itoa(img.Height)
}

func ckFitHints(hints []ckSeg, w int) []ckSeg {
	used := 0
	for i := 0; i+1 < len(hints); i += 2 {
		pair := ckWidth(hints[i], hints[i+1])
		if used+pair-(len(hints[i+1].t)-len(strings.TrimRight(hints[i+1].t, " "))) > w {
			return hints[:i]
		}
		used += pair
	}
	return hints
}
