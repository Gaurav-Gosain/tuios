package app

import (
	"image/color"
	"os"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

var fastBoxDisabled = os.Getenv("TUIOS_NO_FASTBOX") == "1"

func (m *OS) fastWindowBox(content string, window *terminal.Window, borderColorObj color.Color, position int, isTiling bool) (string, bool) {
	if fastBoxDisabled {
		return "", false
	}
	if window.Width < 3 || window.Height < 3 {
		return "", false
	}
	if strings.IndexByte(content, '\t') >= 0 || strings.IndexByte(content, '\r') >= 0 {
		return "", false
	}

	left, right, ok := borderSideCells(borderColorObj, &m.Settings)
	if !ok {
		return "", false
	}

	top, bottom := m.windowBorderRows(window.ContentWidth(), borderColorObj, window, position, isTiling)

	var b strings.Builder
	b.Grow(len(top) + len(bottom) + len(content) + (len(left)+len(right)+1)*(window.Height-1) + 1)
	b.WriteString(top)
	for line := range strings.SplitSeq(content, "\n") {
		b.WriteByte('\n')
		b.WriteString(left)
		b.WriteString(line)
		b.WriteString(right)
	}
	b.WriteByte('\n')
	b.WriteString(bottom)
	return b.String(), true
}

func borderSideCells(borderColorObj color.Color, s *config.Settings) (left, right string, ok bool) {
	border := getBorder(s)
	if !isSingleRune(border.Left) || !isSingleRune(border.Right) {
		return "", "", false
	}
	var style ansi.Style
	if borderColorObj == nil {
		return "", "", false
	}
	style = style.ForegroundColor(ansi.Color(borderColorObj))
	return style.Styled(border.Left), style.Styled(border.Right), true
}

func isSingleRune(s string) bool {
	if s == "" {
		return false
	}
	n := 0
	for range s {
		n++
		if n > 1 {
			return false
		}
	}
	return true
}
