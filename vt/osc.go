package vt

import (
	"bytes"
	"encoding/base64"
	"image/color"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func (e *Emulator) handleOsc(cmd int, data []byte) {
	if e.handlers.handleOsc(cmd, data) {
		return
	}
	if e.cb.PrivateOSC != nil && privateOSCNumber(cmd, data) {
		e.cb.PrivateOSC(cmd, data)
		return
	}
	e.logf("unhandled sequence: OSC %q", data)
}

func sanitiseTitle(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	out.Grow(len(b))
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r != utf8.RuneError || size > 1 {
			out.WriteRune(r)
		}
		b = b[size:]
	}
	return out.String()
}

func (e *Emulator) handleTitle(cmd int, data []byte) {
	parts := bytes.SplitN(data, []byte{';'}, 2)
	if len(parts) != 2 {
		return
	}
	switch cmd {
	case 0:
		name := sanitiseTitle(parts[1])
		e.iconName, e.title = name, name
		if e.cb.Title != nil {
			e.cb.Title(name)
		}
		if e.cb.IconName != nil {
			e.cb.IconName(name)
		}
	case 1:
		name := sanitiseTitle(parts[1])
		e.iconName, e.explicitIcon = name, name
		if e.cb.IconName != nil {
			e.cb.IconName(name)
		}
		if e.cb.ExplicitIconName != nil {
			e.cb.ExplicitIconName(name)
		}
	case 2:
		name := sanitiseTitle(parts[1])
		e.title = name
		if e.cb.Title != nil {
			e.cb.Title(name)
		}
	}
}

func oscReplyEnd(bel bool) string {
	if bel {
		return "\x07"
	}
	return "\x1b\\"
}

func dynamicColorItems(cmd int, parts [][]byte, f func(cmd int, arg string)) {
	for i, p := range parts[1:] {
		if cmd+i > 12 {
			return
		}
		f(cmd+i, string(p))
	}
}

func (e *Emulator) handleDefaultColor(cmd int, data []byte) {
	if cmd != 10 && cmd != 11 && cmd != 12 &&
		cmd != 110 && cmd != 111 && cmd != 112 {
		return
	}

	set := func(cmd int, c color.Color) {
		switch cmd {
		case 10, 110:
			e.guestFg = c != nil
			e.SetForegroundColor(c)
		case 11, 111:
			e.guestBg = c != nil
			e.SetBackgroundColor(c)
		case 12, 112:
			e.guestCur = c != nil
			e.SetCursorColor(c)
		}
	}

	parts := bytes.Split(data, []byte{';'})
	if cmd >= 110 || len(parts) < 2 {
		set(cmd, nil)
		return
	}

	dynamicColorItems(cmd, parts, func(cmd int, arg string) {
		if arg != "?" {
			if c := parseGuestColor(arg); c != nil {
				set(cmd, c)
			}
			return
		}
		var xrgb ansi.XRGBColor
		switch cmd {
		case 10:
			xrgb.Color = e.reportedForeground()
		case 11:
			xrgb.Color = e.reportedBackground()
		case 12:
			xrgb.Color = e.CursorColor()
		}
		if xrgb.Color != nil {
			_, _ = io.WriteString(e.pipe, "\x1b]"+strconv.Itoa(cmd)+";"+xrgb.String()+oscReplyEnd(e.parser.oscBEL))
		}
	})
}

func (e *Emulator) handleWorkingDirectory(cmd int, data []byte) {
	if cmd != 7 {
		return
	}

	parts := bytes.Split(data, []byte{';'})
	if len(parts) != 2 {
		return
	}

	path := string(parts[1])
	e.cwd = path

	if e.cb.WorkingDirectory != nil {
		e.cb.WorkingDirectory(path)
	}
}

func (e *Emulator) handleSemanticZone(data []byte) {
	parts := bytes.Split(data, []byte{';'})
	if len(parts) < 2 || len(parts[1]) == 0 {
		return
	}

	subCmd := parts[1][0]
	switch subCmd {
	case 'A', 'B', 'C', 'D':
	default:
		return
	}

	curX, curY := e.scr.CursorPosition()
	absLine := e.ScrollbackLen() + curY

	exitCode := -1
	if subCmd == 'D' && len(parts) >= 3 {
		code := 0
		for _, b := range parts[2] {
			if b >= '0' && b <= '9' {
				code = code*10 + int(b-'0')
			}
		}
		if len(parts[2]) > 0 {
			exitCode = code
		}
	}

	marker := SemanticMarker{
		Type:     SemanticMarkerType(subCmd),
		AbsLine:  absLine,
		Col:      curX,
		ExitCode: exitCode,
	}
	if e.semanticMarkers != nil {
		if subCmd == 'C' {
			marker.CapturedText = captureCommandText(e.semanticMarkers, e, absLine)
		}

		e.semanticMarkers.Add(marker)
	}
	if e.cb.SemanticMark != nil {
		e.cb.SemanticMark(marker)
	}
}

