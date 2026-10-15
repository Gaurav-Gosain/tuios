// Package teaswatch connects swatch to Bubble Tea v2.
//
// Listen turns a swatch.Watcher into a subscription: each new palette arrives
// in Update as a ChangedMsg. Terminal follows the palette of the terminal the
// program runs in, from its answers to OSC 10, 11 and 4 and its light and
// dark reports (mode 2031).
package teaswatch

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"tuios.dev/swatch"
)

// ChangedMsg carries a new palette.
type ChangedMsg struct{ Palette swatch.Palette }

// Listen waits for the watcher's next palette. Return it from Init, and again
// from Update after each ChangedMsg. It returns nil, and the loop ends, once
// the watcher is closed.
func Listen(w *swatch.Watcher) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-w.Changes()
		if !ok {
			return nil
		}
		return ChangedMsg{Palette: p}
	}
}

// Terminal follows the terminal's own palette. Send Init's command from your
// Init, pass every message to Update, and send Restore's command before the
// program exits.
//
// The zero value is ready to use. A terminal that answers no colour question
// leaves Terminal with nothing, and Update never reports a palette.
type Terminal struct {
	fg, bg color.Color
	ansi   [16]color.Color
}

// TerminalSource is the Source field of the palettes Terminal builds.
const TerminalSource = "terminal"

// paletteQuery asks for the sixteen ANSI colours.
var paletteQuery = func() string {
	var b strings.Builder
	for i := range 16 {
		fmt.Fprintf(&b, "\x1b]4;%d;?\x1b\\", i)
	}
	return b.String()
}()

// Init asks the terminal for its colours and turns on its light and dark
// reports.
func (t *Terminal) Init() tea.Cmd {
	return tea.Batch(t.query(), tea.Raw("\x1b[?2031h"))
}

// Restore turns the light and dark reports off again.
func (t *Terminal) Restore() tea.Cmd { return tea.Raw("\x1b[?2031l") }

func (t *Terminal) query() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, tea.RequestForegroundColor, tea.Raw(paletteQuery))
}

// Update takes a message. It returns the terminal's palette and true when the
// message changed it, and a command to run. A light or dark report asks for
// the colours again, because the report itself names no colour.
func (t *Terminal) Update(msg tea.Msg) (swatch.Palette, bool, tea.Cmd) {
	changed := false
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		changed = set(&t.bg, msg.Color)
	case tea.ForegroundColorMsg:
		changed = set(&t.fg, msg.Color)
	case uv.UnknownOscEvent:
		if i, c, ok := ParsePaletteReply(string(msg)); ok {
			changed = set(&t.ansi[i], c)
		}
	case uv.LightColorSchemeEvent, uv.DarkColorSchemeEvent:
		return swatch.Palette{}, false, t.query()
	}
	if !changed || t.bg == nil {
		return swatch.Palette{}, false, nil
	}
	return t.Palette(), true, nil
}

func set(dst *color.Color, c color.Color) bool {
	if c == nil {
		return false
	}
	if *dst != nil && swatch.Hex(*dst) == swatch.Hex(c) {
		return false
	}
	*dst = c
	return true
}

// Palette is what the terminal said so far, with the roles filled from it:
// the accent and the info colour from blue, the status colours from red,
// green and yellow, and a slot not answered from the xterm default.
func (t *Terminal) Palette() swatch.Palette {
	p := swatch.Palette{ID: TerminalSource, Name: "Terminal", Source: TerminalSource}
	p.Background, p.Foreground = t.bg, t.fg
	if p.Background == nil {
		p.Background = color.RGBA{A: 0xff}
	}
	p.Light = luminance(p.Background) > 0.5
	if p.Foreground == nil {
		p.Foreground = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
		if p.Light {
			p.Foreground = color.RGBA{A: 0xff}
		}
	}
	for i := range p.ANSI {
		p.ANSI[i] = t.ansi[i]
		if p.ANSI[i] == nil {
			p.ANSI[i] = xterm[i]
		}
	}
	p.Cursor, p.SelectionForeground = p.Foreground, p.Foreground
	p.SelectionBackground, p.Muted = p.ANSI[8], p.ANSI[8]
	p.Surface = p.Background
	p.Error, p.Success, p.Warning, p.Info = p.ANSI[1], p.ANSI[2], p.ANSI[3], p.ANSI[4]
	p.Accent, p.Border = p.ANSI[4], p.ANSI[4]
	return p
}

// luminance is a colour's relative luminance on 0 to 1, by the sRGB weights
// without linearisation, which is enough to tell light from dark.
func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 0xffff
}

var xterm = func() [16]color.Color {
	hex := [16]string{
		"#000000", "#cd0000", "#00cd00", "#cdcd00", "#0000ee", "#cd00cd", "#00cdcd", "#e5e5e5",
		"#7f7f7f", "#ff0000", "#00ff00", "#ffff00", "#5c5cff", "#ff00ff", "#00ffff", "#ffffff",
	}
	var out [16]color.Color
	for i, h := range hex {
		out[i], _ = swatch.ParseHex(h)
	}
	return out
}()

// ParsePaletteReply reads a terminal's answer to an OSC 4 query for one of
// the sixteen ANSI colours: OSC 4 ; index ; rgb:R/G/B, each channel one to
// four hex digits, ended by BEL or ST. The leading OSC and the end are
// optional.
func ParsePaletteReply(s string) (int, color.Color, bool) {
	s = strings.TrimPrefix(s, "\x1b]")
	s = strings.TrimSuffix(strings.TrimSuffix(s, "\x07"), "\x1b\\")
	rest, ok := strings.CutPrefix(s, "4;")
	if !ok {
		return 0, nil, false
	}
	num, spec, ok := strings.Cut(rest, ";")
	if !ok || len(num) == 0 || len(num) > 2 {
		return 0, nil, false
	}
	idx, err := strconv.Atoi(num)
	if err != nil || idx < 0 || idx > 15 {
		return 0, nil, false
	}
	spec, ok = strings.CutPrefix(spec, "rgb:")
	if !ok {
		return 0, nil, false
	}
	parts := strings.Split(spec, "/")
	if len(parts) != 3 {
		return 0, nil, false
	}
	var ch [3]uint8
	for i, part := range parts {
		if len(part) == 0 || len(part) > 4 {
			return 0, nil, false
		}
		v, err := strconv.ParseUint(part, 16, 16)
		if err != nil {
			return 0, nil, false
		}
		maxV := uint64(1)<<(4*len(part)) - 1
		ch[i] = uint8((v*255 + maxV/2) / maxV)
	}
	return idx, color.RGBA{R: ch[0], G: ch[1], B: ch[2], A: 0xff}, true
}
