package app

import (
	"strconv"
	"strings"

	"tuios.dev/loom"
	"tuios.dev/tml"

	"github.com/Gaurav-Gosain/tuios/internal/markdown"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/tmldraw"
)

// Rich blocks: a fenced block of Mermaid or TML in an agent's reply or a
// Markdown file is drawn as a picture, by the renderer the agents thread and
// quick look share (cockpit_markdown.go). Both kinds go through loom, the
// engine that draws TML surfaces: a Mermaid block is a TML document of one
// <mermaid>, which sketch draws, and a type this build does not draw goes to
// the sketch plugin off the loop (renderSurface). The picture is static: no
// key or click reaches the document, so it runs no action and no command,
// and loom reads no file and no address.
//
// A header line over the picture names the kind, and a click on it shows
// the source as a code block, and a click on that header the picture again.
// The thread's copy (C, or the header of the source) copies the source. A
// block that does not parse shows its source under a line that says why.
// A block still being written stays code.
//
// A drawing is kept by kind, source, width and look, so a card that is built
// again draws nothing again.

// ckBlockRows bounds the rows of one drawing.
const ckBlockRows = 30

// ckBlocksMax bounds the drawings kept.
const ckBlocksMax = 64

// ckBlockHit starts the hit of a rich block's header: a click shows the
// source, or the picture again.
const ckBlockHit = "blk:"

// ckBlockKey is one drawing.
type ckBlockKey struct {
	kind, src    string
	width, gen   int
	look         uint64
	depth        overlay.Depth
	ascii, plain bool
}

// ckBlockDrawing is a drawing: its rows as styled text, or why there is none.
type ckBlockDrawing struct {
	rows []string
	fail string
}

// ckRichKind is the kind of rich block a fence names, "" for code.
func ckRichKind(lang string) string {
	switch strings.ToLower(lang) {
	case "mermaid":
		return "mermaid"
	case "tml":
		return "tml"
	}
	return ""
}

// richBlock lays out a fenced block of Mermaid or TML as a picture, or as its
// source when the person asked for it. It reports false for any other block,
// which is drawn as code.
func (md *ckMD) richBlock(b markdown.Block, first, rest []ckSeg, avail int) bool {
	kind := ckRichKind(b.Lang)
	if kind == "" || b.Open || len(b.Lines) == 0 || md.blockScope == "" {
		return false
	}
	m, l := md.m, md.l
	pal := l.pal
	src := strings.Join(b.Lines, "\n")
	key := md.blockScope + "/" + strconv.Itoa(md.nblocks)
	md.nblocks++
	at := ckSegsWidth(rest)
	head := ckLine{bg: l.panel, bgAt: at, segs: append(slicesClone(first), ckSeg{t: " " + kind, fg: pal.FgMute}), hit: ckBlockHit + key}
	pic, can := false, false
	if kind == "mermaid" {
		pic, can = m.mermaidShowsPicture(key)
	}
	if m.richSource[key] {
		again := "show the picture"
		if kind == "mermaid" {
			again = "show the diagram"
		}
		head.right = []ckSeg{{t: again, fg: pal.FgMute}, {t: "  "}}
		md.lines = append(md.lines, head)
		md.code(b, rest, rest, avail)
		return true
	}
	start := len(md.lines)
	md.lines = append(md.lines, head)
	drawn := pic && md.mermaidPictureLines(src, avail)
	if !drawn {
		d := m.blockDrawing(kind, src, max(avail-2, 8), pal)
		if d.fail != "" {
			md.lines = md.lines[:start]
			md.text([]ckSeg{{t: "Not drawn: " + d.fail, fg: pal.Warning}}, first, rest, avail)
			md.code(b, rest, rest, avail)
			return true
		}
		for _, row := range d.rows {
			md.lines = append(md.lines, ckLine{indent: at + 1, raw: row})
		}
	}
	md.lines[start] = ckBlockHead(head, key, can, drawn, pal)
	md.lines = append(md.lines, ckLine{segs: slicesClone(rest)})
	if md.t != nil {
		// C and the source's own header copy the source.
		code := md.id + "/" + strconv.Itoa(md.codes)
		md.codes++
		if md.t.code == nil {
			md.t.code = make(map[string]ckCode)
		}
		md.t.code[code] = ckCode{text: src}
		for i := start; i < len(md.lines); i++ {
			md.lines[i].code = code
		}
	}
	return true
}

