package app

import (
	"image"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

type fastPainter struct {
	out          []byte
	parser       *ansi.Parser
	fgNil, bgNil bool
	fg, bg       string
}

func fastPaintFits(window *terminal.Window) bool {
	r := paneContentRect(window).Sub(image.Pt(window.X, window.Y))
	switch {
	case r.Min.X == 0 && r.Max.X == window.Width:
		return true
	case r.Min.X == 1 && r.Max.X == window.Width-1 && window.Width >= 2:
		return true
	}
	return false
}

func (m *OS) paintFullscreenFrame(window *terminal.Window, box, dock, dockPos string) string {
	pane := m.surfaceGround(surfacePane)
	chrome := m.surfaceGround(surfaceChrome)
	dockGround := m.surfaceGround(surfaceDock)
	rect := paneContentRect(window).Sub(image.Pt(window.X, window.Y))

	p := &m.fastPaint
	if p.parser == nil {
		p.parser = ansi.NewParser()
		p.parser.SetDataSize(4 * 1024)
	}
	p.out = p.out[:0]
	p.fgNil, p.bgNil = true, true
	p.fg, p.bg = "", ""

	if dockPos == "top" {
		p.block(dock, &dockGround)
		p.out = append(p.out, '\n')
	}
	row := 0
	for line := range strings.SplitSeq(box, "\n") {
		if row > 0 {
			p.out = append(p.out, '\n')
		}
		switch {
		case row < rect.Min.Y || row >= rect.Max.Y:
			p.line(line, &chrome, nil)
		case rect.Min.X == 0:
			p.line(line, &pane, nil)
		default:
			p.line(line, &chrome, &pane)
		}
		row++
	}
	if dockPos != "top" && dockPos != "hidden" {
		p.out = append(p.out, '\n')
		p.block(dock, &dockGround)
	}
	return string(p.out)
}

func (p *fastPainter) block(s string, g *ground) {
	first := true
	for line := range strings.SplitSeq(s, "\n") {
		if !first {
			p.out = append(p.out, '\n')
		}
		first = false
		p.line(line, g, nil)
	}
}

func (p *fastPainter) line(s string, outer, inner *ground) {
	last := -1
	if inner != nil {
		last = lastTextRune(s)
	}
	seenText := false
	for i := 0; i < len(s); {
		if s[i] == ansi.ESC {
			i += p.escape(s[i:])
			continue
		}
		end := len(s)
		if j := strings.IndexByte(s[i:], ansi.ESC); j >= 0 {
			end = i + j
		}
		if inner == nil {
			p.text(s[i:end], outer)
			i = end
			continue
		}
		pos := i
		if !seenText {
			seenText = true
			_, size := utf8.DecodeRuneInString(s[pos:end])
			p.text(s[pos:pos+size], outer)
			pos += size
		}
		if pos < end {
			if last >= pos && last < end {
				p.text(s[pos:last], inner)
				p.text(s[last:end], outer)
			} else {
				p.text(s[pos:end], inner)
			}
		}
		i = end
	}
}

func (p *fastPainter) text(s string, g *ground) {
	if s == "" {
		return
	}
	if p.bgNil && p.bg != g.bgSGR {
		if g.bgSGR == "" {
			p.out = append(p.out, "\x1b[49m"...)
		} else {
			p.out = append(p.out, g.bgSGR...)
		}
		p.bg = g.bgSGR
	}
	if p.fgNil && p.fg != g.fgSGR {
		if g.fgSGR == "" {
			p.out = append(p.out, "\x1b[39m"...)
		} else {
			p.out = append(p.out, g.fgSGR...)
		}
		p.fg = g.fgSGR
	}
	p.out = append(p.out, s...)
}

const (
	resetKeepBoth = "\x1b[22;23;24;25;27;28;29;59m"
	resetKeepBg   = "\x1b[22;23;24;25;27;28;29;39;59m"
	resetKeepFg   = "\x1b[22;23;24;25;27;28;29;49;59m"
)

func (p *fastPainter) escape(s string) int {
	seq, _, n, _ := ansi.DecodeSequence(s, ansi.NormalState, p.parser)
	if n <= 0 {
		n = 1
	}
	if (p.bg != "" || p.fg != "") && (seq == ansi.ResetStyle || seq == "\x1b[0m") {
		switch {
		case p.bg != "" && p.fg != "":
			p.out = append(p.out, resetKeepBoth...)
		case p.bg != "":
			p.out = append(p.out, resetKeepBg...)
		default:
			p.out = append(p.out, resetKeepFg...)
		}
		p.fgNil, p.bgNil = true, true
		return n
	}
	p.out = append(p.out, s[:n]...)
	if ansi.HasCsiPrefix(seq) && p.parser.Command() == 'm' {
		p.sgr(p.parser.Params())
	}
	return n
}

func (p *fastPainter) sgr(params ansi.Params) {
	if len(params) == 0 {
		p.resetPen()
		return
	}
	for i := 0; i < len(params); i++ {
		param, hasMore, _ := params.Param(i, 0)
		switch {
		case param == 0:
			p.resetPen()
		case param == 4:
			if next, _, ok := params.Param(i+1, 0); hasMore && ok && next >= 0 && next <= 5 {
				i++
			}
		case param >= 30 && param <= 37, param >= 90 && param <= 97:
			p.setFg(false)
		case param == 39:
			p.setFg(true)
		case param >= 40 && param <= 47, param >= 100 && param <= 107:
			p.setBg(false)
		case param == 49:
			p.setBg(true)
		case param == 38, param == 48, param == 58:
			n, isNil := sgrColorSpan(params[i:])
			if n > 0 {
				switch param {
				case 38:
					p.setFg(isNil)
				case 48:
					p.setBg(isNil)
				}
				i += n - 1
			}
		}
	}
}

func (p *fastPainter) resetPen() {
	p.fgNil, p.bgNil = true, true
	p.fg, p.bg = "", ""
}

func (p *fastPainter) setFg(isNil bool) { p.fgNil, p.fg = isNil, "" }

func (p *fastPainter) setBg(isNil bool) { p.bgNil, p.bg = isNil, "" }

func lastTextRune(s string) int {
	last := -1
	for i := 0; i < len(s); {
		if s[i] == ansi.ESC {
			i += escapeLen(s[i:])
			continue
		}
		last = i
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return last
}

func escapeLen(s string) int {
	_, _, n, _ := ansi.DecodeSequence(s, ansi.NormalState, nil)
	if n <= 0 {
		return 1
	}
	return n
}

func sgrColorSpan(params ansi.Params) (n int, isNil bool) {
	if len(params) < 2 {
		return 0, false
	}
	s, p := params[0], params[1]
	colorType := p.Param(0)

	direct := func() (n int, four, ok bool) {
		switch {
		case s.HasMore() && p.HasMore() && len(params) > 8 && params[2].HasMore() && params[3].HasMore() && params[4].HasMore() && params[5].HasMore() && params[6].HasMore() && params[7].HasMore():
			return 9, true, true
		case s.HasMore() && p.HasMore() && len(params) > 7 && params[2].HasMore() && params[3].HasMore() && params[4].HasMore() && params[5].HasMore() && params[6].HasMore():
			return 8, true, true
		case s.HasMore() && p.HasMore() && len(params) > 6 && params[2].HasMore() && params[3].HasMore() && params[4].HasMore() && params[5].HasMore():
			return 7, true, true
		case s.HasMore() && p.HasMore() && len(params) > 5 && params[2].HasMore() && params[3].HasMore() && params[4].HasMore() && !params[5].HasMore():
			return 6, false, true
		case s.HasMore() && p.HasMore() && p.Param(0) == 2 && params[2].HasMore() && params[3].HasMore() && !params[4].HasMore():
			return 5, false, true
		case !s.HasMore() && !p.HasMore() && p.Param(0) == 2 && !params[2].HasMore() && !params[3].HasMore() && !params[4].HasMore():
			return 5, false, true
		}
		return 0, false, false
	}

	switch colorType {
	case 0:
		return 2, true
	case 1:
		return 2, false
	case 2, 3:
		if len(params) < 5 {
			return 0, false
		}
		if n, _, ok := direct(); ok {
			return n, false
		}
		return 0, false
	case 4, 6:
		if len(params) < 6 {
			return 0, false
		}
		if n, four, ok := direct(); ok && four {
			return n, false
		}
		return 0, false
	case 5:
		if len(params) < 3 {
			return 0, false
		}
		switch {
		case s.HasMore() && p.HasMore() && !params[2].HasMore():
		case !s.HasMore() && !p.HasMore() && !params[2].HasMore():
		default:
			return 0, false
		}
		return 3, false
	}
	return 0, false
}
