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

// handleOsc handles an OSC escape sequence.
func (e *Emulator) handleOsc(cmd int, data []byte) {
	// No grapheme flush: an OSC neither prints nor moves the cursor, so the
	// cluster in flight stays open across it, as ghostty keeps it.
	if !e.handlers.handleOsc(cmd, data) {
		e.logf("unhandled sequence: OSC %q", data)
	}
}

// sanitiseTitle drops anything from a title that is not valid UTF-8.
//
// A title is chrome: it is drawn in a rail row, a window frame and a session
// listing, and it is serialised into JSON. An invalid byte survives all of
// that as a replacement character, which draws as a tofu box and marshals as
// U+FFFD, so one bad byte from a guest turns into a black diamond in three
// places at once.
//
// Dropped rather than replaced. There is nothing useful to put in its place,
// and a title that is one character shorter is better than a title with a box
// in it.
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
	// Split on the first ';' only; titles may legitimately contain semicolons.
	parts := bytes.SplitN(data, []byte{';'}, 2)
	if len(parts) != 2 {
		// Invalid, ignore
		return
	}
	switch cmd {
	case 0: // Set window title and icon name
		name := sanitiseTitle(parts[1])
		e.iconName, e.title = name, name
		if e.cb.Title != nil {
			e.cb.Title(name)
		}
		if e.cb.IconName != nil {
			e.cb.IconName(name)
		}
	case 1: // Set icon name
		name := sanitiseTitle(parts[1])
		e.iconName = name
		if e.cb.IconName != nil {
			e.cb.IconName(name)
		}
	case 2: // Set window title
		name := sanitiseTitle(parts[1])
		e.title = name
		if e.cb.Title != nil {
			e.cb.Title(name)
		}
	}
}

// oscReplyEnd is the terminator for a reply to an OSC query: BEL when the
// query ended with BEL and ST otherwise. xterm answers in the form it was
// asked, and a guest that reads up to the terminator it sent waits out its
// timeout on the other one.
func oscReplyEnd(bel bool) string {
	if bel {
		return "\x07"
	}
	return "\x1b\\"
}

// dynamicColorItems walks the items of an OSC 10, 11 or 12. Each item after
// the first applies to the next colour in the sequence, as in xterm: OSC 10 ;
// ? ; ? asks for the foreground and then the background. Colours past 12 are
// not implemented here, so items that would reach them are dropped.
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
		// Invalid, ignore
		return
	}

	set := func(cmd int, c color.Color) {
		switch cmd {
		case 10, 110: // Foreground color
			e.guestFg = c != nil
			e.SetForegroundColor(c)
		case 11, 111: // Background color
			e.guestBg = c != nil
			e.SetBackgroundColor(c)
		case 12, 112: // Cursor color
			e.SetCursorColor(c)
		}
	}

	parts := bytes.Split(data, []byte{';'})
	if cmd >= 110 || len(parts) < 2 {
		// Reset color
		set(cmd, nil)
		return
	}

	dynamicColorItems(cmd, parts, func(cmd int, arg string) {
		if arg != "?" {
			if c := ansi.XParseColor(arg); c != nil {
				set(cmd, c)
			}
			return
		}
		var xrgb ansi.XRGBColor
		switch cmd {
		case 10: // Query foreground color
			xrgb.Color = e.reportedForeground()
		case 11: // Query background color
			xrgb.Color = e.reportedBackground()
		case 12: // Query cursor color
			xrgb.Color = e.CursorColor()
		}
		if xrgb.Color != nil {
			_, _ = io.WriteString(e.pipe, "\x1b]"+strconv.Itoa(cmd)+";"+xrgb.String()+oscReplyEnd(e.parser.oscBEL))
		}
	})
}

func (e *Emulator) handleWorkingDirectory(cmd int, data []byte) {
	if cmd != 7 {
		// Invalid, ignore
		return
	}

	// The data is the working directory path.
	parts := bytes.Split(data, []byte{';'})
	if len(parts) != 2 {
		// Invalid, ignore
		return
	}

	path := string(parts[1])
	e.cwd = path

	if e.cb.WorkingDirectory != nil {
		e.cb.WorkingDirectory(path)
	}
}

