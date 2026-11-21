package app

import (
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/invisible"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/vt"
)

const navStyledMaxCols = 400

type navInk struct {
	on          bool
	fg, bg, cur color.Color
	ansiPalette [16]color.Color
}

func navThemeInk() navInk {
	fg, bg, cur, pal, ok := theme.EmulatorColors()
	if !ok {
		return navInk{}
	}
	return navInk{on: true, fg: fg, bg: bg, cur: cur, ansiPalette: pal}
}

type navParser struct {
	emu *vt.Emulator
}

func newNavParser(ink navInk) *navParser {
	emu := vt.NewEmulator(navStyledMaxCols, 1)
	if ink.on {
		emu.SetThemeColors(ink.fg, ink.bg, ink.cur, ink.ansiPalette)
	}
	return &navParser{emu: emu}
}

func (p *navParser) Close() { _ = p.emu.Close() }

func (p *navParser) parse(content string, keep int) (plain, styled []string) {
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, "\r")
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > keep {
		lines = lines[len(lines)-keep:]
	}
	if len(lines) == 0 {
		return nil, nil
	}
	cols := 1
	for _, l := range lines {
		cols = max(cols, ansi.StringWidth(l))
	}
	cols = min(cols, navStyledMaxCols)

	emu := p.emu
	plain = make([]string, 0, len(lines))
	styled = make([]string, 0, len(lines))
	for _, l := range lines {
		_, _ = emu.WriteString("\x1bc\x1b[?7l")
		_, _ = emu.WriteString(l)
		line := navCellRow(emu, 0, cols)
		plain = append(plain, strings.TrimRight(line.String(), " "))
		styled = append(styled, line.Render())
	}
	return plain, styled
}

func navCellRow(emu *vt.Emulator, y, cols int) uv.Line {
	line := make(uv.Line, cols)
	end := 0
	for x := range cols {
		line[x] = uv.EmptyCell
		c := emu.CellAt(x, y)
		if c == nil {
			continue
		}
		cell := *c
		cell.Link = uv.Link{}
		switch {
		case cell.Width == 0 && cell.Content == "":
		case vt.IsKittyPlaceholder(cell.Content):
			cell = uv.EmptyCell
		default:
			if txt := navPrintable(cell.Content); txt != cell.Content {
				cell.Content = txt
				if txt == "" {
					cell.Content, cell.Width = " ", 1
				}
			}
		}
		line[x] = cell
		if (cell.Content != "" && cell.Content != " ") || cell.Style.Bg != nil || cell.Style.Attrs&uv.AttrReverse != 0 {
			end = x + max(cell.Width, 1)
		}
	}
	return line[:min(end, cols)]
}

func navPrintable(s string) string {
	clean := true
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			clean = false
			break
		}
	}
	if !clean {
		var b strings.Builder
		for _, r := range s {
			if r >= 0x20 && (r < 0x7f || r >= 0xa0) {
				b.WriteRune(r)
			}
		}
		s = b.String()
	}
	return invisible.Strip(s)
}

func navScreenLine(s string, cols int) string {
	if cols <= 0 {
		return ""
	}
	cut := ansi.Truncate(s, cols, "")
	w := ansi.StringWidth(cut)
	return cut + "\x1b[m" + strings.Repeat(" ", max(cols-w, 0))
}
