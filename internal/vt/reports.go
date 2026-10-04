package vt

import (
	"encoding/hex"
	"image/color"
	"io"
	"strconv"
	"strings"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// buildVersion is the version XTVERSION reports. cmd/tuios sets it from the
// release ldflags at startup, because this package cannot read main's
// variables. It stays empty in tests and in programs that embed the emulator.
var buildVersion atomic.Pointer[string]

// SetBuildVersion records the version XTVERSION reports. Call it once, before
// the first pane starts.
func SetBuildVersion(v string) {
	v = strings.TrimSpace(v)
	buildVersion.Store(&v)
}

// XTVersionName is the text an XTVERSION reply carries: "tuios", followed by
// the build version when one was set. Both backends answer with it, so a guest
// that picks its features by terminal name sees the same name on either.
func XTVersionName() string {
	v := buildVersion.Load()
	if v == nil || *v == "" {
		return "tuios"
	}
	return "tuios " + *v
}

// unitID is the DA3 terminal unit identifier: eight hex digits a guest can use
// to tell one terminal from another. Nothing here has a stable identity to
// report, so it is zero, which is what xterm sends and what libghostty sends
// for a terminal that sets none.
const unitID = "00000000"

// registerReportHandlers registers the identification and capability queries:
// XTVERSION, DA3 and XTGETTCAP.
func (e *Emulator) registerReportHandlers() {
	e.RegisterCsiHandler(ansi.Command('>', 0, 'q'), func(params ansi.Params) bool {
		// XTVERSION, "CSI > Ps q". Only Ps 0 (or none) is defined.
		if n, _, _ := params.Param(0, 0); n != 0 {
			return false
		}
		_, _ = io.WriteString(e.pipe, "\x1bP>|"+XTVersionName()+"\x1b\\")
		return true
	})

	e.RegisterCsiHandler(ansi.Command('=', 0, 'c'), func(params ansi.Params) bool {
		// Tertiary Device Attributes [ansi.DA3], "CSI = c" or "CSI = 0 c".
		if n, _, _ := params.Param(0, 0); n != 0 {
			return false
		}
		_, _ = io.WriteString(e.pipe, "\x1bP!|"+unitID+"\x1b\\")
		return true
	})

	e.RegisterDcsHandler(ansi.Command(0, '+', 'q'), func(_ ansi.Params, data []byte) bool {
		// XTGETTCAP, "DCS + q Pt ST".
		_, _ = io.WriteString(e.pipe, termcapReply(string(data)))
		return true
	})
}

// termcapValue is one capability XTGETTCAP answers. A boolean capability has
// no value and is answered with its name alone.
type termcapValue struct {
	value   string
	boolean bool
}

// termcaps are the capabilities XTGETTCAP answers, by terminfo name (and the
// termcap name where a guest is known to ask for that). Each is something this
// emulator implements. The values are the strings a guest would send, with the
// real control bytes, which is what xterm returns.
//
// TN is the entry a pane is given when its own terminal has none on this
// machine (internal/guestenv.FallbackTerm), so it names a database entry the
// guest can expect to find.
var termcaps = map[string]termcapValue{
	"TN":     {value: "xterm-256color"},
	"colors": {value: "256"},
	"Co":     {value: "256"},
	// Direct colour. Tc is tmux's boolean and RGB is the ncurses one. Both
	// are what neovim and tmux probe before they send 38;2.
	"RGB":     {boolean: true},
	"Tc":      {boolean: true},
	"setrgbf": {value: "\x1b[38;2;%p1%d;%p2%d;%p3%dm"},
	"setrgbb": {value: "\x1b[48;2;%p1%d;%p2%d;%p3%dm"},
	// Styled underlines and their colour.
	"Su":     {boolean: true},
	"Smulx":  {value: "\x1b[4:%p1%dm"},
	"Setulc": {value: "\x1b[58:2:%p1%{65536}%/%d:%p1%{256}%/%{255}%&%d:%p1%{255}%&%d%;m"},
	// OSC 52 clipboard writes.
	"Ms": {value: "\x1b]52;%p1%s;%p2%s\x07"},
	// DECSCUSR: set the cursor shape, and reset it to the steady block a
	// pane starts with.
	"Ss": {value: "\x1b[%p1%d q"},
	"Se": {value: "\x1b[2 q"},
	// OSC 12 and OSC 112: the cursor colour.
	"Cs": {value: "\x1b]12;%p1%s\x07"},
	"Cr": {value: "\x1b]112\x07"},
	// Synchronized output, mode 2026.
	"Sync": {value: "\x1b[?2026%?%p1%{1}%-%tl%eh%;"},
	// Bracketed paste, mode 2004.
	"BE": {value: "\x1b[?2004h"},
	"BD": {value: "\x1b[?2004l"},
	"PS": {value: "\x1b[200~"},
	"PE": {value: "\x1b[201~"},
	// Focus reporting, mode 1004.
	"fe": {value: "\x1b[?1004h"},
	"fd": {value: "\x1b[?1004l"},
	// Strikethrough.
	"smxx": {value: "\x1b[9m"},
	"rmxx": {value: "\x1b[29m"},
}

// termcapReply answers an XTGETTCAP request: names in hexadecimal, separated
// by semicolons. Each name gets its own reply, DCS 1 + r name=value ST for one
// this emulator has and DCS 0 + r ST for one it does not, the forms in xterm's
// ctlseqs. The name is echoed as the guest spelled it, so a guest that sent
// lowercase hex matches its own request.
func termcapReply(req string) string {
	var b strings.Builder
	for name := range strings.SplitSeq(req, ";") {
		raw, err := hex.DecodeString(name)
		cap, ok := termcaps[string(raw)]
		if err != nil || name == "" || !ok {
			b.WriteString("\x1bP0+r\x1b\\")
			continue
		}
		b.WriteString("\x1bP1+r")
		b.WriteString(name)
		if !cap.boolean {
			b.WriteByte('=')
			b.WriteString(strings.ToUpper(hex.EncodeToString([]byte(cap.value))))
		}
		b.WriteString("\x1b\\")
	}
	return b.String()
}

// sgrReport renders a pen as the SGR parameters that recreate it from a reset,
// for DECRQSS. It starts with 0, as xterm's reply does, so sending it back
// clears anything the pen does not name.
//
// The colours are written the way the pen holds them: a palette slot nothing
// has redefined as 30-37 or 90-97, an indexed colour as 38;5;n, and anything
// else, a themed palette slot included, as 38;2;r;g;b. Each of those reads
// back to the same colour here.
func sgrReport(pen uv.Style) string {
	var b strings.Builder
	b.WriteByte('0')
	param := func(s string) {
		b.WriteByte(';')
		b.WriteString(s)
	}
	attr := func(bit uint8, code string) {
		if pen.Attrs&bit != 0 {
			param(code)
		}
	}
	attr(uv.AttrBold, "1")
	attr(uv.AttrFaint, "2")
	attr(uv.AttrItalic, "3")
	switch pen.Underline {
	case ansi.UnderlineNone:
	case ansi.UnderlineSingle:
		param("4")
	default:
		param("4:" + strconv.Itoa(int(pen.Underline)))
	}
	attr(uv.AttrBlink, "5")
	attr(uv.AttrRapidBlink, "6")
	attr(uv.AttrReverse, "7")
	attr(uv.AttrConceal, "8")
	attr(uv.AttrStrikethrough, "9")
	if pen.Fg != nil {
		param(sgrColor(pen.Fg, 30, 90, 38))
	}
	if pen.Bg != nil {
		param(sgrColor(pen.Bg, 40, 100, 48))
	}
	if pen.UnderlineColor != nil {
		param(sgrColor(pen.UnderlineColor, -1, -1, 58))
	}
	return b.String()
}

// sgrColor renders one colour. base and bright are the first codes of the
// normal and bright palette ranges, or -1 where the attribute has none (the
// underline colour), and ext is the extended-colour code.
func sgrColor(c color.Color, base, bright, ext int) string {
	e := strconv.Itoa(ext)
	switch c := c.(type) {
	case ansi.BasicColor:
		switch {
		case base >= 0 && c < 8:
			return strconv.Itoa(base + int(c))
		case bright >= 0 && c < 16:
			return strconv.Itoa(bright + int(c) - 8)
		default:
			return e + ";5;" + strconv.Itoa(int(c))
		}
	case ansi.IndexedColor:
		return e + ";5;" + strconv.Itoa(int(c))
	}
	r, g, bl, _ := c.RGBA()
	return e + ";2;" + strconv.Itoa(int(r>>8)) + ";" + strconv.Itoa(int(g>>8)) + ";" + strconv.Itoa(int(bl>>8))
}

// cursorStyleReport renders the cursor shape as the DECSCUSR parameter that
// sets it: 1 and 2 for a blinking and a steady block, 3 and 4 for an
// underline, 5 and 6 for a bar. It is the inverse of the DECSCUSR handler.
func cursorStyleReport(style CursorStyle, steady bool) int {
	n := int(style)*2 + 1
	if steady {
		n++
	}
	return n
}
