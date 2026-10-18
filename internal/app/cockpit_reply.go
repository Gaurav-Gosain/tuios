package app

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The reply area at the foot of the focus: who the reply is for and when it
// is typed, the draft, a chip for each image in it, and a Send button. Closed,
// it is a quiet line that opens the editor; open, it is the composer the
// Inbox reply always used. A click on an image chip opens the image view.

// drawReplyArea draws the reply area over rows up to y1 and returns its top
// row. name is who the reply is for.
func (m *OS) drawReplyArea(c *ckCanvas, l ckLook, e sidebarAgentEntry, name string, x, w, bodyTop, y1, top int) int {
	st := &m.Inbox
	st.ck.edW = w
	pal := l.pal
	right := x + w
	when := "sent now"
	if e.State == "working" {
		when = "typed when it rests"
	}
	hints := []ckSeg{{t: "@", fg: pal.AccentBright, attrs: ckBold}, {t: " mention   ", fg: pal.FgMute},
		{t: "ctrl+v", fg: pal.AccentBright, attrs: ckBold}, {t: " image   ", fg: pal.FgMute},
		{t: "ctrl+j", fg: pal.AccentBright, attrs: ckBold}, {t: " new line", fg: pal.FgMute}}
	send := ckSeg{t: " " + overlay.EnterGlyph + " Send ", fg: overlay.Readable(pal.Fg, pal.Card), bg: pal.Card, attrs: ckBold}

	ed := st.reply.editor
	if ed == nil || m.cockpitReplyMismatch() {
		if ed != nil {
			// The editor is not drawn, so nothing may be placed by
			// where it was last.
			ed.at.ok, ed.at.drawn = false, false
		}
		y := y1 - 3
		c.hline(x, y-1, w, hzGlyph(), l.edge)
		c.fill(x, y, w, 3, l.panel)
		c.text(x+2, y, right-16, ckSeg{t: "Reply to ", fg: pal.FgMute}, ckSeg{t: name, fg: pal.FgDim, attrs: ckBold})
		c.textR(right-2, y, ckSeg{t: when, fg: pal.FgDim})
		c.text(x+2, y+1, right-2, ckSeg{t: " ", bg: pal.FgMute}, ckSeg{t: " Write a reply.", fg: pal.FgMute})
		c.text(x+2, y+2, right-14, hints...)
		bx := c.textR(right-2, y+2, send)
		c.mark(pal, bx, y+2, right-2-bx, 1, ckOn)
		st.ck.hits = append(st.ck.hits, ckHit{x0: x, y0: top + y, x1: right, y1: top + y1, kind: ckHitAction, action: "reply"})
		return y - 1
	}

	inner := max(w-4, 8)
	body := ed.comp.Render(inner, composerStyles(pal), l.panel)
	images := ckDraftImages(ed.comp)
	h := 3 + len(body)
	if len(images) > 0 {
		h++
	}
	// The area never runs over the focus header and its tabs: on a short
	// screen the editor gets fewer rows.
	y := max(y1-h, bodyTop+1)
	c.hline(x, y-1, w, hzGlyph(), l.edge)
	c.fill(x, y, w, y1-y, l.panel)
	c.text(x+2, y, right-24, ckSeg{t: "Reply to ", fg: pal.FgMute}, ckSeg{t: ed.who, fg: pal.FgDim, attrs: ckBold})
	ctx := ed.context
	if strings.HasPrefix(ctx, "working") {
		// A reply to an agent at work waits, and the line says so before
		// it is sent.
		ctx = "typed when it rests" + sepWord() + ctx
	}
	if ctx != "" {
		c.textR(right-2, y, ckSeg{t: ctx, fg: pal.FgDim})
	}
	row := y + 1
	if len(images) > 0 {
		cx := x + 2
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
		c.text(cx, row, right-2, ckSeg{t: "click to view", fg: pal.FgMute})
		row++
	}
	fy := y1 - 1
	// Squeezed under the header, the editor keeps its last rows, where the
	// cursor most often is, and never draws over its hints.
	cut := max(len(body)-max(fy-row, 0), 0)
	for i, s := range body[cut:] {
		c.draw(x+2, row+i, inner, s)
	}
	// The pictures the draft shows are placed relative to the layer.
	ed.at.x, ed.at.y, ed.at.ok = x+2, row-cut, cut == 0
	ed.at.drawn, ed.at.top = true, cut
	c.text(x+2, fy, right-14, hints...)
	sx := c.textR(right-2, fy, send)
	c.mark(pal, sx, fy, right-2-sx, 1, ckOn)
	st.ck.hits = append(st.ck.hits, ckHit{x0: sx, y0: top + fy, x1: right - 2, y1: top + fy + 1, kind: ckHitAction, action: "send"})
	return y - 1
}

// ckDraftImages are the images a draft holds, in order.
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

// ckImageName is an image's name: the file it was read from, or a number.
func ckImageName(img *composer.Image, i int) string {
	if img.Name != "" {
		return printableTitle(img.Name)
	}
	return "Image " + strconv.Itoa(i+1) + "." + img.Format
}

// ckImageSize is an image's size in pixels, as "1280×720".
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
