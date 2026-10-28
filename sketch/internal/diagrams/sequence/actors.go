// Ported from pele src/diagrams/sequence/actors.ts at 8aff183be57021b533f319a29341a529be934b08.

package sequence

import (
	"math"

	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
)

const (
	PAD_X     = 16
	PAD_Y     = 10
	GLYPH     = 34
	GLYPH_GAP = 4
	STACK     = 5
	CAP       = 6
	SHAPE     = ` fill="var(--_s)" stroke="var(--_b)"`
	LINE      = ` fill="none" stroke="var(--_b)"`
)

var KINDS = map[string]bool{"participant": true, "actor": true, "boundary": true, "control": true, "entity": true, "database": true, "collections": true, "queue": true}
var FIGURES = map[string]bool{"actor": true, "boundary": true, "control": true, "entity": true, "database": true}

type ActorSize struct {
	W float64
	H float64
}

func actorKind(typ string) string {
	if KINDS[typ] {
		return typ
	}
	return "participant"
}

// isFigure: a figure stands above its label. The other kinds hold the label inside.
func isFigure(kind string) bool {
	return FIGURES[kind]
}

func actorSize(kind string, label text.Label, minW, minH, paddingX float64) ActorSize {
	if isFigure(kind) {
		h := float64(GLYPH)
		if label.Height > 0 {
			h += GLYPH_GAP + label.Height
		}
		return ActorSize{math.Max(40, label.Width+8), h}
	}
	extra := 0.0
	if kind == "collections" {
		extra = STACK
	} else if kind == "queue" {
		extra = 2 * CAP
	}
	stack := 0.0
	if kind == "collections" {
		stack = STACK
	}
	return ActorSize{
		W: math.Max(minW, label.Width+float64(2*paddingX)) + extra,
		H: math.Max(minH, label.Height+2*PAD_Y) + stack,
	}
}

func figure(kind string, cx, top float64) string {
	x := svg.Num(cx)
	y := func(dy float64) string { return svg.Num(top + dy) }
	switch kind {
	case "actor":
		return `<circle cx="` + x + `" cy="` + y(7) + `" r="6"` + SHAPE + `/>` +
			`<path d="M` + x + `,` + y(13) + `V` + y(24) + `M` + svg.Num(cx-10) + `,` + y(17) + `H` + svg.Num(cx+10) + `M` + svg.Num(cx-8) + `,` + y(34) + `L` + x + `,` + y(24) + `L` + svg.Num(cx+8) + `,` + y(34) + `"` + LINE + `/>`
	case "boundary":
		return `<path d="M` + svg.Num(cx-19) + `,` + y(6) + `V` + y(28) + `M` + svg.Num(cx-19) + `,` + y(17) + `H` + svg.Num(cx-8) + `"` + LINE + `/>` +
			`<circle cx="` + svg.Num(cx+3) + `" cy="` + y(17) + `" r="11"` + SHAPE + `/>`
	case "control":
		return `<circle cx="` + x + `" cy="` + y(21) + `" r="11"` + SHAPE + `/>` +
			`<path d="M` + svg.Num(cx+5) + `,` + y(5) + `L` + svg.Num(cx-1) + `,` + y(10) + `L` + svg.Num(cx+5) + `,` + y(15) + `"` + LINE + `/>`
	case "entity":
		return `<circle cx="` + x + `" cy="` + y(15) + `" r="11"` + SHAPE + `/>` + `<path d="M` + svg.Num(cx-11) + `,` + y(30) + `H` + svg.Num(cx+11) + `"` + LINE + `/>`
	}
	l := svg.Num(cx - 14)
	r := svg.Num(cx + 14)
	return `<path d="M` + l + `,` + y(7) + `V` + y(27) + `A14,5 0 0 0 ` + r + `,` + y(27) + `V` + y(7) + `A14,5 0 0 0 ` + l + `,` + y(7) + `Z"` + SHAPE + `/>` +
		`<path d="M` + l + `,` + y(7) + `A14,5 0 0 0 ` + r + `,` + y(7) + `"` + LINE + `/>`
}

// drawActor draws a participant whose bounding box has its top center at (cx, top).
// ink is the fill attribute for a figure's label, which sits on whatever is behind the figure.
func drawActor(kind string, cx, top float64, size ActorSize, label text.Label, icons text.IconResolver, ink string) string {
	w, h := size.W, size.H
	const txt = ` class="pele-label"`
	if isFigure(kind) {
		return figure(kind, cx, top) + svg.LabelSvg(label, cx, top+GLYPH+GLYPH_GAP+label.Height/2, txt+ink, icons)
	}
	left := cx - w/2
	if kind == "collections" {
		bw := w - STACK
		bh := h - STACK
		back := `<rect x="` + svg.Num(left+STACK) + `" y="` + svg.Num(top) + `" width="` + svg.Num(bw) + `" height="` + svg.Num(bh) + `"` + SHAPE + svg.ROUNDED + `/>`
		front := `<rect x="` + svg.Num(left) + `" y="` + svg.Num(top+STACK) + `" width="` + svg.Num(bw) + `" height="` + svg.Num(bh) + `"` + SHAPE + svg.ROUNDED + `/>`
		return back + front + svg.LabelSvg(label, left+bw/2, top+STACK+bh/2, txt, icons)
	}
	if kind == "queue" {
		l := svg.Num(left + CAP)
		r := svg.Num(left + w - CAP)
		b := svg.Num(top + h)
		ry := svg.Num(h / 2)
		return `<path d="M` + l + `,` + svg.Num(top) + `H` + r + `A6,` + ry + ` 0 0 1 ` + r + `,` + b + `H` + l + `A6,` + ry + ` 0 0 1 ` + l + `,` + svg.Num(top) + `Z"` + SHAPE + `/>` +
			`<path d="M` + r + `,` + svg.Num(top) + `A6,` + ry + ` 0 0 0 ` + r + `,` + b + `"` + LINE + `/>` +
			svg.LabelSvg(label, cx-CAP/2, top+h/2, txt, icons)
	}
	return `<rect x="` + svg.Num(left) + `" y="` + svg.Num(top) + `" width="` + svg.Num(w) + `" height="` + svg.Num(h) + `"` + SHAPE + svg.ROUNDED + `/>` +
		svg.LabelSvg(label, cx, top+h/2, txt, icons)
}
