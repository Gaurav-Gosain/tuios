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

type browserPalette struct {
	ansi   [16]uint32
	mask   uint16
	fg, bg uint32
	hasFg  bool
	hasBg  bool
}

var webPalette browserPalette

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

func (p browserPalette) applyTo(caps *app.HostCapabilities) {
	caps.ANSI = p.ansi
	caps.ANSIMask = p.mask
	caps.Fg, caps.HasFg = p.fg, p.hasFg
	caps.Bg, caps.HasBg = p.bg, p.hasBg
}

func sipDefaultTheme() sip.Theme {
	src, err := fs.ReadFile(sip.Assets(), "terminal.js")
	if err != nil {
		return sip.Theme{}
	}
	return parseSipTheme(string(src))
}

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

func overlayTheme(base, patch sip.Theme) sip.Theme {
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
