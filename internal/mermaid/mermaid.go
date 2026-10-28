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
	t.FgOnLight, t.FgOnDark = onFills(bg, text)
	series := []color.Color{red, overlay.MixColors(red, yellow, 0.5), yellow, green, cyan, blue, magenta, overlay.MixColors(magenta, red, 0.4)}
	for i, c := range series {
		t.Series[i] = mark(c)
	}
	return t
}

// fillFlip is the grey where pele switches from dark to light text on a fill
// the diagram sets itself: #1f1f1f and white have the same contrast with it.
var fillFlip = color.NRGBA{0x7e, 0x7e, 0x7e, 0xff}

// fillFloor is the contrast #1f1f1f and white have with fillFlip, the least
// that pele's own text colours give on any fill.
const fillFloor = 4.0

// onFills is the text on fills a diagram sets itself, in the palette's own
// colours: the darker of bg and fg on a light fill, the lighter on a dark one.
// Each moves toward black or white until it reaches fillFloor against
// fillFlip, so it reads on every fill that pele gives it.
func onFills(bg, fg color.Color) (onLight, onDark color.Color) {
	dark, light := bg, fg
	if overlay.ContrastRatio(bg, color.Black) > overlay.ContrastRatio(fg, color.Black) {
		dark, light = fg, bg
	}
	return toward(dark, color.Black), toward(light, color.White)
}

// toward moves c toward end, black for text on light fills and white for
// text on dark ones, until it reaches fillFloor against fillFlip.
func toward(c, end color.Color) color.Color {
	for t := 0.0; t < 1; t += 0.05 {
		if k := overlay.MixColors(c, end, t); overlay.ContrastRatio(k, fillFlip) >= fillFloor {
			return k
		}
	}
	return end
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

// Kitty writes a PNG as a kitty graphics image in a box of cols by rows
// cells, and moves the cursor to the line under it.
//
// Both sides of the box are given. With the width alone the terminal works out
// the height from its own cell size, which is not the one the picture was
// sized for when the pane reports another (a tuios pane on a daemon that a
// client with another font set up): the picture then came out taller than the
// screen, its top scrolled away, and the next diagram was drawn into it. A
// fixed box takes exactly rows lines in every terminal.
func Kitty(w io.Writer, png []byte, cols, rows int) error {
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
			b.WriteString("a=T,f=100,q=2,c=")
			b.WriteString(strconv.Itoa(cols))
			if rows > 0 {
				b.WriteString(",r=")
				b.WriteString(strconv.Itoa(rows))
			}
			b.WriteString(",")
		}
		b.WriteString("m=")
		b.WriteString(strconv.Itoa(more))
		b.WriteString(";")
		b.WriteString(enc[i:end])
		b.WriteString("\x1b\\")
	}
	b.WriteString("\r\n")
	_, err := w.Write(b.Bytes())
	return err
}