// blockDrawing is the drawing of a rich block at width cells, made once.
func (m *OS) blockDrawing(kind, src string, width int, pal overlay.Palette) *ckBlockDrawing {
	k := ckBlockKey{kind: kind, src: src, width: width, gen: m.blockGen, look: theme.Generation(),
		depth: pal.Depth, ascii: overlay.UseASCII(), plain: m.Settings.NoNerdFont}
	if d := m.blocks[k]; d != nil {
		return d
	}
	if m.blocks == nil || len(m.blocks) >= ckBlocksMax {
		m.blocks = make(map[ckBlockKey]*ckBlockDrawing)
	}
	d := m.drawBlock(kind, src, width, pal, k.ascii)
	m.blocks[k] = d
	return d
}

// drawBlock draws a rich block with loom, at most ckBlockRows rows.
func (m *OS) drawBlock(kind, src string, width int, pal overlay.Palette, ascii bool) *ckBlockDrawing {
	doc := src
	switch {
	case kind == "mermaid":
		doc = `<tml version="1"><mermaid>` + ckXMLText.Replace(src) + `</mermaid></tml>`
	case !strings.HasPrefix(strings.TrimSpace(src), "<tml"):
		doc = `<tml version="1">` + src + `</tml>`
	}
	lim := tml.DefaultLimits()
	node, problems, err := tml.Check([]byte(doc), &tml.ValidateOptions{Limits: &lim, NoStateCheck: true})
	if err != nil {
		why := "the block is not valid TML."
		for _, p := range problems {
			if p.Severity == tml.SevError && p.Message != "" {
				why = p.Message
				break
			}
		}
		return &ckBlockDrawing{fail: ckTrunc(why, 120)}
	}
	s, err := loom.New(node, &loom.Options{Palette: tmldraw.Palette(pal, ascii), Ground: loom.GroundNone, Limits: &lim,
		CellAspect: m.replyPreviewer().CellAspect()})
	if err != nil {
		return &ckBlockDrawing{fail: ckTrunc(err.Error(), 120)}
	}
	buf := m.renderSurface(s, width, ckBlockRows)
	plain := strings.Split(buf.String(), "\n")
	rows := strings.Split(buf.Render(), "\n")
	n := min(len(plain), len(rows))
	for n > 0 && strings.TrimSpace(plain[n-1]) == "" {
		n--
	}
	if n == 0 {
		return &ckBlockDrawing{fail: "the block draws nothing."}
	}
	return &ckBlockDrawing{rows: rows[:n]}
}

// ckXMLText escapes text for an XML element.
var ckXMLText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// ToggleBlock shows the source of the rich block key, or its picture again.
func (m *OS) ToggleBlock(key string) {
	if m.richSource == nil {
		m.richSource = make(map[string]bool)
	}
	m.richSource[key] = !m.richSource[key]
	m.blockView++
	m.blocksChanged()
}