func (e *Emulator) handleTextSizing(data []byte) {
	parts := bytes.SplitN(data, []byte{';'}, 3)
	if len(parts) < 3 {
		return
	}
	text := parts[2]
	if len(text) == 0 {
		return
	}

	scale := 1
	for kv := range bytes.SplitSeq(parts[1], []byte{':'}) {
		if bytes.HasPrefix(kv, []byte("s=")) && len(kv) > 2 {
			if s := kv[2] - '0'; s >= 1 && s <= 7 {
				scale = int(s)
			}
		}
	}

	textRunes := len([]rune(string(text)))
	curX, curY := e.scr.CursorPosition()

	if e.textSizingFunc != nil {
		var rawOSC []byte
		rawOSC = append(rawOSC, "\x1b]"...)
		rawOSC = append(rawOSC, StripControls(string(data))...)
		rawOSC = append(rawOSC, '\a')
		e.textSizingFunc(rawOSC, curX, curY, scale, textRunes)
	}

	w := e.Width()
	h := e.Height()
	scaledCols := textRunes * scale
	for row := range scale {
		y := curY + row
		if y >= h {
			break
		}
		for x := range w {
			e.scr.SetCell(x, y, nil)
		}
	}
	if curY > 0 && scaledCols < w {
		for x := scaledCols; x < w; x++ {
			e.scr.SetCell(x, curY-1, nil)
		}
	}
}

func (e *Emulator) handlePaletteColor(data []byte) {
	parts := bytes.Split(data, []byte{';'})
	if len(parts) < 3 {
		return
	}

	idx, ok := parsePaletteIndex(parts[1])
	if !ok {
		return
	}

	arg := string(parts[2])
	if arg == "?" {
		c := e.reportedIndexedColor(idx)
		if c != nil {
			var xrgb ansi.XRGBColor
			xrgb.Color = c
			response := "\x1b]4;" + string(parts[1]) + ";" + xrgb.String() + oscReplyEnd(e.parser.oscBEL)
			_, _ = io.WriteString(e.pipe, response)
		}
	} else if c := parseGuestColor(arg); c != nil {
		e.SetIndexedColor(idx, c)
	}
}

func (e *Emulator) handleResetPaletteColor(data []byte) {
	parts := bytes.Split(data, []byte{';'})
	if len(parts) < 2 {
		e.colors = [256]color.Color{}
		e.refreshPaletteClaims()
		return
	}
	for _, p := range parts[1:] {
		if idx, ok := parsePaletteIndex(p); ok {
			e.colors[idx] = nil
		}
	}
	e.refreshPaletteClaims()
}

func parsePaletteIndex(b []byte) (int, bool) {
	if len(b) == 0 || len(b) > 3 {
		return 0, false
	}
	idx := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		idx = idx*10 + int(c-'0')
	}
	if idx > 255 {
		return 0, false
	}
	return idx, true
}

func (e *Emulator) handleClipboard(data []byte) {
	parts := bytes.Split(data, []byte{';'})
	if len(parts) < 3 {
		return
	}

	selection := clipboardSelection(parts[1])
	payload := string(parts[2])

	if payload == "?" {
		if e.cb.ClipboardQuery != nil {
			content := e.cb.ClipboardQuery(selection)
			encoded := base64.StdEncoding.EncodeToString([]byte(content))
			response := "\x1b]52;" + selection + ";" + encoded + oscReplyEnd(e.parser.oscBEL)
			_, _ = io.WriteString(e.pipe, response)
		} else {
			response := "\x1b]52;" + selection + ";" + oscReplyEnd(e.parser.oscBEL)
			_, _ = io.WriteString(e.pipe, response)
		}
	} else {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return
		}
		if e.cb.ClipboardSet != nil {
			e.cb.ClipboardSet(selection, string(decoded))
		}
	}
}

func clipboardSelection(field []byte) string {
	if len(field) == 0 {
		return "s0"
	}
	const names = "cpqs01234567"
	var kept [len(names)]byte
	n := 0
	for _, c := range field {
		if bytes.IndexByte([]byte(names), c) < 0 || bytes.IndexByte(kept[:n], c) >= 0 {
			continue
		}
		kept[n] = c
		n++
	}
	return string(kept[:n])
}

func (e *Emulator) handleHyperlink(cmd int, data []byte) {
	parts := bytes.SplitN(data, []byte{';'}, 3)
	if len(parts) != 3 || cmd != 8 {
		return
	}

	e.scr.cur.Link.Params = LinkParams(string(parts[1]))
	e.scr.cur.Link.URL = LinkURI(string(parts[2]))
	if e.scr.cur.Link.URL == "" {
		e.scr.cur.Link = uv.Link{}
	}
}

