// Package mermaid is the tuios side of Mermaid diagrams: it finds the
// diagrams in a file, colours them from the tuios palette, and writes them as
// kitty graphics or as box-drawing text. The drawing itself is tuios.dev/sketch.
package mermaid

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/color"
	"io"
	"regexp"
	"strconv"
	"strings"

	"tuios.dev/sketch"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// Theme is the sketch theme of a tuios palette, for a diagram drawn on the
// pane's own background bg with its text colour fg. It maps the palette the
// way pele's launch video maps its palettes: lines in the text colour, quiet
// node fills a step off the background, the accent the blue of the theme and
// the eight series colours its ANSI hues, red, orange, yellow, green, cyan,
// blue, purple and pink. Every colour that carries text or a line is held to
// a contrast floor against bg: text at overlay.ContrastFloor, lines, marks and
// series at overlay.MarkFloor.
func Theme(pal overlay.Palette, ansi [16]color.Color, bg, fg color.Color) *sketch.Theme {
	if bg == nil {
		bg = pal.Surface
	}
	if fg == nil {
		fg = pal.Fg
	}
	pick := func(i int, fallback color.Color) color.Color {
		if ansi[i] != nil {
			return ansi[i]
		}
		return fallback
	}
	red, green, yellow := pick(1, pal.Warn), pick(2, pal.Success), pick(3, pal.Warning)
	blue, magenta, cyan := pick(4, pal.Info), pick(5, pal.Accent), pick(6, pal.Info)
	// Node fills a step toward the text colour, tinted a little with the accent,
	// and the border two steps further, like pele's Flexoki and Nord.
	accent := pal.Accent
	if accent == nil {
		accent = blue
	}
	surface := overlay.MixColors(overlay.MixColors(bg, fg, 0.07), accent, 0.04)
	surfaceAlt := overlay.MixColors(bg, fg, 0.12)
	border := overlay.MixColors(bg, fg, 0.22)
	text := readableOn(fg, overlay.ContrastFloor, bg, surface, surfaceAlt)
	mark := func(c color.Color) color.Color { return overlay.ReadableAt(c, bg, overlay.MarkFloor) }
	t := &sketch.Theme{
		Bg:         bg,
		Fg:         text,
		Muted:      readableOn(overlay.MixColors(fg, bg, 0.35), overlay.ContrastFloor, bg, surfaceAlt),
		Line:       overlay.ReadableAt(overlay.MixColors(fg, bg, 0.1), bg, overlay.ContrastFloor),
		Surface:    surface,
		SurfaceAlt: surfaceAlt,
		Border:     border,
		Accent:     mark(blue),
		Critical:   mark(red),
		Radius:     6,
	}
	series := []color.Color{red, overlay.MixColors(red, yellow, 0.5), yellow, green, cyan, blue, magenta, overlay.MixColors(magenta, red, 0.4)}
	for i, c := range series {
		t.Series[i] = mark(c)
	}
	return t
}

// readableOn moves c toward white or black, whichever reads better on the
// grounds, until it reaches floor on all of them.
func readableOn(c color.Color, floor float64, grounds ...color.Color) color.Color {
	least := func(c color.Color) float64 {
		m := 100.0
		for _, g := range grounds {
			m = min(m, overlay.ContrastRatio(c, g))
		}
		return m
	}
	if least(c) >= floor {
		return c
	}
	// The least change that reaches the floor, toward white or toward black.
	best, bestT := color.Color(nil), 2.0
	for _, end := range []color.Color{color.White, color.Black} {
		for t := 0.05; t < bestT && t <= 1.0001; t += 0.05 {
			if k := overlay.MixColors(c, end, t); least(k) >= floor {
				best, bestT = k, t
				break
			}
		}
	}
	if best != nil {
		return best
	}
	if least(color.White) > least(color.Black) {
		return color.White
	}
	return color.Black
}

// ThemeNames lists the names --theme and [mermaid] theme take.
func ThemeNames() []string {
	names := []string{"tuios"}
	for _, t := range sketch.Themes() {
		names = append(names, t.Name)
	}
	return names
}

// ThemeFor is the theme a name gives: "" or "tuios" builds one from the tuios
// palette, any other name is one of sketch's named themes.
func ThemeFor(name string, pal overlay.Palette, ansi [16]color.Color, bg, fg color.Color) (*sketch.Theme, error) {
	if name == "" || strings.EqualFold(name, "tuios") {
		return Theme(pal, ansi, bg, fg), nil
	}
	if t, ok := sketch.ThemeNamed(name); ok {
		return t, nil
	}
	return nil, fmt.Errorf("no theme named %q. Use one of: %s", name, strings.Join(ThemeNames(), ", "))
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
