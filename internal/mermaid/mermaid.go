// Package mermaid is the tuios side of Mermaid diagrams: it finds the
// diagrams in a file, colours them from the tuios palette, and writes them as
// kitty graphics or as box-drawing text. The drawing itself is tuios.dev/sketch.
package mermaid

import (
	"bytes"
	"encoding/base64"
	"image/color"
	"io"
	"regexp"
	"strconv"
	"strings"

	"tuios.dev/sketch"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// Theme is the sketch theme of a tuios palette, for a diagram drawn on the pane's
// own background bg with its text colour fg. Every colour that carries text or
// a line is held to a contrast floor against bg, so the diagram reads on any
// theme: text at overlay.ContrastFloor, lines and borders at overlay.MarkFloor.
// Boxes are bg lifted a little toward fg, and their text is checked again on
// that fill. The eight series colours are the ANSI colours 1 to 6 with Info and
// Success, at the mark floor.
func Theme(pal overlay.Palette, ansi [16]color.Color, bg, fg color.Color) *sketch.Theme {
	if bg == nil {
		bg = pal.Surface
	}
	if fg == nil {
		fg = pal.Fg
	}
	surface := overlay.MixColors(bg, fg, 0.18)
	text := overlay.Readable(overlay.Readable(fg, bg), surface)
	mark := func(c color.Color) color.Color {
		if c == nil {
			c = fg
		}
		return overlay.ReadableAt(c, bg, overlay.MarkFloor)
	}
	t := &sketch.Theme{
		Bg:         bg,
		Fg:         text,
		Muted:      overlay.Readable(pal.FgDim, bg),
		Line:       mark(pal.FgDim),
		Surface:    surface,
		SurfaceAlt: overlay.MixColors(bg, fg, 0.1),
		Border:     mark(pal.FgDim),
		Accent:     mark(pal.Accent),
		Critical:   mark(pal.Warn),
	}
	for i := 0; i < 6; i++ {
		t.Series[i] = mark(ansi[i+1])
	}
	t.Series[6] = mark(pal.Info)
	t.Series[7] = mark(pal.Success)
	return t
}

var reFence = regexp.MustCompile("(?ms)^[ \t]*(```+|~~~+)[ \t]*mermaid[^\n]*\n(.*?)^[ \t]*(```+|~~~+)[ \t]*$")

// Sources lists the diagrams in a file: each mermaid fence of a Markdown
// file, or the whole text of any other.
func Sources(name string, data []byte) []string {
	text := string(data)
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown") || (name == "-" && strings.Contains(text, "```mermaid")) {
		var out []string
		for _, m := range reFence.FindAllStringSubmatch(text, -1) {
			out = append(out, m[2])
		}
		return out
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []string{text}
}

// Kitty writes a PNG as a kitty graphics image cols cells wide. The terminal
// keeps the picture's aspect and moves the cursor below it.
func Kitty(w io.Writer, png []byte, cols int) error {
	enc := base64.StdEncoding.EncodeToString(png)
	var b bytes.Buffer
	const chunk = 4096
	for i := 0; i < len(enc); i += chunk {
		end := min(i+chunk, len(enc))
		more := 0
		if end < len(enc) {
			more = 1
		}
		b.WriteString("\x1b_G")
		if i == 0 {
			b.WriteString("a=T,f=100,q=2,c=" + strconv.Itoa(cols) + ",")
		}
		b.WriteString("m=" + strconv.Itoa(more) + ";" + enc[i:end] + "\x1b\\")
	}
	b.WriteString("\r\n")
	_, err := w.Write(b.Bytes())
	return err
}