// StripControls returns s without C0 controls, DEL, C1 controls (as raw bytes
// or UTF-8 encoded) and bytes that are not valid UTF-8.
//
// A link's address and parameters are written back out to the host terminal
// inside its own OSC 8 when the frame is drawn. The parser already ends the
// sequence at ESC and BEL, but a raw 0x9c is ST to a terminal that reads 8-bit
// controls, and none of these belong in a link.
func StripControls(s string) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c >= 0x7f {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if (r == utf8.RuneError && size == 1) || r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// LinkURI is a link's address as tuios keeps it in a cell and writes it to
// the host: without control bytes, and with every other byte outside printable
// ASCII percent-encoded, which a browser reads as the same address.
//
// The OSC 8 spec allows only printable ASCII in a link's address. A raw UTF-8
// address is not safe even with every control rune stripped: the continuation
// bytes of many runes are 0x80-0x9f, and a parser that reads 8-bit controls
// (charmbracelet/x/ansi, tmux, and the terminals that do the same) takes them
// as C1. 0x9c is ST, so the sequence ends in the middle of the address and the
// rest of it is printed as text. "术" is E6 9C AF. The row is then wider than
// the pane, and everything after it on screen is drawn in the wrong place. The
// cell keeps the encoded form, so every renderer that writes the cell back
// out, and a frame read back in, agree on the address.
func LinkURI(s string) string { return hostLinkURI(StripControls(s)) }

// LinkParams is a link's parameters made safe the same way. A byte outside
// printable ASCII, and ';', which would end them, is percent-encoded rather
// than dropped, so id=链 and id=接 stay two ids.
func LinkParams(s string) string { return hostLinkParams(StripControls(s)) }

// MaxHostLinkURI is the longest address written to the host in an OSC 8, in
// bytes as written. It is VTE's limit, and the one the scrollback ring keeps
// links to. A longer link is drawn as plain text: the frame writes the address
// again each time the link starts on a row, and an OSC payload can be 4 MiB.
const MaxHostLinkURI = 2083

// SetHostHyperlink returns the OSC 8 that opens a link in a frame written to
// the host, with LinkURI and LinkParams applied, or "" for an address longer
// than MaxHostLinkURI. A cell the vt emulator filled is already encoded. A
// bare URL tuios finds in the text, and a cell from another backend, may not
// be. It is for a host that writes the cells to its own terminal.
func SetHostHyperlink(url, params string) string {
	uri := LinkURI(url)
	if len(uri) > MaxHostLinkURI {
		return ""
	}
	return ansi.SetHyperlink(uri, LinkParams(params))
}

// LinkURIDecodedLen is the length in bytes of a LinkURI address with its
// percent escapes decoded: the length of the address the program wrote.
func LinkURIDecodedLen(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHexByte(s[i+1]) && isHexByte(s[i+2]) {
			i += 2
		}
		n++
	}
	return n
}

func isHexByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hostLinkByte(b byte) bool { return b > 0x20 && b < 0x7f }

func hostLinkURI(s string) string { return percentEncode(s, hostLinkByte) }

func percentEncode(s string, keep func(byte) bool) string {
	n := 0
	for i := 0; i < len(s); i++ {
		if !keep(s[i]) {
			n++
		}
	}
	if n == 0 {
		return s
	}
	const hex = "0123456789ABCDEF"
	b := make([]byte, 0, len(s)+2*n)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if keep(c) {
			b = append(b, c)
			continue
		}
		b = append(b, '%', hex[c>>4], hex[c&0x0f])
	}
	return string(b)
}

func hostLinkParams(s string) string {
	return percentEncode(s, func(c byte) bool { return hostLinkByte(c) && c != ';' })
}

func (e *Emulator) handleNotify9(data []byte) bool {
	parts := strings.SplitN(string(data), ";", 2)
	if len(parts) < 2 {
		return true
	}
	msg := parts[1]
	if path, ok := strings.CutPrefix(msg, "9;"); ok {
		e.cwd = path
		if e.cb.WorkingDirectory != nil {
			e.cb.WorkingDirectory(path)
		}
		return true
	}
	if isProgressPayload(msg) {
		if state, percent, ok := parseProgress(msg); ok && e.cb.Progress != nil {
			e.cb.Progress(state, percent)
		}
		return true
	}
	if e.cb.Notify != nil {
		e.cb.Notify("", msg)
	}
	return true
}

func (e *Emulator) handleNotify777(data []byte) bool {
	parts := strings.SplitN(string(data), ";", 4)
	if len(parts) < 4 || parts[1] != "notify" {
		return true
	}
	if e.cb.Notify != nil {
		e.cb.Notify(parts[2], parts[3])
	}
	return true
}

func (e *Emulator) handleNotify99(data []byte) bool {
	if title, body, ok := parseNotify99(data); ok && e.cb.Notify != nil {
		e.cb.Notify(title, body)
	}
	return true
}

func parseNotify99(data []byte) (title, body string, ok bool) {
	parts := strings.SplitN(string(data), ";", 3)
	meta := map[string]string{}
	if len(parts) >= 2 {
		for kv := range strings.SplitSeq(parts[1], ":") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				meta[k] = v
			}
		}
	}
	if meta["d"] == "0" {
		return "", "", false
	}
	payload := ""
	if len(parts) >= 3 {
		payload = parts[2]
	}
	if meta["e"] == "1" {
		if decoded, err := base64.StdEncoding.DecodeString(payload); err == nil {
			payload = string(decoded)
		}
	}
	if meta["p"] == "title" {
		return payload, "", true
	}
	return "", payload, true
}
