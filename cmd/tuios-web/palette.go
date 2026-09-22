package main

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// browserPalette is the colours the browser terminal resolves palette indices
// to, packed the way app.HostCapabilities carries a host's answer.
//
// A local client asks its terminal for these with OSC 4/10/11. A browser cannot
// be asked, but it does not need to be: it draws with sip's built-in palette
// patched by the theme this server sends, and both are known here. Without it
// a capture of an unthemed web session guessed with the xterm defaults while
// the browser showed sip's colours.
type browserPalette struct {
	ansi   [16]uint32
	mask   uint16
	fg, bg uint32
	hasFg  bool
	hasBg  bool
}

// webPalette is the palette every web connection reports. It is set once in
// runWebServer, after the theme is settled, and only read after that.
var webPalette browserPalette

// newBrowserPalette is sip's own palette with th laid over it.
//
// A slot neither names, or names in a form that does not parse, is left
// unanswered, so the capture keeps its fallback there instead of black.
func newBrowserPalette(th sip.Theme) browserPalette {
	eff := overlayTheme(sipDefaultTheme(), th)
	var p browserPalette
	for i, c := range eff.ANSI() {
		if v, ok := packHex(c); ok {
			p.ansi[i] = v
			p.mask |= 1 << uint(i)
		}
	}
	p.fg, p.hasFg = packHex(eff.Foreground)
	p.bg, p.hasBg = packHex(eff.Background)
	return p
}

// applyTo writes the palette into a capability set.
func (p browserPalette) applyTo(caps *app.HostCapabilities) {
	caps.ANSI = p.ansi
	caps.ANSIMask = p.mask
	caps.Fg, caps.HasFg = p.fg, p.hasFg
	caps.Bg, caps.HasBg = p.bg, p.hasBg
}

// sipDefaultTheme reads the palette sip's page draws with when it is sent none,
// out of the terminal.js sip serves. sip keeps that palette in the script and
// not in Go, and reading the served file means a sip upgrade that changes it
// is followed here without a copy to keep in step.
//
// A file that cannot be read or parsed answers the zero Theme, which leaves
// every slot unanswered: the behaviour from before this existed.
func sipDefaultTheme() sip.Theme {
	src, err := fs.ReadFile(sip.Assets(), "terminal.js")
	if err != nil {
		return sip.Theme{}
	}
	return parseSipTheme(string(src))
}

// parseSipTheme reads the `const THEME = { key: '#hex', ... };` block out of
// sip's script. The keys are xterm.js theme names, which are also sip.Theme's
// JSON tags, so the block decodes straight into the struct.
func parseSipTheme(src string) sip.Theme {
	_, block, found := strings.Cut(src, "const THEME = {")
	if !found {
		return sip.Theme{}
	}
	block, _, found = strings.Cut(block, "};")
	if !found {
		return sip.Theme{}
	}
	fields := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(block))
	for sc.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(sc.Text()), ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), ",'\"")
		fields[strings.TrimSpace(key)] = value
	}
	var th sip.Theme
	raw, err := json.Marshal(fields)
	if err != nil {
		return sip.Theme{}
	}
	if err := json.Unmarshal(raw, &th); err != nil {
		return sip.Theme{}
	}
	return th
}

// overlayTheme lays patch over base the way the page does: every colour patch
// sets wins, and every one it leaves empty keeps base's.
func overlayTheme(base, patch sip.Theme) sip.Theme {
	// sip.Theme's JSON omits empty colours, so decoding patch onto base writes
	// exactly the fields patch sets.
	raw, err := json.Marshal(patch)
	if err != nil {
		return base
	}
	out := base
	if err := json.Unmarshal(raw, &out); err != nil {
		return base
	}
	return out
}

// packHex turns a CSS hex colour (#rgb, #rgba, #rrggbb or #rrggbbaa) into
// 0xRRGGBB. Alpha is dropped. An empty or malformed colour reports false.
func packHex(c sip.Color) (uint32, bool) {
	d, ok := strings.CutPrefix(string(c), "#")
	if !ok {
		return 0, false
	}
	switch len(d) {
	case 3, 4:
		d = string([]byte{d[0], d[0], d[1], d[1], d[2], d[2]})
	case 6, 8:
		d = d[:6]
	default:
		return 0, false
	}
	v, err := strconv.ParseUint(d, 16, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}