func (e *Emulator) handleSemanticZone(data []byte) {
	// OSC 133 format: "133;<subcommand>[;params]"
	// data includes the "133;" prefix from the parser
	parts := bytes.Split(data, []byte{';'})
	if len(parts) < 2 || len(parts[1]) == 0 {
		return
	}

	subCmd := parts[1][0] // 'A', 'B', 'C', or 'D'
	switch subCmd {
	case 'A', 'B', 'C', 'D':
		// valid
	default:
		return
	}

	curX, curY := e.scr.CursorPosition()
	absLine := e.ScrollbackLen() + curY

	exitCode := -1
	if subCmd == 'D' && len(parts) >= 3 {
		// Parse exit code from params (e.g., "D;0" or "D;1")
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
		// On C marker (command executed), capture the command text from the
		// terminal buffer before the program's output overwrites it.
		// This is the only reliable time to read the command text.
		if subCmd == 'C' {
			if bMarker := e.semanticMarkers.Last(MarkerCommandStart); bMarker != nil {
				marker.CapturedText = e.extractCommandText(bMarker.AbsLine, bMarker.Col, absLine, curX)
			}
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

	// Parse scale
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

	// Forward to host terminal
	if e.textSizingFunc != nil {
		var rawOSC []byte
		rawOSC = append(rawOSC, "\x1b]"...)
		// The sequence is replayed to the host terminal as is, so it is
		// rebuilt without the control characters the parser let through.
		rawOSC = append(rawOSC, StripControls(string(data))...)
		rawOSC = append(rawOSC, '\a')
		e.textSizingFunc(rawOSC, curX, curY, scale, textRunes)
	}

	// Clear rows occupied by the scaled text so bubbletea renders spaces,
	// allowing our post-render OSC 66 passthrough to persist.
	// Also clear columns beyond the scaled text on the row above (curY-1)
	// to remove wrapped command text like "ext\a\n\n"".
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
	// Clear only columns beyond scaledCols on the row above (command text wrap area)
	if curY > 0 && scaledCols < w {
		for x := scaledCols; x < w; x++ {
			e.scr.SetCell(x, curY-1, nil)
		}
	}
}

func (e *Emulator) handlePaletteColor(data []byte) {
	// OSC 4 format: "4;<index>;<spec>" where spec can be "?" for query or an X11 color
	parts := bytes.Split(data, []byte{';'})
	if len(parts) < 3 {
		return
	}

	// Parse color index. A malformed one is dropped rather than rounded down
	// to slot 0, which would have the guest repaint black by accident.
	idx, ok := parsePaletteIndex(parts[1])
	if !ok {
		return
	}

	arg := string(parts[2])
	if arg == "?" {
		// Query: respond with current color
		c := e.reportedIndexedColor(idx)
		if c != nil {
			var xrgb ansi.XRGBColor
			xrgb.Color = c
			response := "\x1b]4;" + string(parts[1]) + ";" + xrgb.String() + oscReplyEnd(e.parser.oscBEL)
			_, _ = io.WriteString(e.pipe, response)
		}
	} else if c := ansi.XParseColor(arg); c != nil {
		// Set: update the palette entry
		e.SetIndexedColor(idx, c)
	}
}

// handleResetPaletteColor handles OSC 104, which puts palette slots back to
// what they were before the guest touched them. Bare OSC 104 resets all of
// them.
//
// What "before" means is the user's terminal, or the user's tuios theme when
// one is active, so the reset clears the guest layer and leaves the theme
// layer standing.
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

// parsePaletteIndex reads a palette index, rejecting anything that is not a
// plain number in range rather than silently landing on slot 0.
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
	// OSC 52 format: "52;<selection>;<base64-data>"
	// selection: c = clipboard, p = primary, s = secondary, etc.
	// base64-data: "?" to query, base64-encoded string to set
	parts := bytes.Split(data, []byte{';'})
	if len(parts) < 3 {
		return
	}

	selection := string(parts[1])
	payload := string(parts[2])

	if payload == "?" {
		// Query clipboard
		if e.cb.ClipboardQuery != nil {
			content := e.cb.ClipboardQuery(selection)
			encoded := base64.StdEncoding.EncodeToString([]byte(content))
			response := "\x1b]52;" + selection + ";" + encoded + oscReplyEnd(e.parser.oscBEL)
			_, _ = io.WriteString(e.pipe, response)
		} else {
			// No callback: respond empty
			response := "\x1b]52;" + selection + ";" + oscReplyEnd(e.parser.oscBEL)
			_, _ = io.WriteString(e.pipe, response)
		}
	} else {
		// Set clipboard
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return
		}
		if e.cb.ClipboardSet != nil {
			e.cb.ClipboardSet(selection, string(decoded))
		}
	}
}

// handleHyperlink handles OSC 8: "8;<params>;<uri>".
//
// The parameters come first and the URI second, which is the opposite of what
// this used to store: every hyperlink came out with its parameters in the URL
// field, so a plain link, which has no parameters, got an empty URL and a link
// carrying an id got the id as its address.
//
// The split is limited to three parts because a URI may contain semicolons, in
// a query string or a matrix parameter. Splitting on all of them made any such
// link parse to four parts and be dropped.
func (e *Emulator) handleHyperlink(cmd int, data []byte) {
	parts := bytes.SplitN(data, []byte{';'}, 3)
	if len(parts) != 3 || cmd != 8 {
		// Invalid, ignore
		return
	}

	e.scr.cur.Link.Params = StripControls(string(parts[1]))
	e.scr.cur.Link.URL = StripControls(string(parts[2]))
	// An empty URI closes the link, whatever parameters came with it. Keeping
	// the parameters would leave every following cell carrying a link value
	// with no address, which no reader of a cell expects.
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

// handleNotify9 handles OSC 9 (iTerm2 desktop notification): "9;<msg>".
func (e *Emulator) handleNotify9(data []byte) bool {
	parts := strings.SplitN(string(data), ";", 2)
	if len(parts) < 2 {
		return true
	}
	msg := parts[1]
	// OSC 9;4 is the ConEmu progress-report sequence, not a notification: the
	// program is describing its own progress rather than asking for a desktop
	// alert, so it goes to the progress callback and never to Notify.
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

// handleNotify777 handles OSC 777 (urxvt desktop notification):
// "777;notify;<title>;<body>".
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

// handleNotify99 handles OSC 99 (kitty desktop notification).
func (e *Emulator) handleNotify99(data []byte) bool {
	if title, body, ok := parseNotify99(data); ok && e.cb.Notify != nil {
		e.cb.Notify(title, body)
	}
	return true
}

// parseNotify99 parses an OSC 99 (kitty desktop notification) payload:
// "99;<metadata>;<payload>". Metadata is a colon-separated list of key=val
// pairs. This is a best-effort v1 parse: e=1 base64-decodes the payload,
// p=title routes the payload as the title, and d=0 continuation chunks are
// ignored rather than accumulated. ok is false for a chunk that raises no
// notification. Both backends share it, because libghostty has no OSC 99.
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
	// d=0 signals more chunks follow; skip continuation chunks best-effort.
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
