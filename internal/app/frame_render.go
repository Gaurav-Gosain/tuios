package app

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"tuios.dev/vt"
)

type frameRenderer struct {
	out   []byte
	diffs map[[2]uv.Style]string
}

const maxDiffMemo = 4096

func (r *frameRenderer) diff(from, to *uv.Style) string {
	key := [2]uv.Style{*from, *to}
	if s, ok := r.diffs[key]; ok {
		return s
	}
	if r.diffs == nil || len(r.diffs) >= maxDiffMemo {
		r.diffs = make(map[[2]uv.Style]string, 64)
	}
	s := uv.StyleDiff(from, to)
	r.diffs[key] = s
	return s
}

func (r *frameRenderer) render(lines []uv.Line) string {
	out := r.out[:0]
	for i, l := range lines {
		start := len(out)
		out = r.renderLine(out, l)
		out = trimLineRight(out, start)
		if i < len(lines)-1 {
			out = append(out, '\n')
		}
	}
	r.out = out
	return string(out)
}

func trimLineRight(out []byte, start int) []byte {
	end := len(out)
	hasCR := end > start && out[end-1] == '\r'
	if hasCR {
		end--
	}
	for end > start && out[end-1] == ' ' {
		end--
	}
	if hasCR {
		out[end] = '\r'
		end++
	}
	return out[:end]
}

func styleIsZero(s *uv.Style) bool {
	return *s == uv.Style{}
}

func styleEqual(a, b *uv.Style) bool {
	return *a == *b || a.Equal(b)
}

func isEmptyCell(c *uv.Cell) bool {
	return c.Width == 1 && c.Content == " " && styleIsZero(&c.Style) && c.Link == uv.Link{}
}

func (r *frameRenderer) renderLine(out []byte, l uv.Line) []byte {
	var pen uv.Style
	var link uv.Link
	pending := 0

	for i := range l {
		c := &l[i]
		if c.IsZero() {
			continue
		}
		if isEmptyCell(c) {
			if !styleIsZero(&pen) {
				out = append(out, ansi.ResetStyle...)
				pen = uv.Style{}
			}
			if link != (uv.Link{}) {
				out = append(out, resetHyperlink...)
				link = uv.Link{}
			}
			pending++
			continue
		}

		for ; pending > 0; pending-- {
			out = append(out, ' ')
		}

		if styleIsZero(&c.Style) && !styleIsZero(&pen) {
			out = append(out, ansi.ResetStyle...)
			pen = uv.Style{}
		}
		if !styleEqual(&c.Style, &pen) {
			out = append(out, r.diff(&pen, &c.Style)...)
			pen = c.Style
		}

		if c.Link != link && link.URL != "" {
			out = append(out, resetHyperlink...)
			link = uv.Link{}
		}
		if c.Link != link {
			out = append(out, vt.SetHostHyperlink(c.Link.URL, c.Link.Params)...)
			link = c.Link
		}

		out = append(out, c.Content...)
	}

	for ; pending > 0; pending-- {
		out = append(out, ' ')
	}
	if link.URL != "" {
		out = append(out, resetHyperlink...)
	}
	if !styleIsZero(&pen) {
		out = append(out, ansi.ResetStyle...)
	}
	return out
}

var resetHyperlink = ansi.ResetHyperlink()