// CockpitBlockToggle is P: the lowest rich block on screen shows its source,
// or its picture again. With pic it is T: the lowest Mermaid block on screen
// that can be a picture is drawn as a picture, or as text. A block whose
// header scrolled off the top counts when its lines reach the top row.
func (m *OS) CockpitBlockToggle(pic bool) {
	v := m.Inbox.ck.code
	if v.t != nil {
		lines := v.t.lines
		for i := min(v.end, len(lines)) - 1; i >= 0; i-- {
			main, alt, _, _, split := ckSplitRead(lines[i].hit)
			key, ok := strings.CutPrefix(main, ckBlockHit)
			if !ok {
				continue
			}
			if i < v.start && (i+1 >= len(lines) || v.start >= len(lines) || lines[v.start].code == "" || lines[v.start].code != lines[i+1].code) {
				break
			}
			switch {
			case !pic:
				m.ToggleBlock(key)
				return
			case split:
				m.ToggleBlockPicture(strings.TrimPrefix(alt, ckBlockPicHit))
				return
			}
		}
	}
	what := "No diagram on screen."
	if pic {
		what = "No Mermaid diagram on screen that can be a picture."
	}
	m.ShowNotification(what, "info", m.Settings.NotificationDuration)
}

// ckBlockPicHit starts the hit of a Mermaid block's picture and text switch.
const ckBlockPicHit = "bpic:"

// ckBlockHead is a drawn block's header: "show the source" on the right, and,
// for a Mermaid block this client can draw as a picture, the switch between
// picture and text before it, the drawing in use in bold. A click on the
// switch flips the drawing. A click anywhere else on the header shows the
// source.
func ckBlockHead(head ckLine, key string, can, pic bool, pal overlay.Palette) ckLine {
	source := []ckSeg{{t: "show the source", fg: pal.FgMute}, {t: "  "}}
	if !can {
		head.right = source
		return head
	}
	p, x := ckSeg{t: "picture", fg: pal.FgMute}, ckSeg{t: "text", fg: pal.FgMute}
	if pic {
		p.fg, p.attrs = pal.Fg, ckBold
	} else {
		x.fg, x.attrs = pal.Fg, ckBold
	}
	sw := []ckSeg{p, {t: " / ", fg: pal.FgMute}, x}
	head.right = append(append(sw, ckSeg{t: "   "}), source...)
	from := ckSegsWidth(head.right)
	head.hit = ckSplitHit(head.hit, ckBlockPicHit+key, from, from-ckSegsWidth(sw))
	return head
}

// ckSplitHit gives part of a line's hit to another action: a click from
// "from" cells to "to" cells left of the line's right edge does alt, and a
// click anywhere else on the line does hit.
func ckSplitHit(hit, alt string, from, to int) string {
	return hit + "\x1f" + strconv.Itoa(from) + "\x1f" + strconv.Itoa(to) + "\x1f" + alt
}

// ckSplitRead reads a hit ckSplitHit made. A plain hit is main, with split
// false.
func ckSplitRead(hit string) (main, alt string, from, to int, split bool) {
	parts := strings.SplitN(hit, "\x1f", 4)
	if len(parts) != 4 {
		return hit, "", 0, 0, false
	}
	f, e1 := strconv.Atoi(parts[1])
	t, e2 := strconv.Atoi(parts[2])
	if e1 != nil || e2 != nil {
		return parts[0], "", 0, 0, false
	}
	return parts[0], parts[3], f, t, true
}

// ckLineHits adds the hits of a line from column x0 to x1 on screen row y,
// its right edge at column edge: the switch of a split hit first, so a click
// on it finds it before the line's own action.
func ckLineHits(hits []ckHit, hit string, x0, x1, edge, y int) []ckHit {
	main, alt, from, to, split := ckSplitRead(hit)
	if split {
		a, b := max(edge-from, x0), min(edge-to, x1)
		if a < b {
			hits = append(hits, ckHit{x0: a, y0: y, x1: b, y1: y + 1, kind: ckHitAction, action: alt})
		}
	}
	return append(hits, ckHit{x0: x0, y0: y, x1: x1, y1: y + 1, kind: ckHitAction, action: main})
}

// blocksChanged makes every layout that may hold a rich block again: the
// threads and quick look's Markdown.
func (m *OS) blocksChanged() {
	for _, t := range m.Inbox.threads.byKey {
		t.gen++
	}
	m.cockpitTouch()
	m.quickLook.mdGen++
}
